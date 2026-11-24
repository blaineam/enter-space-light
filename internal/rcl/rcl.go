// Package rcl runs rclone in-process. Every operation goes through rclone's
// own rc functions (the same ones `rclone rcd` serves), called directly with
// no HTTP hop and no subprocess.
package rcl

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/accounting"
	"github.com/rclone/rclone/fs/config"
	"github.com/rclone/rclone/fs/config/configfile"
	"github.com/rclone/rclone/fs/log"
	"github.com/rclone/rclone/fs/rc"
	"github.com/rclone/rclone/fs/rc/jobs"

	// Registers every backend and the rc calls we use.
	_ "github.com/rclone/rclone/backend/all"
	_ "github.com/rclone/rclone/cmd/bisync"
	_ "github.com/rclone/rclone/cmd/mountlib"
	_ "github.com/rclone/rclone/fs/operations"
	_ "github.com/rclone/rclone/fs/sync"
)

// Params is rclone's rc parameter map.
type Params = rc.Params

// Caller is the subset of rclone the daemon depends on; tests fake it.
type Caller interface {
	Call(ctx context.Context, method string, in Params) (Params, error)
}

// Options configure the embedded rclone.
type Options struct {
	ConfigPath string // "" = rclone's default
	CacheDir   string
	LogFile    string
	LogLevel   string // ERROR, NOTICE, INFO, DEBUG
}

var initOnce sync.Once

// Init starts the embedded rclone exactly once per process.
func Init(o Options) error {
	var err error
	initOnce.Do(func() { err = doInit(o) })
	return err
}

func doInit(o Options) error {
	ctx := context.Background()
	ci := fs.GetConfig(ctx)
	// The daemon has no terminal; an encrypted config is unlocked through
	// the UI (or the OS keychain), never by prompting on stdin.
	ci.AskPassword = false
	if o.LogLevel != "" {
		var lvl fs.LogLevel
		if err := lvl.Set(strings.ToUpper(o.LogLevel)); err != nil {
			return fmt.Errorf("log level %q: %w", o.LogLevel, err)
		}
		ci.LogLevel = lvl
	}
	if o.ConfigPath != "" {
		if err := config.SetConfigPath(o.ConfigPath); err != nil {
			return err
		}
	}
	if o.CacheDir != "" {
		if err := config.SetCacheDir(o.CacheDir); err != nil {
			return err
		}
	}
	if o.LogFile != "" {
		log.Opt.File = o.LogFile
		log.Opt.MaxSize = 10 * fs.Mebi
		log.Opt.MaxBackups = 5
	}
	log.InitLogging()
	configfile.Install()
	accounting.Start(ctx)
	return nil
}

// Local is the in-process Caller.
type Local struct{}

// Call runs an rc method synchronously (or starts it, if in has _async).
func (Local) Call(ctx context.Context, method string, in Params) (out Params, err error) {
	call := rc.Calls.Get(method)
	if call == nil {
		return nil, fmt.Errorf("rclone has no method %q", method)
	}
	if in == nil {
		in = Params{}
	}
	// rc functions consume (delete) keys from their input; never let that
	// mutate a caller's map.
	cp := make(Params, len(in))
	for k, v := range in {
		cp[k] = v
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("rclone %s panicked: %v", method, r)
		}
	}()
	_, out, err = jobs.NewJob(ctx, call.Fn, cp)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = Params{}
	}
	return out, nil
}

// JobStatus is rclone's job/status reply, trimmed.
type JobStatus struct {
	Finished bool
	Success  bool
	Error    string
	Output   Params
}

// StartAsync starts method in the background, returning rclone's job id.
func StartAsync(ctx context.Context, c Caller, method string, in Params, group string) (int64, error) {
	cp := make(Params, len(in)+2)
	for k, v := range in {
		cp[k] = v
	}
	cp["_async"] = true
	if group != "" {
		cp["_group"] = group
	}
	out, err := c.Call(ctx, method, cp)
	if err != nil {
		return 0, err
	}
	return out.GetInt64("jobid")
}

// Status reads job/status.
func Status(ctx context.Context, c Caller, id int64) (JobStatus, error) {
	out, err := c.Call(ctx, "job/status", Params{"jobid": id})
	if err != nil {
		return JobStatus{}, err
	}
	st := JobStatus{}
	st.Finished, _ = out.GetBool("finished")
	st.Success, _ = out.GetBool("success")
	st.Error, _ = out.GetString("error")
	if o, ok := out["output"].(Params); ok {
		st.Output = o
	} else if o, ok := out["output"].(map[string]any); ok {
		st.Output = o
	}
	return st, nil
}

// Wait polls a job until it finishes or ctx ends. onTick, if set, is called
// between polls (used to publish progress).
func Wait(ctx context.Context, c Caller, id int64, every time.Duration, onTick func()) (JobStatus, error) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		st, err := Status(context.Background(), c, id)
		if err != nil {
			return st, err
		}
		if st.Finished {
			return st, nil
		}
		if onTick != nil {
			onTick()
		}
		select {
		case <-ctx.Done():
			_, _ = c.Call(context.Background(), "job/stop", Params{"jobid": id})
			return JobStatus{Finished: true, Error: "stopped"}, ctx.Err()
		case <-t.C:
		}
	}
}

// Stats is a slice of rclone's core/stats.
type Stats struct {
	Bytes          int64   `json:"bytes"`
	TotalBytes     int64   `json:"totalBytes"`
	Transfers      int64   `json:"transfers"`
	TotalTransfers int64   `json:"totalTransfers"`
	Checks         int64   `json:"checks"`
	Errors         int64   `json:"errors"`
	Speed          float64 `json:"speed"`
	ETA            *int64  `json:"eta,omitempty"`
}

// GroupStats reads core/stats for one stats group.
func GroupStats(ctx context.Context, c Caller, group string) (Stats, error) {
	out, err := c.Call(ctx, "core/stats", Params{"group": group})
	if err != nil {
		return Stats{}, err
	}
	var s Stats
	err = rc.Reshape(&s, out)
	return s, err
}

// Backend resolves a remote's type and the remote it wraps, straight from
// the loaded rclone.conf.
func Backend(name string) (typ, wraps string, ok bool) {
	data := config.LoadedData()
	if !data.HasSection(name) {
		return "", "", false
	}
	typ, _ = data.GetValue(name, "type")
	switch typ {
	case "union", "combine":
		// Several upstreams; tune for the first one.
		up, _ := data.GetValue(name, "upstreams")
		f := strings.Fields(up)
		if len(f) > 0 {
			// combine: "dir=remote:path"; union: "remote:path[:ro|:nc|:writeback]"
			first := f[0]
			if i := strings.Index(first, "="); i >= 0 {
				first = first[i+1:]
			}
			for _, suffix := range []string{":ro", ":nc", ":writeback"} {
				first = strings.TrimSuffix(first, suffix)
			}
			wraps = first
		}
	default:
		wraps, _ = data.GetValue(name, "remote")
	}
	return typ, wraps, true
}

// ConfigPath is the rclone.conf in use.
func ConfigPath() string { return config.GetConfigPath() }

// ConfigEncrypted reports whether rclone.conf is encrypted, by its header.
func ConfigEncrypted() bool {
	f, err := os.Open(config.GetConfigPath())
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for i := 0; i < 8 && sc.Scan(); i++ {
		if strings.HasPrefix(strings.TrimSpace(sc.Text()), "RCLONE_ENCRYPT_V") {
			return true
		}
	}
	return false
}

var unlockMu sync.Mutex
var unlocked bool

// ConfigLocked reports whether an encrypted config still needs its password.
func ConfigLocked() bool {
	unlockMu.Lock()
	defer unlockMu.Unlock()
	return ConfigEncrypted() && !unlocked
}

// ErrWrongPassword is returned by Unlock for a bad password.
var ErrWrongPassword = errors.New("wrong configuration password")

// Unlock decrypts rclone.conf with password and reloads it.
func Unlock(password string) error {
	unlockMu.Lock()
	defer unlockMu.Unlock()
	if err := config.SetConfigPassword(password); err != nil {
		return err
	}
	if err := config.LoadedData().Load(); err != nil {
		config.ClearConfigPassword()
		unlocked = false
		return ErrWrongPassword
	}
	unlocked = true
	return nil
}

// Reload re-reads rclone.conf (after the user edited it by hand).
func Reload() error { return config.LoadedData().Load() }

// Version is the embedded rclone's version string.
func Version() string { return fs.Version }
