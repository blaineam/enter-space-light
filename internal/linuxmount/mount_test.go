//go:build e2e && linux && !android

package linuxmount_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	_ "github.com/blaineam/enter-space-light/internal/linuxmount"

	_ "github.com/rclone/rclone/backend/local"
	"github.com/rclone/rclone/cmd/mountlib"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/vfs"
	"github.com/rclone/rclone/vfs/vfscommon"
)

const childEnv = "ESL_SELF_USE_CHILD"

// A sync or backup may read or write a drive this same process serves. Go
// registers each file it opens with epoll, which can send the FUSE server a
// POLL request while holding a Go processor. With one processor the server
// then never runs unless the kernel has stopped asking, so this freezes
// every time without the fix (with more processors, only now and then). It
// runs in a child process because a frozen Go runtime can't time itself out.
func TestUsingOwnDriveDoesNotFreeze(t *testing.T) {
	if scenario := os.Getenv(childEnv); scenario != "" {
		useOwnDrive(t, scenario)
		return
	}
	for _, scenario := range []string{"read", "write-into-empty-drive", "read-only"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			dir := t.TempDir()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestUsingOwnDriveDoesNotFreeze$", "-test.v")
			cmd.Env = append(os.Environ(), childEnv+"="+scenario, "ESL_SELF_USE_DIR="+dir)
			// SIGKILL can't end a frozen child: the kernel waits for a reply
			// that only the child could send. Aborting the connection can.
			point := filepath.Join(dir, "drive")
			cmd.Cancel = func() error {
				abortFUSE(t, point)
				return cmd.Process.Kill()
			}
			cmd.WaitDelay = 5 * time.Second
			out, err := cmd.CombinedOutput()
			// A killed or failed child can leave its mount behind.
			_ = exec.Command("fusermount3", "-uz", point).Run()
			if ctx.Err() != nil {
				t.Fatalf("froze while using its own drive:\n%s", out)
			}
			if err != nil {
				t.Fatalf("%v:\n%s", err, out)
			}
		})
	}
}

// abortFUSE fails every request on the drive mounted at point. It reads the
// connection number from mountinfo, because asking the drive itself would
// hang too.
func abortFUSE(t *testing.T, point string) {
	info, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		t.Logf("reading mountinfo: %v", err)
		return
	}
	for line := range strings.Lines(string(info)) {
		fields := strings.Fields(line)
		if len(fields) > 4 && fields[4] == point {
			_, minor, _ := strings.Cut(fields[2], ":")
			if err := os.WriteFile("/sys/fs/fuse/connections/"+minor+"/abort", []byte("1"), 0o200); err != nil {
				t.Logf("aborting the drive: %v", err)
			}
		}
	}
}

func useOwnDrive(t *testing.T, scenario string) {
	dir := os.Getenv("ESL_SELF_USE_DIR")
	remote, point := filepath.Join(dir, "remote"), filepath.Join(dir, "drive")
	for _, d := range []string{remote, point} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if scenario != "write-into-empty-drive" {
		if err := os.WriteFile(filepath.Join(remote, "a.txt"), []byte("hello"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f, err := fs.NewFs(context.Background(), remote)
	if err != nil {
		t.Fatal(err)
	}
	vfsOpt := vfscommon.Opt
	vfsOpt.CacheMode = vfscommon.CacheModeWrites
	vfsOpt.ReadOnly = scenario == "read-only"
	mountOpt := mountlib.Opt
	mountOpt.DeviceName = "esl-test"
	_, mountFn := mountlib.ResolveMountMethod("mount")
	v := vfs.New(context.Background(), f, &vfsOpt)
	_, unmount, _, err := mountFn(v, point, &mountOpt)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unmount() }()

	runtime.GOMAXPROCS(1)
	for range 20 {
		if scenario == "write-into-empty-drive" {
			if err := os.WriteFile(filepath.Join(point, "new.txt"), []byte("hello"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		name := "a.txt"
		if scenario == "write-into-empty-drive" {
			name = "new.txt"
		}
		got, err := os.ReadFile(filepath.Join(point, name))
		if err != nil || string(got) != "hello" {
			t.Fatalf("read %q, %v", got, err)
		}
		runtime.GC()
	}
	// Nothing the fix does may show up on the drive or reach the remote. (The
	// remote may still be empty: new files upload a few seconds later.)
	want := "a.txt"
	if scenario == "write-into-empty-drive" {
		want = "new.txt"
	}
	for _, d := range []string{point, remote} {
		entries, err := os.ReadDir(d)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.Name() != want {
				t.Fatalf("unexpected %q in %s", e.Name(), d)
			}
		}
		if d == point && len(entries) != 1 {
			t.Fatalf("the drive lists %v, want %s", entries, want)
		}
	}
}
