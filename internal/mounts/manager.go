// Package mounts keeps the user's remotes mounted.
//
// The manager is a reconcile loop over desired state: a mount the user
// wants is mounted, re-mounted with backoff when it fails, health-checked
// while up, and re-checked immediately after the machine wakes from sleep.
// Nothing here blocks on a slow remote: health checks run with a timeout
// and never pile up.
package mounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/blaineam/enter-space-light/internal/rcl"
	"github.com/blaineam/enter-space-light/internal/settings"
	"github.com/blaineam/enter-space-light/internal/tune"
)

// Status of one mount.
type Status string

// Mount statuses.
const (
	StatusUnmounted Status = "unmounted"
	StatusMounting  Status = "mounting"
	StatusMounted   Status = "mounted"
	StatusRetrying  Status = "retrying"
	StatusLocked    Status = "locked" // rclone.conf is encrypted and not unlocked yet
)

// State is what the UI shows for a mount.
type State struct {
	ID         string    `json:"id"`
	Status     Status    `json:"status"`
	Want       bool      `json:"want"`
	MountPoint string    `json:"mountPoint,omitempty"`
	Error      string    `json:"error,omitempty"`
	Since      time.Time `json:"since"`
	NextRetry  time.Time `json:"nextRetry,omitzero"`
	Attempts   int       `json:"attempts,omitempty"`
}

// Platform is the OS-specific part of mounting.
type Platform interface {
	// Prepare validates and readies the mount point, returning the value
	// to pass to rclone ("*" for an automatic Windows drive letter).
	Prepare(m settings.Mount, p settings.Prefs) (string, error)
	// Healthy checks the mounted filesystem answers. Called with a timeout.
	Healthy(mountPoint string) error
	// Released runs after an unmount (remove an auto-created folder…).
	Released(m settings.Mount, mountPoint string)
	// Mounted runs after a successful mount (file-manager bookmark…).
	Mounted(m settings.Mount, mountPoint string, p settings.Prefs)
	// Forget runs when a mount is deleted from settings.
	Forget(m settings.Mount, mountPoint string)
	// FreeBytes reports free space where the VFS cache lives.
	FreeBytes(dir string) int64
}

// Config wires the manager.
type Config struct {
	RC       rcl.Caller
	Store    *settings.Store
	Platform Platform
	GOOS     string
	CacheDir string
	Resolve  tune.Resolver
	Locked   func() bool // rclone.conf still encrypted
	Logf     func(format string, args ...any)
	OnChange func()
	// Timing (tests shrink these).
	Tick          time.Duration // reconcile cadence
	HealthEvery   time.Duration // health-check cadence while mounted
	HealthTimeout time.Duration
	Backoff       []time.Duration
	Now           func() time.Time
}

type entry struct {
	State
	cfg            string // JSON of the settings.Mount it was mounted with
	failures       int    // consecutive failed health checks
	healthInflight bool
	lastHealth     time.Time
}

// Manager owns all mounts.
type Manager struct {
	c       Config
	mu      sync.Mutex
	entries map[string]*entry
	kick    chan struct{}
	lastRun time.Time
	busy    sync.WaitGroup
}

// New builds a manager. Call Start to begin reconciling.
func New(c Config) *Manager {
	if c.Tick == 0 {
		c.Tick = 5 * time.Second
	}
	if c.HealthEvery == 0 {
		c.HealthEvery = 30 * time.Second
	}
	if c.HealthTimeout == 0 {
		c.HealthTimeout = 20 * time.Second
	}
	if len(c.Backoff) == 0 {
		c.Backoff = []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute}
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Logf == nil {
		c.Logf = func(string, ...any) {}
	}
	if c.Locked == nil {
		c.Locked = func() bool { return false }
	}
	return &Manager{c: c, entries: map[string]*entry{}, kick: make(chan struct{}, 1)}
}

// Start marks every auto-mount as wanted and runs the loop until ctx ends.
func (mg *Manager) Start(ctx context.Context) {
	doc := mg.c.Store.Get()
	mg.mu.Lock()
	for _, m := range doc.Mounts {
		e := mg.ensure(m.ID)
		e.Want = m.AutoMount
	}
	mg.mu.Unlock()
	go mg.loop(ctx)
}

func (mg *Manager) ensure(id string) *entry {
	e, ok := mg.entries[id]
	if !ok {
		e = &entry{State: State{ID: id, Status: StatusUnmounted, Since: mg.c.Now()}}
		mg.entries[id] = e
	}
	return e
}

// Kick asks the loop to reconcile now.
func (mg *Manager) Kick() {
	select {
	case mg.kick <- struct{}{}:
	default:
	}
}

func (mg *Manager) loop(ctx context.Context) {
	t := time.NewTicker(mg.c.Tick)
	defer t.Stop()
	mg.Reconcile(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-mg.kick:
		}
		mg.Reconcile(ctx)
	}
}

// Reconcile performs one pass. Exported for tests.
func (mg *Manager) Reconcile(ctx context.Context) {
	now := mg.c.Now()
	doc := mg.c.Store.Get()

	mg.mu.Lock()
	// A wall-clock gap much longer than the tick means we were asleep:
	// network sockets are dead and every mount needs checking right away.
	resumed := !mg.lastRun.IsZero() && now.Sub(mg.lastRun) > 3*mg.c.Tick+10*time.Second
	mg.lastRun = now

	known := map[string]settings.Mount{}
	for _, m := range doc.Mounts {
		known[m.ID] = m
		mg.ensure(m.ID)
	}
	type action struct {
		kind string // mount, unmount, health, forget
		m    settings.Mount
		e    *entry
	}
	var todo []action
	for id, e := range mg.entries {
		m, exists := known[id]
		if !exists {
			todo = append(todo, action{"forget", settings.Mount{ID: id}, e})
			continue
		}
		if resumed && e.Status == StatusMounted {
			e.lastHealth = time.Time{}
		}
		cfg := mountKey(m)
		switch {
		case e.Want && e.Status == StatusMounted && e.cfg != "" && e.cfg != cfg:
			// Settings changed while mounted: remount with the new ones.
			todo = append(todo, action{"remount", m, e})
		case e.Want && (e.Status == StatusUnmounted || e.Status == StatusRetrying || e.Status == StatusLocked):
			if !e.NextRetry.IsZero() && now.Before(e.NextRetry) {
				continue
			}
			if mg.c.Locked() {
				mg.set(e, StatusLocked, "rclone.conf is encrypted — unlock it to mount")
				continue
			}
			e.Status = StatusMounting
			todo = append(todo, action{"mount", m, e})
		case e.Want && e.Status == StatusMounted && !e.healthInflight && now.Sub(e.lastHealth) >= mg.c.HealthEvery:
			e.healthInflight = true
			todo = append(todo, action{"health", m, e})
		case !e.Want && e.Status == StatusMounted:
			todo = append(todo, action{"unmount", m, e})
		case !e.Want && (e.Status == StatusRetrying || e.Status == StatusLocked):
			mg.set(e, StatusUnmounted, "")
		}
	}
	mg.mu.Unlock()

	for _, a := range todo {
		switch a.kind {
		case "mount":
			// Concurrent so one unreachable server can't hold up the
			// others; Status=mounting keeps the loop from re-trying it.
			mg.busy.Add(1)
			go func(m settings.Mount, e *entry) {
				defer mg.busy.Done()
				mg.doMount(ctx, m, e)
			}(a.m, a.e)
		case "remount":
			mg.mu.Lock()
			a.e.Status = StatusMounting
			mg.mu.Unlock()
			mg.busy.Add(1)
			go func(m settings.Mount, e *entry) {
				defer mg.busy.Done()
				mg.doUnmount(m, e)
				mg.mu.Lock()
				e.NextRetry = time.Time{}
				mg.mu.Unlock()
				mg.Kick()
			}(a.m, a.e)
		case "unmount":
			mg.doUnmount(a.m, a.e)
		case "health":
			mg.busy.Add(1)
			go func(m settings.Mount, e *entry) {
				defer mg.busy.Done()
				mg.doHealth(m, e)
			}(a.m, a.e)
		case "forget":
			mg.mu.Lock()
			mp := a.e.MountPoint
			mounted := a.e.Status == StatusMounted
			delete(mg.entries, a.e.ID)
			mg.mu.Unlock()
			if mounted {
				mg.unmountPoint(mp)
			}
			mg.c.Platform.Forget(a.m, mp)
			mg.changed()
		}
	}
}

func mountKey(m settings.Mount) string {
	// Fields that need a remount when they change. Name/AutoMount don't.
	b, _ := json.Marshal(struct {
		R, P string
		RO   bool
		O    map[string]string
	}{m.Remote, m.MountPoint, m.ReadOnly, m.Options})
	return string(b)
}

func (mg *Manager) set(e *entry, s Status, msg string) {
	if e.Status != s || e.Error != msg {
		e.Since = mg.c.Now()
	}
	e.Status = s
	e.Error = msg
}

func (mg *Manager) changed() {
	if mg.c.OnChange != nil {
		mg.c.OnChange()
	}
}

func (mg *Manager) cacheBudget(doc settings.Settings) string {
	n := 0
	for _, m := range doc.Mounts {
		if m.AutoMount {
			n++
		}
	}
	return tune.CacheBudget(mg.c.Platform.FreeBytes(mg.c.CacheDir), n, doc.Prefs.CacheMaxSize)
}

func (mg *Manager) doMount(ctx context.Context, m settings.Mount, e *entry) {
	mg.changed()
	doc := mg.c.Store.Get()
	fail := func(err error) {
		mg.mu.Lock()
		e.Attempts++
		i := e.Attempts - 1
		if i >= len(mg.c.Backoff) {
			i = len(mg.c.Backoff) - 1
		}
		e.NextRetry = mg.c.Now().Add(mg.c.Backoff[i])
		mg.set(e, StatusRetrying, friendly(err))
		mg.mu.Unlock()
		mg.c.Logf("mount %s (%s) failed, retrying in %s: %v", m.Name, m.Remote, mg.c.Backoff[i], err)
		mg.changed()
	}
	mp, err := mg.c.Platform.Prepare(m, doc.Prefs)
	if err != nil {
		fail(err)
		return
	}
	backend := tune.BackendOf(m.Remote, mg.c.Resolve)
	p := tune.MountParams(m, backend, tune.Env{GOOS: mg.c.GOOS, CacheBudget: mg.cacheBudget(doc)})
	in := rcl.Params{"fs": m.Remote, "mountPoint": mp}
	for k, v := range p {
		in[k] = v
	}
	out, err := mg.c.RC.Call(context.Background(), "mount/mount", in)
	if err != nil {
		// Let the platform avoid what just failed (another drive letter).
		if n, ok := mg.c.Platform.(interface{ MountFailed(settings.Mount, string) }); ok {
			n.MountFailed(m, mp)
		}
		fail(err)
		return
	}
	actual, _ := out.GetString("mountPoint")
	if actual == "" {
		actual = mp
	}
	mg.mu.Lock()
	e.MountPoint = actual
	e.cfg = mountKey(m)
	e.Attempts = 0
	e.NextRetry = time.Time{}
	e.failures = 0
	e.lastHealth = mg.c.Now()
	stillWanted := e.Want
	mg.set(e, StatusMounted, "")
	mg.mu.Unlock()
	mg.c.Logf("mounted %s (%s) at %s", m.Name, m.Remote, actual)
	mg.c.Platform.Mounted(m, actual, doc.Prefs)
	mg.changed()
	if !stillWanted { // unmount was requested while we were mounting
		mg.Kick()
	}
}

func (mg *Manager) unmountPoint(mp string) {
	if mp == "" {
		return
	}
	if _, err := mg.c.RC.Call(context.Background(), "mount/unmount", rcl.Params{"mountPoint": mp}); err != nil {
		mg.c.Logf("unmount %s: %v", mp, err)
	}
}

func (mg *Manager) doUnmount(m settings.Mount, e *entry) {
	mg.mu.Lock()
	mp := e.MountPoint
	mg.mu.Unlock()
	mg.unmountPoint(mp)
	mg.c.Platform.Released(m, mp)
	mg.mu.Lock()
	e.MountPoint = ""
	e.cfg = ""
	mg.set(e, StatusUnmounted, "")
	mg.mu.Unlock()
	mg.c.Logf("unmounted %s", m.Name)
	mg.changed()
}

func (mg *Manager) doHealth(m settings.Mount, e *entry) {
	mg.mu.Lock()
	mp := e.MountPoint
	mg.mu.Unlock()

	err := mg.listed(mp)
	if err == nil {
		done := make(chan error, 1)
		go func() { done <- mg.c.Platform.Healthy(mp) }()
		select {
		case err = <-done:
		case <-time.After(mg.c.HealthTimeout):
			err = fmt.Errorf("no response from %s within %s", mp, mg.c.HealthTimeout)
		}
	}

	mg.mu.Lock()
	defer mg.mu.Unlock()
	e.healthInflight = false
	e.lastHealth = mg.c.Now()
	if e.Status != StatusMounted {
		return
	}
	if err == nil {
		e.failures = 0
		if e.Error != "" {
			mg.set(e, StatusMounted, "")
			go mg.changed()
		}
		return
	}
	e.failures++
	mg.c.Logf("health check %s failed (%d): %v", m.Name, e.failures, err)
	if e.failures < 2 {
		mg.set(e, StatusMounted, "not responding: "+friendly(err))
		go mg.changed()
		return
	}
	// Two strikes: tear down and let the loop remount with backoff.
	e.failures = 0
	mg.set(e, StatusRetrying, "remounting: "+friendly(err))
	e.NextRetry = mg.c.Now()
	mg.busy.Add(1)
	go func() {
		defer mg.busy.Done()
		mg.unmountPoint(mp)
		mg.c.Platform.Released(m, mp)
		mg.mu.Lock()
		e.MountPoint = ""
		e.cfg = ""
		mg.mu.Unlock()
		mg.changed()
		mg.Kick()
	}()
}

// listed confirms rclone still has the mount (a FUSE crash or an external
// `fusermount -u` removes it without telling us).
func (mg *Manager) listed(mp string) error {
	out, err := mg.c.RC.Call(context.Background(), "mount/listmounts", nil)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(out["mountPoints"])
	var list []struct {
		MountPoint string `json:"MountPoint"`
	}
	_ = json.Unmarshal(raw, &list)
	for _, l := range list {
		if strings.EqualFold(strings.TrimRight(l.MountPoint, `\/`), strings.TrimRight(mp, `\/`)) {
			return nil
		}
	}
	return errors.New("the mount disappeared")
}

// SetWant mounts (true) or unmounts (false) a mount on the user's request.
func (mg *Manager) SetWant(id string, want bool) error {
	doc := mg.c.Store.Get()
	if _, ok := doc.FindMount(id); !ok {
		return fmt.Errorf("no mount %q", id)
	}
	mg.mu.Lock()
	e := mg.ensure(id)
	e.Want = want
	e.NextRetry = time.Time{}
	e.Attempts = 0
	mg.mu.Unlock()
	mg.Kick()
	mg.changed()
	return nil
}

// Snapshot returns every mount's state, in settings order.
func (mg *Manager) Snapshot() []State {
	doc := mg.c.Store.Get()
	mg.mu.Lock()
	defer mg.mu.Unlock()
	out := make([]State, 0, len(doc.Mounts))
	for _, m := range doc.Mounts {
		if e, ok := mg.entries[m.ID]; ok {
			out = append(out, e.State)
		} else {
			out = append(out, State{ID: m.ID, Status: StatusUnmounted, Want: m.AutoMount})
		}
	}
	return out
}

// Get returns one mount's state.
func (mg *Manager) Get(id string) (State, bool) {
	for _, s := range mg.Snapshot() {
		if s.ID == id {
			return s, true
		}
	}
	return State{}, false
}

// Wait blocks until in-flight mounts and health checks finish (tests).
func (mg *Manager) Wait() { mg.busy.Wait() }

// Shutdown unmounts everything, waiting at most d.
func (mg *Manager) Shutdown(d time.Duration) {
	mg.mu.Lock()
	var points []string
	for _, e := range mg.entries {
		e.Want = false
		if e.MountPoint != "" {
			points = append(points, e.MountPoint)
		}
	}
	mg.mu.Unlock()
	sort.Strings(points)
	done := make(chan struct{})
	go func() {
		for _, p := range points {
			mg.unmountPoint(p)
		}
		_, _ = mg.c.RC.Call(context.Background(), "mount/unmountall", nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(d):
		mg.c.Logf("shutdown: unmount still running after %s, exiting anyway", d)
	}
}

// friendly trims rclone's error chains to something a person can act on.
func friendly(err error) string {
	s := err.Error()
	low := strings.ToLower(s)
	switch {
	case strings.Contains(low, "mount stopped before calling init"):
		return "the system refused to create the drive — will try again (on Windows, with another letter)"
	case strings.Contains(low, "mount option specified is not registered"):
		return "this build can't mount on this operating system"
	case strings.Contains(low, "didn't find section in config file"):
		return "this remote no longer exists in rclone.conf"
	case strings.Contains(low, "cannot find winfsp") || strings.Contains(low, "winfsp"):
		if strings.Contains(low, "not") || strings.Contains(low, "cannot") {
			return "WinFsp is not installed — install it from the app's Settings"
		}
	case strings.Contains(low, "fusermount") && strings.Contains(low, "not found"):
		return "FUSE is not installed — install the fuse3 package"
	case strings.Contains(low, "no such host") || strings.Contains(low, "network is unreachable") || strings.Contains(low, "dial tcp"):
		return "offline or the server is unreachable — will keep trying"
	case strings.Contains(low, "directory already mounted") || strings.Contains(low, "already in use"):
		return s
	}
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}
