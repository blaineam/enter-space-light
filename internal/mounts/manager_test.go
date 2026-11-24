package mounts

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/blaineam/enter-space-light/internal/rcl"
	"github.com/blaineam/enter-space-light/internal/settings"
)

type fakeRC struct {
	mu        sync.Mutex
	live      map[string]bool
	failNext  int
	mounts    []rcl.Params
	unmounts  []string
	dropMount bool // listmounts omits everything (FUSE died)
}

func (f *fakeRC) count(kind string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	if kind == "mount" {
		return len(f.mounts)
	}
	return len(f.unmounts)
}

func (f *fakeRC) Call(_ context.Context, method string, in rcl.Params) (rcl.Params, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch method {
	case "mount/mount":
		if f.failNext > 0 {
			f.failNext--
			return nil, errors.New("dial tcp: no such host")
		}
		f.mounts = append(f.mounts, in)
		mp := in["mountPoint"].(string)
		if mp == "*" {
			mp = "Z:"
		}
		f.live[mp] = true
		return rcl.Params{"mountPoint": mp}, nil
	case "mount/unmount":
		mp := in["mountPoint"].(string)
		f.unmounts = append(f.unmounts, mp)
		delete(f.live, mp)
		return rcl.Params{}, nil
	case "mount/unmountall":
		f.live = map[string]bool{}
		return rcl.Params{}, nil
	case "mount/listmounts":
		var list []map[string]any
		if !f.dropMount {
			for mp := range f.live {
				list = append(list, map[string]any{"MountPoint": mp})
			}
		}
		return rcl.Params{"mountPoints": list}, nil
	}
	return nil, errors.New("unexpected " + method)
}

type fakePlat struct {
	mu        sync.Mutex
	unhealthy bool
	released  []string
	forgotten []string
}

func (p *fakePlat) Prepare(m settings.Mount, _ settings.Prefs) (string, error) {
	if m.MountPoint != "" {
		return m.MountPoint, nil
	}
	return "/cloud/" + m.Name, nil
}
func (p *fakePlat) Healthy(string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.unhealthy {
		return errors.New("transport endpoint is not connected")
	}
	return nil
}
func (p *fakePlat) Released(_ settings.Mount, mp string) {
	p.mu.Lock()
	p.released = append(p.released, mp)
	p.mu.Unlock()
}
func (p *fakePlat) Mounted(settings.Mount, string, settings.Prefs) {}
func (p *fakePlat) Forget(_ settings.Mount, mp string) {
	p.mu.Lock()
	p.forgotten = append(p.forgotten, mp)
	p.mu.Unlock()
}
func (p *fakePlat) FreeBytes(string) int64 { return 100 << 30 }

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func setup(t *testing.T, ms ...settings.Mount) (*Manager, *fakeRC, *fakePlat, *clock, *settings.Store) {
	t.Helper()
	st, err := settings.Open(filepath.Join(t.TempDir(), "s.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Update(func(d *settings.Settings) error { d.Mounts = ms; return nil }); err != nil {
		t.Fatal(err)
	}
	rc := &fakeRC{live: map[string]bool{}}
	pl := &fakePlat{}
	ck := &clock{t: time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)}
	mg := New(Config{
		RC: rc, Store: st, Platform: pl, GOOS: "linux", Now: ck.Now,
		Tick: time.Second, HealthEvery: 30 * time.Second, HealthTimeout: time.Second,
		Backoff: []time.Duration{5 * time.Second, 20 * time.Second},
		Resolve: func(string) (string, string, bool) { return "drive", "", true },
	})
	doc := st.Get()
	mg.mu.Lock()
	for _, m := range doc.Mounts {
		mg.ensure(m.ID).Want = m.AutoMount
	}
	mg.mu.Unlock()
	return mg, rc, pl, ck, st
}

func step(mg *Manager) { mg.Reconcile(context.Background()); mg.Wait() }

func status(t *testing.T, mg *Manager, id string) State {
	t.Helper()
	s, ok := mg.Get(id)
	if !ok {
		t.Fatalf("no state for %s", id)
	}
	return s
}

func TestAutoMountMountsWithTunedParams(t *testing.T) {
	mg, rc, _, _, _ := setup(t, settings.Mount{ID: "a", Name: "Drive", Remote: "gdrive:", AutoMount: true},
		settings.Mount{ID: "b", Name: "Manual", Remote: "nas:"})
	step(mg)
	if s := status(t, mg, "a"); s.Status != StatusMounted || s.MountPoint != "/cloud/Drive" {
		t.Fatalf("auto-mount not mounted: %+v", s)
	}
	if s := status(t, mg, "b"); s.Status != StatusUnmounted {
		t.Fatalf("manual mount should stay down: %+v", s)
	}
	p := rc.mounts[0]
	if p["fs"] != "gdrive:" || p["vfs_cache_mode"] != "full" || p["tpslimit"] != 10.0 {
		t.Fatalf("mount params not tuned: %v", p)
	}
}

func TestFailedMountBacksOffThenRecovers(t *testing.T) {
	mg, rc, _, ck, _ := setup(t, settings.Mount{ID: "a", Name: "Drive", Remote: "gdrive:", AutoMount: true})
	rc.failNext = 2
	step(mg)
	s := status(t, mg, "a")
	if s.Status != StatusRetrying || s.Error == "" || !s.NextRetry.Equal(ck.Now().Add(5*time.Second)) {
		t.Fatalf("first failure should back off 5s: %+v", s)
	}
	step(mg) // too early: no attempt
	if len(rc.mounts) != 0 || status(t, mg, "a").Attempts != 1 {
		t.Fatal("retried before the backoff elapsed")
	}
	ck.Add(6 * time.Second)
	step(mg)
	if s := status(t, mg, "a"); s.Attempts != 2 || !s.NextRetry.Equal(ck.Now().Add(20*time.Second)) {
		t.Fatalf("second failure should back off 20s: %+v", s)
	}
	ck.Add(21 * time.Second)
	step(mg)
	if s := status(t, mg, "a"); s.Status != StatusMounted || s.Attempts != 0 || s.Error != "" {
		t.Fatalf("should recover and reset: %+v", s)
	}
}

func TestTwoFailedHealthChecksRemount(t *testing.T) {
	mg, rc, pl, ck, _ := setup(t, settings.Mount{ID: "a", Name: "Drive", Remote: "gdrive:", AutoMount: true})
	step(mg)
	pl.unhealthy = true
	ck.Add(31 * time.Second)
	step(mg)
	if s := status(t, mg, "a"); s.Status != StatusMounted || s.Error == "" {
		t.Fatalf("one strike should warn, not remount: %+v", s)
	}
	ck.Add(31 * time.Second)
	step(mg)
	mg.Wait()
	if rc.count("unmount") != 1 {
		t.Fatalf("two strikes should unmount: %v", rc.unmounts)
	}
	pl.unhealthy = false
	step(mg)
	if s := status(t, mg, "a"); s.Status != StatusMounted || len(rc.mounts) != 2 {
		t.Fatalf("should remount: %+v mounts=%d", s, len(rc.mounts))
	}
}

func TestVanishedMountIsDetected(t *testing.T) {
	mg, rc, _, ck, _ := setup(t, settings.Mount{ID: "a", Name: "Drive", Remote: "gdrive:", AutoMount: true})
	step(mg)
	rc.mu.Lock()
	rc.dropMount = true
	rc.mu.Unlock()
	for i := 0; i < 2; i++ {
		ck.Add(31 * time.Second)
		step(mg)
	}
	mg.Wait()
	rc.mu.Lock()
	rc.dropMount = false
	rc.mu.Unlock()
	step(mg)
	if rc.count("mount") != 2 {
		t.Fatalf("a mount that disappeared from rclone must be remounted (mounts=%d)", len(rc.mounts))
	}
}

func TestResumeFromSleepChecksHealthImmediately(t *testing.T) {
	mg, _, pl, ck, _ := setup(t, settings.Mount{ID: "a", Name: "Drive", Remote: "gdrive:", AutoMount: true})
	step(mg)
	ck.Add(2 * time.Second)
	step(mg)
	pl.unhealthy = true
	ck.Add(5 * time.Second) // < HealthEvery, but…
	step(mg)
	if status(t, mg, "a").Error != "" {
		t.Fatal("no health check expected yet")
	}
	ck.Add(2 * time.Hour) // …a big gap is a resume
	step(mg)
	if status(t, mg, "a").Error == "" {
		t.Fatal("resume should trigger an immediate health check")
	}
}

func TestSettingsChangeRemountsAndUnwantUnmounts(t *testing.T) {
	mg, rc, _, _, st := setup(t, settings.Mount{ID: "a", Name: "Drive", Remote: "gdrive:", AutoMount: true})
	step(mg)
	_, _ = st.Update(func(d *settings.Settings) error { d.Mounts[0].ReadOnly = true; return nil })
	step(mg)
	step(mg)
	if len(rc.mounts) != 2 || rc.mounts[1]["read_only"] != true {
		t.Fatalf("changing read-only should remount with it: %d %v", len(rc.mounts), rc.mounts)
	}
	_, _ = st.Update(func(d *settings.Settings) error {
		d.Mounts[0].Name = "Renamed"
		d.Mounts[0].MountPoint = "/cloud/Drive"
		return nil
	})
	step(mg)
	if len(rc.mounts) != 2 {
		t.Fatal("a cosmetic change must not remount")
	}
	if err := mg.SetWant("a", false); err != nil {
		t.Fatal(err)
	}
	step(mg)
	if s := status(t, mg, "a"); s.Status != StatusUnmounted {
		t.Fatalf("unwanted mount should unmount: %+v", s)
	}
}

func TestDeletedMountIsUnmountedAndForgotten(t *testing.T) {
	mg, rc, pl, _, st := setup(t, settings.Mount{ID: "a", Name: "Drive", Remote: "gdrive:", AutoMount: true})
	step(mg)
	_, _ = st.Update(func(d *settings.Settings) error { d.Mounts = nil; return nil })
	step(mg)
	if len(rc.unmounts) != 1 || len(pl.forgotten) != 1 {
		t.Fatalf("deleted mount should be unmounted and its bookmark removed: %v %v", rc.unmounts, pl.forgotten)
	}
	if len(mg.Snapshot()) != 0 {
		t.Fatal("deleted mount still listed")
	}
}

func TestLockedConfigWaits(t *testing.T) {
	mg, rc, _, _, _ := setup(t, settings.Mount{ID: "a", Name: "Drive", Remote: "gdrive:", AutoMount: true})
	locked := true
	mg.c.Locked = func() bool { return locked }
	step(mg)
	if s := status(t, mg, "a"); s.Status != StatusLocked || len(rc.mounts) != 0 {
		t.Fatalf("locked config must not attempt a mount: %+v", s)
	}
	locked = false
	step(mg)
	if status(t, mg, "a").Status != StatusMounted {
		t.Fatal("should mount once unlocked")
	}
}

func TestShutdownUnmountsAll(t *testing.T) {
	mg, rc, _, _, _ := setup(t, settings.Mount{ID: "a", Name: "A", Remote: "a:", AutoMount: true}, settings.Mount{ID: "b", Name: "B", Remote: "b:", AutoMount: true})
	step(mg)
	mg.Shutdown(time.Second)
	if len(rc.live) != 0 {
		t.Fatalf("still mounted after shutdown: %v", rc.live)
	}
}
