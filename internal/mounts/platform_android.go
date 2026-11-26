package mounts

import (
	"os"

	"github.com/blaineam/enter-space-light/internal/androidfs"
	"github.com/blaineam/enter-space-light/internal/settings"
	"golang.org/x/sys/unix"
)

// OSPlatform on Android: a "mounted" drive is a live VFS that the app's
// DocumentsProvider serves to the Files app and other apps.
type OSPlatform struct{}

// Prepare registers the Android drive layer and names the drive by its ID.
func (OSPlatform) Prepare(m settings.Mount, _ settings.Prefs) (string, error) {
	androidfs.Register()
	return androidfs.Prefix + m.ID, nil
}

// Healthy lists the drive's root. mp is androidfs.Prefix + ID.
func (OSPlatform) Healthy(mp string) error { return androidfs.Healthy(mp[len(androidfs.Prefix):]) }

// Released is a no-op: nothing was created on disk.
func (OSPlatform) Released(settings.Mount, string) {}

// Mounted is a no-op; the app watches status and refreshes the Files app.
func (OSPlatform) Mounted(settings.Mount, string, settings.Prefs) {}

// Forget is a no-op.
func (OSPlatform) Forget(settings.Mount, string) {}

// FreeBytes of the filesystem holding dir.
func (OSPlatform) FreeBytes(dir string) int64 {
	_ = os.MkdirAll(dir, 0o700)
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0
	}
	return int64(st.Bavail) * int64(st.Bsize)
}
