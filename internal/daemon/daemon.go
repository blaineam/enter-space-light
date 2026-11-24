// Package daemon is the long-running process: it embeds rclone, keeps
// mounts up, runs scheduled syncs, watches for drives, serves the local UI
// and API, and shows the tray icon.
package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/blaineam/enter-space-light/internal/backup"
	"github.com/blaineam/enter-space-light/internal/desktop"
	"github.com/blaineam/enter-space-light/internal/jobs"
	"github.com/blaineam/enter-space-light/internal/lock"
	"github.com/blaineam/enter-space-light/internal/mounts"
	"github.com/blaineam/enter-space-light/internal/paths"
	"github.com/blaineam/enter-space-light/internal/rcl"
	"github.com/blaineam/enter-space-light/internal/secrets"
	"github.com/blaineam/enter-space-light/internal/settings"
	"github.com/blaineam/enter-space-light/internal/tray"
	"github.com/blaineam/enter-space-light/internal/version"
	"github.com/blaineam/enter-space-light/internal/volumes"
	"github.com/blaineam/enter-space-light/internal/winfsp"
	"github.com/rclone/rclone/fs"
)

// ErrAlreadyRunning is returned by Run when another daemon holds the lock.
var ErrAlreadyRunning = lock.ErrHeld

// Options for Run.
type Options struct {
	Tray   bool // show the tray icon (desktop sessions)
	OpenUI bool // open the UI window once started
}

// Handshake is api.json: how clients reach the daemon.
type Handshake struct {
	URL     string `json:"url"`
	Token   string `json:"token"`
	PID     int    `json:"pid"`
	Version string `json:"version"`
}

// Daemon is the running process.
type Daemon struct {
	store  *settings.Store
	mounts *mounts.Manager
	runner *jobs.Runner
	sched  *jobs.Scheduler
	vols   *volumes.Watcher
	engine *backup.Engine
	portal portal
	rc     rcl.Caller
	hub    *hub

	token   string // bearer for the CLI
	session string // cookie for the UI window
	host    string // 127.0.0.1:port

	codesMu sync.Mutex
	codes   map[string]time.Time

	notice           atomic.Value // string shown as a banner
	winfspInstalling atomic.Bool
	failures         atomic.Int64 // unseen sync failures (tray badge)

	quitOnce sync.Once
	quitCh   chan struct{}
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func logf(format string, args ...any) { fs.Logf(nil, "esl: "+format, args...) }

// openSettings loads settings.json; an unreadable file is moved aside so
// the app still starts (it runs unattended at login) and the UI says so.
func openSettings() (*settings.Store, string, error) {
	st, err := settings.Open(paths.SettingsFile())
	if err == nil {
		return st, "", nil
	}
	aside := paths.SettingsFile() + ".broken-" + time.Now().Format("20060102-150405")
	if rerr := os.Rename(paths.SettingsFile(), aside); rerr != nil {
		return nil, "", fmt.Errorf("%w (and it could not be moved aside: %v)", err, rerr)
	}
	st, err2 := settings.Open(paths.SettingsFile())
	if err2 != nil {
		return nil, "", err2
	}
	return st, fmt.Sprintf("Your settings file could not be read (%v). It was moved to %s and Enter Space Light started fresh.", err, aside), nil
}

// Run starts the daemon and blocks until it is told to quit.
func Run(o Options) error {
	if err := paths.EnsureAll(); err != nil {
		return err
	}
	lk, err := lock.Acquire(paths.LockFile())
	if err != nil {
		return err
	}
	defer lk.Release()

	store, notice, err := openSettings()
	if err != nil {
		return err
	}
	prefs := store.Get().Prefs
	level := prefs.LogLevel
	if level == "" {
		level = "NOTICE"
	}
	if err := rcl.Init(rcl.Options{ConfigPath: paths.RcloneConfig(), CacheDir: paths.CacheDir(), LogFile: paths.LogFile(), LogLevel: level}); err != nil {
		return err
	}
	logf("Enter Space Light %s starting (rclone %s, %s/%s), config %s", version.Version, rcl.Version(), runtime.GOOS, runtime.GOARCH, rcl.ConfigPath())

	if rcl.ConfigEncrypted() {
		if pw := secrets.ConfigPassword(); pw != "" {
			if err := rcl.Unlock(pw); err != nil {
				logf("saved rclone.conf password no longer works: %v", err)
			} else {
				logf("rclone.conf unlocked from the system keychain")
			}
		}
	}

	d := &Daemon{
		store:   store,
		rc:      rcl.Local{},
		hub:     newHub(400 * time.Millisecond),
		token:   randHex(32),
		session: randHex(32),
		codes:   map[string]time.Time{},
		quitCh:  make(chan struct{}),
	}
	d.notice.Store(notice)
	if notice != "" {
		logf("%s", notice)
	}

	d.mounts = mounts.New(mounts.Config{
		RC: d.rc, Store: store, Platform: mounts.OSPlatform{}, GOOS: runtime.GOOS,
		CacheDir: paths.CacheDir(), Resolve: rcl.Backend, Locked: rcl.ConfigLocked,
		Logf: logf, OnChange: d.hub.notify,
	})
	d.portal = portal{indexes: map[string]portalIndex{}, alerted: map[string]string{}}
	d.engine = d.newEngine()
	d.runner = jobs.New(jobs.Config{
		RC: d.rc, Store: store, Resolve: rcl.Backend, BookPath: paths.HistoryFile(), Backup: d.engine,
		Workdir: paths.BisyncDir(), Logf: logf, OnChange: d.hub.notify, Locked: rcl.ConfigLocked,
		OnFailure: func(j settings.Job, msg string) {
			d.failures.Add(1)
			desktop.Notify("Sync failed: "+j.Name, msg)
		},
	})
	d.sched = &jobs.Scheduler{Runner: d.runner, Store: store, Logf: logf, StartDelay: 45 * time.Second, VolumeCooldown: 10 * time.Minute}
	d.vols = volumes.NewWatcher(nil, 3*time.Second)
	d.applyPrefs()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	d.host = ln.Addr().String()
	srv := &http.Server{Handler: d.routes(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logf("api server: %v", err)
		}
	}()
	if err := settings.WriteJSONAtomic(paths.APIFile(), Handshake{URL: "http://" + d.host, Token: d.token, PID: os.Getpid(), Version: version.Version}); err != nil {
		return fmt.Errorf("writing %s: %w", paths.APIFile(), err)
	}
	defer func() { _ = os.Remove(paths.APIFile()) }()

	d.mounts.Start(ctx)
	go d.sched.Run(ctx, 15*time.Second)
	go d.vols.Run(ctx, func(v volumes.Volume) {
		d.hub.notify()
		d.sched.VolumeArrived(v)
	}, func(volumes.Volume) { d.hub.notify() })

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-sig:
			logf("signal received, shutting down")
			d.Quit()
		case <-d.quitCh:
		}
	}()

	d.firstRun()
	if o.OpenUI {
		d.OpenUI()
	}

	if o.Tray && desktop.HasDesktop() {
		changes, unsub := d.hub.subscribe()
		// Blocks on this (main) thread until Quit.
		tray.Run(d.trayModel, changes, tray.Actions{
			Open:     d.OpenUI,
			OpenPath: func(p string) { _ = desktop.OpenFolder(p) },
			Mount:    func(id string, want bool) { _ = d.mounts.SetWant(id, want) },
			RunJob:   func(id string) { _ = d.runner.Start(id, "manual", "") },
			StopJob:  func(id string) { d.runner.Stop(id) },
			Seen:     func() { d.failures.Store(0); d.hub.notify() },
			Quit:     d.Quit,
		}, d.quitCh)
		unsub()
	}
	<-d.quitCh

	logf("stopping syncs and unmounting")
	cancel()
	d.runner.StopAll(10 * time.Second)
	d.mounts.Shutdown(20 * time.Second)
	sctx, scancel := context.WithTimeout(context.Background(), 2*time.Second)
	_ = srv.Shutdown(sctx)
	scancel()
	logf("stopped")
	return nil
}

// Quit asks the daemon to stop.
func (d *Daemon) Quit() {
	d.quitOnce.Do(func() {
		close(d.quitCh)
		tray.Quit()
	})
}

// firstRun turns on start-at-login the first time the app runs in a
// desktop session: that is what people expect from a drive-mapping tool.
// Turning it off in Settings is remembered (the marker stays).
func (d *Daemon) firstRun() {
	marker := filepath.Join(paths.StateDir(), "first-run-done")
	if _, err := os.Stat(marker); err == nil {
		return
	}
	if desktop.HasDesktop() && paths.Home() == "" {
		if err := desktop.SetAutostart(true, false); err != nil {
			logf("could not enable start at login: %v", err)
		}
	}
	_ = os.WriteFile(marker, []byte(time.Now().Format(time.RFC3339)+"\n"), 0o600)
}

// applyPrefs pushes preferences into the running rclone.
func (d *Daemon) applyPrefs() {
	p := d.store.Get().Prefs
	rate := p.BWLimit
	if rate == "" {
		rate = "off"
	}
	if _, err := d.rc.Call(context.Background(), "core/bwlimit", rcl.Params{"rate": rate}); err != nil {
		logf("bandwidth limit %q: %v", rate, err)
	}
	if p.LogLevel != "" {
		var lvl fs.LogLevel
		if lvl.Set(p.LogLevel) == nil {
			fs.GetConfig(context.Background()).LogLevel = lvl
		}
	}
}

// afterSettings runs after any settings change.
func (d *Daemon) afterSettings() {
	doc := d.store.Get()
	keep := map[string]bool{}
	for _, j := range doc.Jobs {
		keep[j.ID] = true
	}
	d.runner.Forget(keep)
	d.applyPrefs()
	d.mounts.Kick()
	d.hub.notify()
}

// OpenUI opens the settings window with a fresh one-time login code.
func (d *Daemon) OpenUI() {
	url := fmt.Sprintf("http://%s/login?code=%s", d.host, d.newCode())
	if err := desktop.OpenURL(url); err != nil {
		logf("open UI: %v", err)
	}
}

func (d *Daemon) newCode() string {
	code := randHex(24)
	d.codesMu.Lock()
	defer d.codesMu.Unlock()
	now := time.Now()
	for c, exp := range d.codes {
		if now.After(exp) {
			delete(d.codes, c)
		}
	}
	d.codes[code] = now.Add(2 * time.Minute)
	return code
}

func (d *Daemon) useCode(code string) bool {
	d.codesMu.Lock()
	defer d.codesMu.Unlock()
	exp, ok := d.codes[code]
	delete(d.codes, code)
	return ok && time.Now().Before(exp)
}

// trayModel summarises state for the tray menu.
func (d *Daemon) trayModel() tray.Model {
	doc := d.store.Get()
	m := tray.Model{Locked: rcl.ConfigLocked()}
	states := map[string]mounts.State{}
	for _, s := range d.mounts.Snapshot() {
		states[s.ID] = s
	}
	up, trouble := 0, 0
	for _, mt := range doc.Mounts {
		s := states[mt.ID]
		item := tray.MountItem{ID: mt.ID, Name: mt.Name, Want: s.Want, Path: s.MountPoint}
		switch s.Status {
		case mounts.StatusMounted:
			item.Mounted = true
			up++
			if s.Error != "" {
				trouble++
				item.Problem = true
			}
		case mounts.StatusRetrying, mounts.StatusLocked:
			trouble++
			item.Problem = true
		}
		m.Mounts = append(m.Mounts, item)
	}
	running := 0
	for i, s := range d.runner.Snapshot() {
		j := doc.Jobs[i]
		item := tray.JobItem{ID: j.ID, Name: j.Name, Running: s.Running != nil, NeedsDrive: strings.Contains(j.Source+j.Dest, "{volume}")}
		if s.Running != nil {
			running++
		}
		if s.Last != nil && !s.Last.OK {
			item.Failed = true
		}
		m.Jobs = append(m.Jobs, item)
	}
	for _, a := range d.backupHealth(doc) {
		if a != nil && a.Critical {
			trouble++
		}
	}
	failed := d.failures.Load()
	m.Alert = trouble > 0 || failed > 0 || m.Locked || (runtime.GOOS == "windows" && !winfsp.Installed())
	switch {
	case m.Locked:
		m.Status = "rclone.conf is locked — open to unlock"
	case runtime.GOOS == "windows" && !winfsp.Installed():
		m.Status = "WinFsp needed — open to install"
	case trouble > 0:
		m.Status = fmt.Sprintf("%d drive(s) need attention", trouble)
	case failed > 0:
		m.Status = fmt.Sprintf("%d sync(s) failed", failed)
	case running > 0:
		m.Status = fmt.Sprintf("Syncing (%d running)", running)
	case up > 0:
		m.Status = fmt.Sprintf("%d drive(s) connected", up)
	default:
		m.Status = "Ready"
	}
	m.FailedSeen = failed > 0
	return m
}
