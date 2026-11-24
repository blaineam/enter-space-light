//go:build windows

package mounts

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/blaineam/enter-space-light/internal/settings"
	"github.com/blaineam/enter-space-light/internal/winfsp"
	"golang.org/x/sys/windows"
)

// OSPlatform is the Windows implementation.
type OSPlatform struct{}

// Prepare: "" → "*" (rclone picks the next free letter, from Z: down);
// "X:" must be free; a folder must not exist yet (WinFsp creates it as a
// reparse point) — an empty leftover folder is removed for the user.
func (OSPlatform) Prepare(m settings.Mount, _ settings.Prefs) (string, error) {
	if !winfsp.Installed() {
		return "", errors.New("WinFsp is not installed — install it from the app's Settings")
	}
	mp := strings.TrimSpace(m.MountPoint)
	if mp == "" {
		return "*", nil
	}
	if settings.IsDriveLetter(mp) {
		letter := strings.ToUpper(mp[:1])
		mask, err := windows.GetLogicalDrives()
		if err == nil && mask&(1<<uint(letter[0]-'A')) != 0 {
			return "", fmt.Errorf("drive %s: is already in use — pick another letter or leave it automatic", letter)
		}
		return letter + ":", nil
	}
	mp = filepath.Clean(mp)
	if fi, err := os.Stat(mp); err == nil {
		if !fi.IsDir() {
			return "", fmt.Errorf("%s exists and is a file", mp)
		}
		entries, err := os.ReadDir(mp)
		if err != nil || len(entries) > 0 {
			return "", fmt.Errorf("folder %s is not empty — choose an empty or new folder", mp)
		}
		if err := os.Remove(mp); err != nil {
			return "", fmt.Errorf("cannot prepare %s: %w", mp, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(mp), 0o700); err != nil {
		return "", err
	}
	return mp, nil
}

// Healthy lists the root of the drive.
func (OSPlatform) Healthy(mp string) error {
	p := mp
	if len(p) == 2 && p[1] == ':' {
		p += `\`
	}
	f, err := os.Open(p)
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

// Released has nothing to clean up on Windows.
func (OSPlatform) Released(settings.Mount, string) {}

// Mounted: the drive letter itself is the Explorer integration.
func (OSPlatform) Mounted(settings.Mount, string, settings.Prefs) {}

// Forget has nothing to clean up on Windows.
func (OSPlatform) Forget(settings.Mount, string) {}

// FreeBytes of the volume holding dir.
func (OSPlatform) FreeBytes(dir string) int64 {
	_ = os.MkdirAll(dir, 0o700)
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, &total, &totalFree); err != nil {
		return 0
	}
	return int64(free)
}
