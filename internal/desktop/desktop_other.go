//go:build !windows && !linux

package desktop

import (
	"errors"
	"os/exec"
)

func hideWindow(*exec.Cmd) {}

// OpenFolder uses `open`.
func OpenFolder(p string) error { return detach("open", p) }

// OpenURL uses `open`.
func OpenURL(u string) error { return detach("open", u) }

// SetAutostart is unsupported here.
func SetAutostart(bool, bool) error { return errors.New("autostart is supported on Windows and Linux") }

// Autostart is always false here.
func Autostart() bool { return false }

// AddBookmark is a no-op here.
func AddBookmark(string, string) error { return nil }

// RemoveBookmark is a no-op here.
func RemoveBookmark(string) error { return nil }

// HasDesktop is true for development.
func HasDesktop() bool { return true }

func notifyWindows(string, string) {}

func notifyAndroid(string, string) {}
