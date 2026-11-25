//go:build windows

package mounts

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/blaineam/enter-space-light/internal/settings"
	"github.com/blaineam/enter-space-light/internal/winfsp"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// OSPlatform is the Windows implementation.
type OSPlatform struct{}

var (
	lettersMu     sync.Mutex
	failedLetters = map[byte]bool{}      // letters where a mount failed this session
	givenLetters  = map[byte]time.Time{} // recently handed out, mount still starting
)

// letterTaken reports whether Windows has any claim on a drive letter: a
// device (connected or not), or a remembered network drive that Explorer
// shows with a red X.
func letterTaken(l byte) bool {
	if mask, err := windows.GetLogicalDrives(); err == nil && mask&(1<<uint(l-'A')) != 0 {
		return true
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, `Network\`+string(rune(l)), registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	_ = k.Close()
	return true
}

// autoLetter picks a letter, reserving it briefly so drives mounting at the
// same moment don't choose the same one.
func autoLetter() (string, error) {
	lettersMu.Lock()
	defer lettersMu.Unlock()
	mp, err := pickLetter(func(l byte) bool {
		return letterTaken(l) || time.Since(givenLetters[l]) < 20*time.Second
	}, failedLetters)
	if err == nil {
		givenLetters[mp[0]] = time.Now()
	}
	return mp, err
}

// MountFailed makes the next automatic attempt try a different letter.
func (OSPlatform) MountFailed(m settings.Mount, mountPoint string) {
	if strings.TrimSpace(m.MountPoint) != "" || !settings.IsDriveLetter(mountPoint) {
		return
	}
	lettersMu.Lock()
	defer lettersMu.Unlock()
	failedLetters[mountPoint[0]] = true
	delete(givenLetters, mountPoint[0])
}

// Prepare: "" → the next free letter (see pickLetter); "X:" must be free; a folder must not exist yet (WinFsp creates it as a
// reparse point) — an empty leftover folder is removed for the user.
func (OSPlatform) Prepare(m settings.Mount, _ settings.Prefs) (string, error) {
	if !winfsp.Installed() {
		return "", errors.New("WinFsp is not installed — install it from the app's Settings")
	}
	mp := strings.TrimSpace(m.MountPoint)
	if mp == "" {
		return autoLetter()
	}
	if settings.IsDriveLetter(mp) {
		letter := strings.ToUpper(mp[:1])
		if letterTaken(letter[0]) {
			return "", fmt.Errorf("drive %s: is already in use (possibly by a disconnected network drive) — pick another letter or leave it automatic", letter)
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

// Mounted: the drive letter itself is the Explorer integration. The letter
// is now Windows' to report as taken, so drop our reservation: after a quick
// disconnect the drive gets the same letter back.
func (OSPlatform) Mounted(_ settings.Mount, mountPoint string, _ settings.Prefs) {
	if settings.IsDriveLetter(mountPoint) {
		lettersMu.Lock()
		defer lettersMu.Unlock()
		delete(givenLetters, strings.ToUpper(mountPoint)[0])
	}
}

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
