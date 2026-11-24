package jobs

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/blaineam/enter-space-light/internal/rcl"
	"github.com/blaineam/enter-space-light/internal/settings"
	"github.com/blaineam/enter-space-light/internal/volumes"
)

// fakeRC finishes every async job on its first status poll with the next
// scripted outcome ("" = success).
type fakeRC struct {
	mu       sync.Mutex
	outcomes []string
	calls    []rcl.Params
	methods  []string
	nextID   int64
	results  map[int64]string
	block    chan struct{} // if set, status polls report "running" until closed
}

func (f *fakeRC) Call(_ context.Context, method string, in rcl.Params) (rcl.Params, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch method {
	case "sync/sync", "sync/copy", "sync/move", "sync/bisync":
		f.methods = append(f.methods, method)
		f.calls = append(f.calls, in)
		f.nextID++
		out := ""
		if len(f.outcomes) > 0 {
			out, f.outcomes = f.outcomes[0], f.outcomes[1:]
		}
		if f.results == nil {
			f.results = map[int64]string{}
		}
		f.results[f.nextID] = out
		return rcl.Params{"jobid": f.nextID}, nil
	case "job/status":
		id := in["jobid"].(int64)
		if f.block != nil {
			select {
			case <-f.block:
			default:
				return rcl.Params{"finished": false}, nil
			}
		}
		res := f.results[id]
		return rcl.Params{"finished": true, "success": res == "", "error": res}, nil
	case "job/stop":
		return rcl.Params{}, nil
	case "core/stats":
		return rcl.Params{"bytes": int64(42), "transfers": int64(3)}, nil
	case "core/stats-reset":
		return rcl.Params{}, nil
	}
	return nil, errors.New("unexpected " + method)
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

func setup(t *testing.T, js ...settings.Job) (*Runner, *fakeRC, *settings.Store, string) {
	t.Helper()
	dir := t.TempDir()
	st, _ := settings.Open(filepath.Join(dir, "s.json"))
	if _, err := st.Update(func(d *settings.Settings) error { d.Jobs = js; return nil }); err != nil {
		t.Fatal(err)
	}
	rc := &fakeRC{}
	book := filepath.Join(dir, "history.json")
	r := New(Config{RC: rc, Store: st, BookPath: book, Workdir: "/w", Poll: time.Millisecond,
		Retry:   []time.Duration{time.Millisecond, time.Millisecond},
		Resolve: func(string) (string, string, bool) { return "s3", "", true }})
	return r, rc, st, book
}

func job(id string) settings.Job {
	return settings.Job{ID: id, Name: id, Kind: settings.KindCopy, Enabled: true, Source: "/src", Dest: "b2:dst"}
}

func TestSuccessRecordsHistoryAndStats(t *testing.T) {
	r, rc, _, book := setup(t, job("j"))
	if err := r.Start("j", "manual", ""); err != nil {
		t.Fatal(err)
	}
	r.Wait()
	s := r.Snapshot()[0]
	if s.Last == nil || !s.Last.OK || s.Last.Bytes != 42 || s.Last.Files != 3 || s.Last.Attempts != 1 {
		t.Fatalf("bad result: %+v", s.Last)
	}
	if rc.methods[0] != "sync/copy" || rc.calls[0]["_async"] != true || rc.calls[0]["_group"] != "esl/j" {
		t.Fatalf("bad call: %v %v", rc.methods, rc.calls[0])
	}
	again := New(Config{RC: rc, Store: r.c.Store, BookPath: book})
	if h := again.Snapshot()[0].History; len(h) != 1 {
		t.Fatalf("history not persisted: %v", h)
	}
}

func TestTransientFailureRetriesThenSucceeds(t *testing.T) {
	r, rc, _, _ := setup(t, job("j"))
	rc.outcomes = []string{"connection reset by peer", ""}
	_ = r.Start("j", "manual", "")
	r.Wait()
	last := r.Snapshot()[0].Last
	if !last.OK || last.Attempts != 2 {
		t.Fatalf("should succeed on the 2nd attempt: %+v", last)
	}
}

func TestRetriesAreBounded(t *testing.T) {
	r, rc, _, _ := setup(t, job("j"))
	rc.outcomes = []string{"timeout", "timeout", "timeout", "timeout"}
	var failed string
	r.c.OnFailure = func(_ settings.Job, e string) { failed = e }
	_ = r.Start("j", "manual", "")
	r.Wait()
	last := r.Snapshot()[0].Last
	if last.OK || last.Attempts != 3 || failed != "timeout" {
		t.Fatalf("1 try + 2 retries then give up and notify: %+v %q", last, failed)
	}
}

func TestPermanentFailureDoesNotRetry(t *testing.T) {
	r, rc, _, _ := setup(t, job("j"))
	rc.outcomes = []string{"didn't find section in config file"}
	_ = r.Start("j", "manual", "")
	r.Wait()
	if last := r.Snapshot()[0].Last; last.Attempts != 1 {
		t.Fatalf("config errors won't fix themselves: %+v", last)
	}
}

func TestAlreadyRunningAndStop(t *testing.T) {
	r, rc, _, _ := setup(t, job("j"))
	rc.block = make(chan struct{})
	if err := r.Start("j", "manual", ""); err != nil {
		t.Fatal(err)
	}
	if err := r.Start("j", "manual", ""); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second start should be refused: %v", err)
	}
	if !r.Stop("j") {
		t.Fatal("stop found nothing")
	}
	r.Wait()
	if last := r.Snapshot()[0].Last; last.OK || last.Error != "stopped" {
		t.Fatalf("stopped run: %+v", last)
	}
}

func TestBisyncResyncsOnlyOnce(t *testing.T) {
	j := job("b")
	j.Kind = settings.KindBisync
	r, rc, _, _ := setup(t, j)
	_ = r.Start("b", "manual", "")
	r.Wait()
	_ = r.Start("b", "manual", "")
	r.Wait()
	if rc.calls[0]["resync"] != true || rc.calls[1]["resync"] != nil {
		t.Fatalf("resync on first run only: %v / %v", rc.calls[0]["resync"], rc.calls[1]["resync"])
	}
}

func TestVolumeJobNeedsAVolume(t *testing.T) {
	j := job("v")
	j.Dest = "{volume}/Backup"
	j.Triggers.OnVolume = &settings.VolumeMatch{ID: "AAAA-1111"}
	r, rc, _, _ := setup(t, j)
	if err := r.Start("v", "manual", ""); err == nil {
		t.Fatal("a {volume} job can't run without its drive")
	}
	if err := r.Start("v", "drive", `E:\`); err != nil {
		t.Fatal(err)
	}
	r.Wait()
	if rc.calls[0]["dstFs"] != `E:\Backup` {
		t.Fatalf("volume not expanded: %v", rc.calls[0]["dstFs"])
	}
}

func scheduler(r *Runner, st *settings.Store, ck *clock) *Scheduler {
	return &Scheduler{Runner: r, Store: st, Now: ck.Now, StartDelay: 30 * time.Second, VolumeCooldown: 5 * time.Minute}
}

func TestIntervalFiresAfterEachPeriod(t *testing.T) {
	j := job("j")
	j.Triggers.Every = "1h"
	r, rc, st, _ := setup(t, j)
	base := time.Date(2026, 11, 23, 9, 0, 0, 0, time.Local)
	ck := &clock{t: base}
	r.c.Now = ck.Now
	s := scheduler(r, st, ck)
	s.Tick()
	r.Wait()
	if len(rc.calls) != 0 {
		t.Fatal("a never-run interval job waits one period from start")
	}
	ck.Set(base.Add(61 * time.Minute))
	s.Tick()
	r.Wait()
	ck.Set(base.Add(90 * time.Minute))
	s.Tick()
	r.Wait()
	if len(rc.calls) != 1 {
		t.Fatalf("want 1 run, got %d", len(rc.calls))
	}
	ck.Set(base.Add(10 * time.Hour)) // laptop slept through 8 periods
	s.Tick()
	r.Wait()
	if len(rc.calls) != 2 {
		t.Fatalf("missed periods collapse into one catch-up run, got %d", len(rc.calls))
	}
}

func TestDailyArmsThenFiresAndCatchesUp(t *testing.T) {
	j := job("d")
	j.Triggers.Daily = "02:30"
	r, rc, st, _ := setup(t, j)
	ck := &clock{t: time.Date(2026, 11, 23, 9, 0, 0, 0, time.Local)}
	r.c.Now = ck.Now
	s := scheduler(r, st, ck)
	s.Tick()
	r.Wait()
	if len(rc.calls) != 0 {
		t.Fatal("this morning's slot predates the job: must not fire")
	}
	ck.Set(time.Date(2026, 11, 24, 2, 30, 0, 0, time.Local))
	s.Tick()
	r.Wait()
	if len(rc.calls) != 1 {
		t.Fatal("should fire at the slot")
	}
	s.Tick()
	r.Wait()
	// Asleep through the 25th; wakes the 26th at 07:00 → one catch-up run.
	ck.Set(time.Date(2026, 11, 26, 7, 0, 0, 0, time.Local))
	s.Tick()
	r.Wait()
	s.Tick()
	r.Wait()
	if len(rc.calls) != 2 {
		t.Fatalf("missed slot should fire once on wake, got %d", len(rc.calls))
	}
}

func TestOnStartWaitsForTheNetworkDelay(t *testing.T) {
	j := job("s")
	j.Triggers.OnStart = true
	r, rc, st, _ := setup(t, j)
	base := time.Date(2026, 11, 23, 9, 0, 0, 0, time.Local)
	ck := &clock{t: base}
	r.c.Now = ck.Now
	s := scheduler(r, st, ck)
	s.Tick()
	r.Wait()
	ck.Set(base.Add(31 * time.Second))
	s.Tick()
	r.Wait()
	ck.Set(base.Add(time.Hour))
	s.Tick()
	r.Wait()
	if len(rc.calls) != 1 {
		t.Fatalf("on-start runs exactly once, after the delay: %d", len(rc.calls))
	}
}

func TestVolumeArrivalFiresMatchingJobsWithCooldown(t *testing.T) {
	j := job("v")
	j.Dest = "{volume}/Backup"
	j.Triggers.OnVolume = &settings.VolumeMatch{ID: "AAAA-1111"}
	other := job("o")
	other.Dest = "{volume}/X"
	other.Triggers.OnVolume = &settings.VolumeMatch{Label: "Other"}
	r, rc, st, _ := setup(t, j, other)
	ck := &clock{t: time.Date(2026, 11, 23, 9, 0, 0, 0, time.Local)}
	r.c.Now = ck.Now
	s := scheduler(r, st, ck)
	usb := volumes.Volume{ID: "aaaa-1111", Label: "Backup", Root: "/media/u/Backup", Removable: true}
	s.VolumeArrived(usb)
	r.Wait()
	s.VolumeArrived(usb) // flaky hub, 0s later
	r.Wait()
	if len(rc.calls) != 1 || rc.calls[0]["dstFs"] != "/media/u/Backup/Backup" {
		t.Fatalf("one run for the matching drive: %v", rc.calls)
	}
	ck.Set(ck.Now().Add(6 * time.Minute))
	s.VolumeArrived(usb)
	r.Wait()
	if len(rc.calls) != 2 {
		t.Fatal("after the cooldown a reconnect runs again")
	}
	s.VolumeArrived(volumes.Volume{ID: "SYS", Label: "Other", System: true})
	r.Wait()
	if len(rc.calls) != 2 {
		t.Fatal("the system drive never triggers")
	}
}

func TestDisabledJobsNeverFire(t *testing.T) {
	j := job("j")
	j.Enabled = false
	j.Triggers.OnStart = true
	r, rc, st, _ := setup(t, j)
	ck := &clock{t: time.Date(2026, 11, 23, 9, 0, 0, 0, time.Local)}
	s := scheduler(r, st, ck)
	s.StartDelay = 0
	s.Tick()
	r.Wait()
	if len(rc.calls) != 0 {
		t.Fatal("disabled job ran")
	}
}
