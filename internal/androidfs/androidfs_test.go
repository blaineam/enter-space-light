package androidfs_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blaineam/enter-space-light/internal/androidfs"
	"github.com/blaineam/enter-space-light/internal/rcl"
	"github.com/blaineam/enter-space-light/internal/settings"
	"github.com/blaineam/enter-space-light/internal/tune"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "esl-androidfs-*")
	if err != nil {
		panic(err)
	}
	androidfs.Register()
	if err := rcl.Init(rcl.Options{ConfigPath: filepath.Join(dir, "rclone.conf"), CacheDir: filepath.Join(dir, "cache"), LogLevel: "ERROR"}); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// A drive is connected through the same rc call and automatic tuning the
// app uses, then driven the way the DocumentsProvider drives it.
func TestDriveThroughRcloneMount(t *testing.T) {
	remote := t.TempDir()
	if err := os.WriteFile(filepath.Join(remote, "hello.txt"), []byte("hello world"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := settings.Mount{ID: "abc123", Name: "My Drive", Remote: remote}
	in := rcl.Params{"fs": remote, "mountPoint": androidfs.Prefix + m.ID}
	for k, v := range tune.MountParams(m, "local", tune.Env{GOOS: "android", CacheBudget: "1G"}) {
		in[k] = v
	}
	in["vfs_cache_min_free_space"] = "off"
	in["vfs_write_back"] = "100ms"
	call := rcl.Local{}
	if _, err := call.Call(context.Background(), "mount/mount", in); err != nil {
		t.Fatalf("mount: %v", err)
	}
	unmounted := false
	unmount := func() {
		if unmounted {
			return
		}
		unmounted = true
		if _, err := call.Call(context.Background(), "mount/unmount", rcl.Params{"mountPoint": androidfs.Prefix + m.ID}); err != nil {
			t.Errorf("unmount: %v", err)
		}
	}
	defer unmount()

	out, err := call.Call(context.Background(), "mount/listmounts", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("listmounts: %v", out)
	if err := androidfs.Healthy(m.ID); err != nil {
		t.Fatal(err)
	}

	read := func(p string, off int64, n int) string {
		t.Helper()
		h, err := androidfs.Open(m.ID, p, "r")
		if err != nil {
			t.Fatalf("open %s: %v", p, err)
		}
		defer func() { _ = androidfs.Close(h) }()
		buf := make([]byte, n)
		got, err := androidfs.ReadAt(h, buf, off)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		return string(buf[:got])
	}
	if got := read("hello.txt", 6, 100); got != "world" {
		t.Fatalf("seeked read = %q", got)
	}

	// Waits for the background upload to reach the "remote".
	onRemote := func(p, want string) {
		t.Helper()
		var b []byte
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			if b, err = os.ReadFile(filepath.Join(remote, p)); err == nil && string(b) == want {
				return
			}
		}
		t.Fatalf("%s on the remote = %q (%v), want %q", p, b, err, want)
	}

	folder, err := androidfs.Create(m.ID, "", "New folder", true)
	if err != nil {
		t.Fatal(err)
	}
	file, err := androidfs.Create(m.ID, folder, "note.txt", false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := androidfs.Create(m.ID, folder, "note.txt", false)
	if err != nil {
		t.Fatal(err)
	}
	if second != "New folder/note (2).txt" {
		t.Fatalf("a taken name must be numbered, got %q", second)
	}

	write := func(p, mode string, off int64, data string) {
		t.Helper()
		h, err := androidfs.Open(m.ID, p, mode)
		if err != nil {
			t.Fatalf("open %s %s: %v", p, mode, err)
		}
		if _, err := androidfs.WriteAt(h, []byte(data), off); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
		if err := androidfs.Close(h); err != nil {
			t.Fatalf("close %s: %v", p, err)
		}
	}
	write(file, "w", 0, "a long first version")
	onRemote(file, "a long first version")
	write(file, "rw", 2, "SHORT")
	onRemote(file, "a SHORTfirst version")
	write(file, "w", 0, "tiny")
	onRemote(file, "tiny")

	if _, err := androidfs.Rename(m.ID, second, "note.txt"); err == nil {
		t.Fatal("rename must not replace another file")
	}
	renamed, err := androidfs.Rename(m.ID, file, "renamed.txt")
	if err != nil {
		t.Fatal(err)
	}
	moved, err := androidfs.Move(m.ID, renamed, "")
	if err != nil {
		t.Fatal(err)
	}
	if moved != "renamed.txt" {
		t.Fatalf("moved to %q", moved)
	}
	onRemote(moved, "tiny")
	if _, err := androidfs.Move(m.ID, folder, folder); err == nil {
		t.Fatal("a folder moved into itself")
	}
	for _, bad := range []string{"", "..", "a/b"} {
		if _, err := androidfs.Create(m.ID, "", bad, false); err == nil {
			t.Fatalf("created %q", bad)
		}
	}
	if err := androidfs.Delete(m.ID, ""); err == nil {
		t.Fatal("deleted the whole drive")
	}
	if got := androidfs.Clean("../../etc/passwd"); got != "etc/passwd" {
		t.Fatalf("Clean escaped the root: %q", got)
	}
	if err := androidfs.Delete(m.ID, folder); err != nil {
		t.Fatal(err)
	}
	list, err := androidfs.List(m.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("root = %+v", list)
	}

	// Disconnecting closes files other apps still hold.
	if _, err := androidfs.Open(m.ID, "hello.txt", "r"); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); androidfs.PendingUploads() > 0 && time.Now().Before(deadline); {
		time.Sleep(50 * time.Millisecond)
	}
	unmount()
	if n := androidfs.OpenCount(); n != 0 {
		t.Fatalf("%d handles left open", n)
	}
	if _, err := androidfs.List(m.ID, ""); err != androidfs.ErrNotConnected {
		t.Fatalf("after unmount: %v", err)
	}
}
