//go:build !windows && !linux

package mounts

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/blaineam/enter-space-light/internal/paths"
	"github.com/blaineam/enter-space-light/internal/settings"
)

// OSPlatform lets the daemon run for development on other systems. Mounting
// needs a FUSE implementation rclone supports there.
type OSPlatform struct{}

// Prepare creates ~/Cloud/<name>.
func (OSPlatform) Prepare(m settings.Mount, p settings.Prefs) (string, error) {
	mp := strings.TrimSpace(m.MountPoint)
	if mp == "" {
		mp = filepath.Join(paths.MountRoot(p.MountRoot), m.Name)
	}
	return mp, os.MkdirAll(mp, 0o700)
}

// Healthy stats the folder.
func (OSPlatform) Healthy(mp string) error { _, err := os.Stat(mp); return err }

// Released is a no-op.
func (OSPlatform) Released(settings.Mount, string) {}

// Mounted is a no-op.
func (OSPlatform) Mounted(settings.Mount, string, settings.Prefs) {}

// Forget is a no-op.
func (OSPlatform) Forget(settings.Mount, string) {}

// FreeBytes is unknown; the cache budget floors at 1 GiB.
func (OSPlatform) FreeBytes(string) int64 { return 0 }
