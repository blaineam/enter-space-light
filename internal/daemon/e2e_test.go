//go:build e2e

// End-to-end: runs the real daemon (embedded rclone, real HTTP API) in a
// sandbox and drives it like the UI and CLI do. With ESL_E2E_MOUNT=1 it
// also mounts for real (CI installs fuse3 on Linux and WinFsp on Windows)
// and does file I/O through the mount.
//
//	go test -tags e2e[,cmount] -run TestE2E -v ./internal/daemon
package daemon_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/blaineam/enter-space-light/internal/backup"
	"github.com/blaineam/enter-space-light/internal/client"
	"github.com/blaineam/enter-space-light/internal/daemon"
	"github.com/blaineam/enter-space-light/internal/paths"

	_ "github.com/blaineam/enter-space-light/internal/rclonecli" // registers the mount implementations
)

type status struct {
	Mounts []struct {
		ID    string `json:"id"`
		State struct {
			Status     string `json:"status"`
			MountPoint string `json:"mountPoint"`
			Error      string `json:"error"`
		} `json:"state"`
	} `json:"mounts"`
	Jobs []struct {
		ID    string `json:"id"`
		State struct {
			Running *json.RawMessage `json:"running"`
			Last    *struct {
				OK    bool   `json:"ok"`
				Error string `json:"error"`
				Files int64  `json:"files"`
			} `json:"last"`
		} `json:"state"`
	} `json:"jobs"`
	Config struct {
		Remotes []struct {
			Name string `json:"name"`
		} `json:"remotes"`
	} `json:"config"`
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, p, s string) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Dir(p), 0o755))
	must(t, os.WriteFile(p, []byte(s), 0o644))
}

func eventually(t *testing.T, d time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", d, what)
}

func TestE2E(t *testing.T) {
	// Not t.TempDir(): the embedded rclone keeps its log file open for the
	// life of the process, and Windows refuses to delete open files, which
	// would fail a passing test at cleanup.
	home, err := os.MkdirTemp("", "esl-e2e-")
	must(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("ESL_HOME", home)
	decoy := filepath.Join(home, "decoy", "rclone.conf") // stands in for the user's real config
	t.Setenv("RCLONE_CONFIG", decoy)
	defer func() {
		if _, err := os.Stat(decoy); !os.IsNotExist(err) {
			t.Error("the sandbox touched RCLONE_CONFIG's file")
		}
	}()
	remoteRoot := filepath.Join(home, "remote-root")
	must(t, os.MkdirAll(remoteRoot, 0o755))
	write(t, filepath.Join(home, "rclone.conf"), fmt.Sprintf("[cloud]\ntype = alias\nremote = %s\n", remoteRoot))

	done := make(chan error, 1)
	go func() { done <- daemon.Run(daemon.Options{Tray: false}) }()

	var c *client.Client
	eventually(t, 30*time.Second, "the daemon API", func() bool {
		var err error
		c, err = client.Connect()
		return err == nil
	})
	if err := daemon.Run(daemon.Options{}); err != daemon.ErrAlreadyRunning {
		t.Fatalf("second daemon should be refused by the lock, got %v", err)
	}
	get := func() status {
		var s status
		must(t, c.Do(http.MethodGet, "/api/status", nil, &s))
		return s
	}
	if s := get(); len(s.Config.Remotes) != 1 || s.Config.Remotes[0].Name != "cloud" {
		t.Fatalf("rclone.conf not loaded: %+v", s.Config)
	}

	t.Run("security", func(t *testing.T) {
		u := c.URL
		resp, err := http.Get(u + "/api/status")
		must(t, err)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("unauthenticated status: %d", resp.StatusCode)
		}
		req, _ := http.NewRequest(http.MethodGet, u+"/api/status", nil)
		req.Host = "evil.example:80"
		req.Header.Set("Authorization", "Bearer "+c.Token)
		resp, err = http.DefaultClient.Do(req)
		must(t, err)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("DNS-rebinding host accepted: %d", resp.StatusCode)
		}
		req, _ = http.NewRequest(http.MethodPost, u+"/api/quit", nil)
		req.Header.Set("Authorization", "Bearer "+c.Token)
		resp, err = http.DefaultClient.Do(req)
		must(t, err)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("POST without X-ESL accepted: %d", resp.StatusCode)
		}
		// UI login: one-time code → cookie → API works; the code can't be reused.
		login, err := c.LoginURL()
		must(t, err)
		jar, _ := cookiejar.New(nil)
		browser := &http.Client{Jar: jar}
		resp, err = browser.Get(login)
		must(t, err)
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "Enter Space Light") {
			t.Fatalf("login did not land on the UI: %d", resp.StatusCode)
		}
		if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") {
			t.Fatalf("missing CSP: %q", csp)
		}
		resp, err = browser.Get(u + "/api/status")
		must(t, err)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("cookie session rejected: %d", resp.StatusCode)
		}
		req, _ = http.NewRequest(http.MethodPost, u+"/api/jobs", strings.NewReader(`{}`))
		req.Header.Set("X-ESL", "1")
		req.Header.Set("Origin", "http://evil.example")
		uu, _ := url.Parse(u)
		for _, ck := range jar.Cookies(uu) {
			req.AddCookie(ck)
		}
		resp, err = http.DefaultClient.Do(req)
		must(t, err)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("cross-origin POST accepted: %d", resp.StatusCode)
		}
		resp, err = (&http.Client{}).Get(login)
		must(t, err)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("login code reused: %d", resp.StatusCode)
		}
	})

	t.Run("sync jobs", func(t *testing.T) {
		src := filepath.Join(home, "src")
		write(t, filepath.Join(src, "a.txt"), "alpha")
		write(t, filepath.Join(src, "sub", "b.txt"), "beta")
		write(t, filepath.Join(src, "skip.tmp"), "junk")

		var job struct{ ID string }
		must(t, c.Do(http.MethodPost, "/api/jobs", map[string]any{
			"name": "Backup", "kind": "copy", "enabled": true, "source": src, "dest": "cloud:backup",
			"filters": []string{"- *.tmp"}, "triggers": map[string]any{"every": "24h"},
		}, &job))
		must(t, c.Do(http.MethodPost, "/api/jobs/"+job.ID+"/run", nil, nil))
		eventually(t, 60*time.Second, "the copy job", func() bool {
			for _, j := range get().Jobs {
				if j.ID == job.ID && j.State.Running == nil && j.State.Last != nil {
					return true
				}
			}
			return false
		})
		for _, j := range get().Jobs {
			if j.ID == job.ID && (!j.State.Last.OK || j.State.Last.Files != 2) {
				t.Fatalf("copy result: %+v", j.State.Last)
			}
		}
		if b, err := os.ReadFile(filepath.Join(remoteRoot, "backup", "sub", "b.txt")); err != nil || string(b) != "beta" {
			t.Fatalf("copied file missing: %v %q", err, b)
		}
		if _, err := os.Stat(filepath.Join(remoteRoot, "backup", "skip.tmp")); !os.IsNotExist(err) {
			t.Fatal("filter rule ignored: skip.tmp was copied")
		}

		// Two-way: first run resyncs, then a change on each side propagates.
		left, right := filepath.Join(home, "left"), filepath.Join(remoteRoot, "right")
		write(t, filepath.Join(left, "l.txt"), "L")
		write(t, filepath.Join(right, "r.txt"), "R")
		var bj struct{ ID string }
		must(t, c.Do(http.MethodPost, "/api/jobs", map[string]any{
			"name": "Two-way", "kind": "bisync", "enabled": true, "source": left, "dest": "cloud:right", "triggers": map[string]any{},
		}, &bj))
		runOK := func() {
			must(t, c.Do(http.MethodPost, "/api/jobs/"+bj.ID+"/run", nil, nil))
			eventually(t, 90*time.Second, "bisync", func() bool {
				for _, j := range get().Jobs {
					if j.ID == bj.ID && j.State.Running == nil && j.State.Last != nil {
						return true
					}
				}
				return false
			})
			for _, j := range get().Jobs {
				if j.ID == bj.ID && !j.State.Last.OK {
					t.Fatalf("bisync failed: %s", j.State.Last.Error)
				}
			}
		}
		runOK()
		if _, err := os.Stat(filepath.Join(left, "r.txt")); err != nil {
			t.Fatal("bisync resync didn't bring r.txt left")
		}
		write(t, filepath.Join(right, "r2.txt"), "R2")
		time.Sleep(1100 * time.Millisecond) // distinct modtimes
		runOK()
		if _, err := os.Stat(filepath.Join(left, "r2.txt")); err != nil {
			t.Fatal("second bisync didn't propagate r2.txt")
		}
	})

	t.Run("backup and time portal", func(t *testing.T) {
		src := filepath.Join(home, "pc")
		write(t, filepath.Join(src, "a.txt"), "version one")
		write(t, filepath.Join(src, "docs", "b.txt"), "beta original")
		write(t, filepath.Join(src, ".cache", "junk.bin"), "cache")
		write(t, filepath.Join(src, "AppData", "Local", "Temp", "junk.tmp"), "temp")

		var job struct {
			ID     string
			Backup struct{ ID, Namespace string }
		}
		must(t, c.Do(http.MethodPost, "/api/jobs", map[string]any{
			"name": "PC backup", "kind": "sync", "enabled": true, "source": src, "dest": "cloud:Backup/PC",
			"triggers": map[string]any{"daily": "02:30"},
			"backup":   map[string]any{"template": "home", "retentionDays": 30, "excludeGroups": []string{"temp", "system"}},
		}, &job))
		if !backup.ValidUUID(job.Backup.ID) || job.Backup.Namespace != "Backup-PC" {
			t.Fatalf("backup identity not assigned: %+v", job.Backup)
		}
		runJob := func(wantOK bool) string {
			t.Helper()
			before := 0
			for _, j := range get().Jobs {
				if j.ID == job.ID && j.State.Last != nil {
					before = 1
				}
			}
			_ = before
			must(t, c.Do(http.MethodPost, "/api/jobs/"+job.ID+"/run", nil, nil))
			var last string
			eventually(t, 90*time.Second, "backup run", func() bool {
				for _, j := range get().Jobs {
					if j.ID == job.ID && j.State.Running == nil && j.State.Last != nil {
						if j.State.Last.OK != wantOK {
							t.Fatalf("run ok=%v want %v: %s", j.State.Last.OK, wantOK, j.State.Last.Error)
						}
						last = j.State.Last.Error
						return true
					}
				}
				return false
			})
			return last
		}
		runJob(true)
		dest := filepath.Join(remoteRoot, "Backup", "PC")
		if b, _ := os.ReadFile(filepath.Join(dest, "a.txt")); string(b) != "version one" {
			t.Fatal("first backup missing a.txt")
		}
		junkDirs := []string{".cache"} // Linux (and the macOS dev build) skip ~/.cache
		if runtime.GOOS == "windows" {
			junkDirs = []string{filepath.Join("AppData", "Local", "Temp")}
		}
		for _, junk := range junkDirs {
			if _, err := os.Stat(filepath.Join(dest, junk)); !os.IsNotExist(err) {
				t.Fatalf("exclude group ignored: %s was backed up", junk)
			}
		}
		manifests, _ := filepath.Glob(filepath.Join(dest, ".enterspace", "manifests", job.Backup.ID, "*.json.deflate"))
		if len(manifests) != 1 {
			t.Fatalf("want 1 run record on the destination, got %v", manifests)
		}
		raw, _ := os.ReadFile(manifests[0])
		m, err := backup.Decode(raw)
		must(t, err)
		onDisk := 0
		_ = filepath.WalkDir(dest, func(p string, de os.DirEntry, err error) error {
			if err == nil && !de.IsDir() && !strings.Contains(p, ".enterspace") {
				onDisk++
			}
			return nil
		})
		if m.Outcome != backup.Completed || m.TreeCoverage != backup.CoverageFull || len(m.LiveEntries) != onDisk || onDisk < 2 || m.SyncID != job.Backup.ID {
			t.Fatalf("run record: outcome=%s coverage=%s live=%d", m.Outcome, m.TreeCoverage, len(m.LiveEntries))
		}

		time.Sleep(1100 * time.Millisecond)
		write(t, filepath.Join(src, "a.txt"), "version two")
		must(t, os.RemoveAll(filepath.Join(src, "docs")))
		time.Sleep(1100 * time.Millisecond)
		runJob(true)
		vers, _ := filepath.Glob(filepath.Join(remoteRoot, ".previous-versions", "Backup-PC", "*", "a.txt"))
		if len(vers) != 1 {
			t.Fatalf("replaced a.txt should be kept as a version: %v", vers)
		}
		if b, _ := os.ReadFile(vers[0]); string(b) != "version one" {
			t.Fatalf("version content %q", b)
		}

		var tl struct {
			Timeline []time.Time `json:"timeline"`
		}
		must(t, c.Do(http.MethodGet, "/api/portal/timeline?refresh=1&job="+job.ID, nil, &tl))
		if len(tl.Timeline) != 2 {
			t.Fatalf("timeline %v", tl.Timeline)
		}
		first := tl.Timeline[1].Unix()
		var l backup.Listing
		must(t, c.Do(http.MethodGet, fmt.Sprintf("/api/portal/list?job=%s&at=%d", job.ID, first), nil, &l))
		states := map[string]backup.State{}
		for _, it := range l.Items {
			states[it.Name] = it.State
		}
		if states["a.txt"] != backup.ModifiedSince || states["docs"] != backup.DeletedSince {
			t.Fatalf("point-in-time view at the first backup: %+v (note %q)", states, l.Note)
		}

		restoreTo := filepath.Join(home, "restored")
		restore := func(body map[string]any) {
			t.Helper()
			must(t, c.Do(http.MethodPost, "/api/portal/restore?job="+job.ID, body, nil))
			eventually(t, 60*time.Second, "restore", func() bool {
				var st struct {
					Restore *backup.RestoreReport `json:"restore"`
				}
				must(t, c.Do(http.MethodGet, "/api/portal/restore", nil, &st))
				if st.Restore != nil && st.Restore.Finished && len(st.Restore.Failed) > 0 {
					t.Fatalf("restore failures: %v", st.Restore.Failed)
				}
				return st.Restore != nil && st.Restore.Finished
			})
		}
		restore(map[string]any{"paths": []string{"docs"}, "at": first, "target": "folder", "folder": restoreTo})
		if b, _ := os.ReadFile(filepath.Join(restoreTo, "docs", "b.txt")); string(b) != "beta original" {
			t.Fatalf("restored deleted file: %q", b)
		}
		restore(map[string]any{"paths": []string{"a.txt"}, "at": first, "target": "original", "keepBoth": true})
		if b, _ := os.ReadFile(filepath.Join(src, "a.txt")); string(b) != "version two" {
			t.Fatal("keep-both restore overwrote the current file")
		}
		kept, _ := filepath.Glob(filepath.Join(src, "a (restored *).txt"))
		if len(kept) != 1 {
			t.Fatalf("keep-both copy missing: %v", kept)
		}
		if b, _ := os.ReadFile(kept[0]); string(b) != "version one" {
			t.Fatalf("keep-both content %q", b)
		}

		// The delete guard: an emptied source (an unplugged drive, a moved
		// folder) must never wipe the backup.
		entries, _ := os.ReadDir(src)
		for _, e := range entries {
			must(t, os.RemoveAll(filepath.Join(src, e.Name())))
		}
		msg := runJob(false)
		if !strings.Contains(msg, "source is empty") {
			t.Fatalf("guard message: %q", msg)
		}
		if _, err := os.Stat(filepath.Join(dest, "a.txt")); err != nil {
			t.Fatal("the guard let the backup be deleted")
		}
		var health struct {
			Jobs []struct {
				ID     string
				Health *backup.Alert `json:"health"`
			} `json:"jobs"`
		}
		must(t, c.Do(http.MethodGet, "/api/status", nil, &health))
		for _, j := range health.Jobs {
			if j.ID == job.ID && (j.Health == nil || j.Health.Kind != "guardAborted") {
				t.Fatalf("health should report the guard stop: %+v", j.Health)
			}
		}
	})

	t.Run("bad input is rejected", func(t *testing.T) {
		err := c.Do(http.MethodPost, "/api/jobs", map[string]any{"name": "x", "kind": "copy", "source": "a:", "dest": "b:", "triggers": map[string]any{"every": "5s"}}, nil)
		if err == nil || !strings.Contains(err.Error(), "at least 1m") {
			t.Fatalf("short interval accepted: %v", err)
		}
		err = c.Do(http.MethodPut, "/api/prefs", map[string]any{"bwLimit": "fast please"}, nil)
		if err == nil {
			t.Fatal("bad bandwidth limit accepted")
		}
	})

	if os.Getenv("ESL_E2E_MOUNT") == "1" {
		t.Run("mount", func(t *testing.T) {
			mp := ""
			if runtime.GOOS != "windows" {
				mp = filepath.Join(home, "mnt", "Cloud")
			}
			var m struct{ ID string }
			must(t, c.Do(http.MethodPost, "/api/mounts", map[string]any{"name": "Cloud", "remote": "cloud:", "autoMount": true, "mountPoint": mp}, &m))
			var where string
			eventually(t, 60*time.Second, "the mount", func() bool {
				for _, x := range get().Mounts {
					if x.ID == m.ID {
						if x.State.Error != "" {
							t.Logf("mount state: %s %s", x.State.Status, x.State.Error)
						}
						where = x.State.MountPoint
						return x.State.Status == "mounted"
					}
				}
				return false
			})
			root := where
			if runtime.GOOS == "windows" && len(root) == 2 {
				root += `\`
			}
			// Read through the mount what the sync job wrote.
			b, err := os.ReadFile(filepath.Join(root, "backup", "a.txt"))
			if err != nil || string(b) != "alpha" {
				t.Fatalf("read through mount: %v %q", err, b)
			}
			// Write through the mount; write-back lands it in the remote.
			payload := bytes.Repeat([]byte("enter space light "), 200000) // ~3.6 MB
			must(t, os.WriteFile(filepath.Join(root, "written.bin"), payload, 0o644))
			must(t, os.Rename(filepath.Join(root, "written.bin"), filepath.Join(root, "renamed.bin")))
			eventually(t, 60*time.Second, "write-back upload", func() bool {
				got, err := os.ReadFile(filepath.Join(remoteRoot, "renamed.bin"))
				return err == nil && bytes.Equal(got, payload)
			})
			// Random-access read (seek) through the mount.
			f, err := os.Open(filepath.Join(root, "renamed.bin"))
			must(t, err)
			buf := make([]byte, 5)
			_, err = f.ReadAt(buf, 18*1000+6)
			_ = f.Close()
			must(t, err)
			if string(buf) != "space" {
				t.Fatalf("seek read got %q", buf)
			}
			must(t, c.Do(http.MethodPost, "/api/mounts/"+m.ID+"/disconnect", nil, nil))
			eventually(t, 30*time.Second, "unmount", func() bool {
				for _, x := range get().Mounts {
					if x.ID == m.ID {
						return x.State.Status == "unmounted"
					}
				}
				return false
			})
		})
	}

	must(t, c.Do(http.MethodPost, "/api/quit", nil, nil))
	select {
	case err := <-done:
		must(t, err)
	case <-time.After(60 * time.Second):
		t.Fatal("daemon did not exit")
	}
	if _, err := os.Stat(paths.APIFile()); !os.IsNotExist(err) {
		t.Fatal("api.json left behind after quit")
	}
}
