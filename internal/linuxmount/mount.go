//go:build linux && !android

package linuxmount

import (
	"errors"
	"fmt"
	"path/filepath"

	fusefs "bazil.org/fuse/fs"
	_ "github.com/rclone/rclone/cmd/mount" // the drives themselves
	"github.com/rclone/rclone/cmd/mountlib"
	"github.com/rclone/rclone/vfs"
	"golang.org/x/sys/unix"
)

func init() {
	// cmd/mount registered itself first, being imported above.
	_, mount := mountlib.ResolveMountMethod("mount")
	mountlib.AddRc("mount", safeForOwnUse(mount))
}

// safeForOwnUse stops a new drive being able to freeze this process when it
// opens files on it (see third_party/bazil-fuse/ENTER-SPACE-LIGHT.md).
func safeForOwnUse(mount mountlib.MountFn) mountlib.MountFn {
	return func(v *vfs.VFS, point string, opt *mountlib.Options) (<-chan error, func() error, string, error) {
		errChan, unmount, actual, err := mount(v, point, opt)
		if err != nil {
			return errChan, unmount, actual, err
		}
		if err := endPolling(point); err != nil {
			return nil, nil, "", errors.Join(fmt.Errorf("preparing the drive: %w", err), unmount())
		}
		return errChan, unmount, actual, nil
	}
}

// endPolling gets the kernel's first POLL question asked and answered "not
// supported", after which it never asks again. It must use plain system
// calls: they release the Go processor, which the os package would hold.
func endPolling(point string) error {
	fd, err := unix.Open(filepath.Join(point, fusefs.PollProbeName), unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	for {
		_, err = unix.Poll([]unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}, 0)
		if !errors.Is(err, unix.EINTR) {
			return err
		}
	}
}
