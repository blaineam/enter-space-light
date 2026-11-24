//go:build linux

package mounts

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/blaineam/enter-space-light/internal/desktop"
	"github.com/blaineam/enter-space-light/internal/paths"
	"github.com/blaineam/enter-space-light/internal/settings"
	"golang.org/x/sys/unix"
)

const fuseSuperMagic = 0x65735546

// OSPlatform is the Linux implementation.
type OSPlatform struct{}

// MountRoot is where automatic mount points go (~/Cloud by default).
func MountRoot(p settings.Prefs) string { return paths.MountRoot(p.MountRoot) }

// ResolvePoint is where m will be mounted.
func ResolvePoint(m settings.Mount, p settings.Prefs) string {
	mp := strings.TrimSpace(m.MountPoint)
	if mp == "" {
		return filepath.Join(MountRoot(p), m.Name)
	}
	if strings.HasPrefix(mp, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, mp[2:])
	}
	return mp
}

// Prepare creates the folder, clearing a stale FUSE mount left by a crash
// ("Transport endpoint is not connected").
func (OSPlatform) Prepare(m settings.Mount, p settings.Prefs) (string, error) {
	if _, err := exec.LookPath("fusermount3"); err != nil {
		if _, err := exec.LookPath("fusermount"); err != nil {
			return "", errors.New("FUSE is not installed — install the fuse3 package (e.g. sudo apt install fuse3)")
		}
	}
	mp := ResolvePoint(m, p)
	if _, err := os.Stat(mp); err != nil && errors.Is(err, syscall.ENOTCONN) {
		lazyUnmount(mp)
	}
	if err := os.MkdirAll(mp, 0o700); err != nil {
		if errors.Is(err, syscall.ENOTCONN) || errors.Is(err, syscall.EEXIST) {
			lazyUnmount(mp)
			err = os.MkdirAll(mp, 0o700)
		}
		if err != nil {
			return "", fmt.Errorf("cannot create %s: %w", mp, err)
		}
	}
	var st unix.Statfs_t
	if err := unix.Statfs(mp, &st); err == nil && st.Type == fuseSuperMagic {
		// Something (an old rclone, a previous run) is still mounted here.
		lazyUnmount(mp)
	}
	entries, err := os.ReadDir(mp)
	if err != nil {
		return "", err
	}
	if len(entries) > 0 {
		return "", fmt.Errorf("folder %s is not empty — choose an empty or new folder", mp)
	}
	return mp, nil
}

func lazyUnmount(mp string) {
	for _, bin := range []string{"fusermount3", "fusermount"} {
		if path, err := exec.LookPath(bin); err == nil {
			if exec.Command(path, "-uz", mp).Run() == nil {
				return
			}
		}
	}
}

// Healthy confirms the folder is still a FUSE filesystem and answers.
func (OSPlatform) Healthy(mp string) error {
	var st unix.Statfs_t
	if err := unix.Statfs(mp, &st); err != nil {
		return err
	}
	if st.Type != fuseSuperMagic {
		return errors.New("the folder is no longer mounted")
	}
	f, err := os.Open(mp)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = f.Readdirnames(1)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// Released removes the empty folder we created, so ~/Cloud doesn't fill
// with dead directories. Only removes it if it is empty and not a mount.
func (OSPlatform) Released(_ settings.Mount, mp string) {
	if mp == "" {
		return
	}
	var st unix.Statfs_t
	if unix.Statfs(mp, &st) == nil && st.Type == fuseSuperMagic {
		return
	}
	_ = os.Remove(mp) // fails harmlessly if not empty
}

// Mounted adds the folder to the GTK file-manager sidebar.
func (OSPlatform) Mounted(m settings.Mount, mp string, p settings.Prefs) {
	if p.BookmarksEnabled() {
		_ = desktop.AddBookmark(mp, m.Name)
	}
}

// Forget removes the sidebar bookmark.
func (OSPlatform) Forget(m settings.Mount, mp string) {
	if mp != "" {
		_ = desktop.RemoveBookmark(mp)
	}
}

// FreeBytes of the filesystem holding dir.
func (OSPlatform) FreeBytes(dir string) int64 {
	_ = os.MkdirAll(dir, 0o700)
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0
	}
	return int64(st.Bavail) * int64(st.Bsize)
}
