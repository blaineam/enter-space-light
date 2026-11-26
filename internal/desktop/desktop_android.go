package desktop

import (
	"errors"
	"os/exec"
	"strings"

	"github.com/blaineam/enter-space-light/internal/android"
	"github.com/rclone/rclone/lib/oauthutil"
)

// Android has no xdg-open: rclone's cloud sign-in must go through the app.
func init() { oauthutil.OpenURL = OpenURL }

var errNoApp = errors.New("the Android app isn't running")

func hideWindow(*exec.Cmd) {}

// HasDesktop is false: there is no tray or settings window to launch.
func HasDesktop() bool { return false }

// OpenFolder shows a connected drive in the Files app.
func OpenFolder(p string) error {
	h := android.Current()
	if h == nil {
		return errNoApp
	}
	if id, ok := strings.CutPrefix(p, "esl://"); ok && h.OpenDrive(id) {
		return nil
	}
	return errors.New("open this folder in the Files app")
}

// OpenURL shows a web page in the browser.
func OpenURL(u string) error {
	if h := android.Current(); h != nil && h.OpenURL(u) {
		return nil
	}
	return errors.New("no browser could be opened")
}

// SetAutostart turns "start when the phone starts" on or off.
func SetAutostart(on bool, _ bool) error {
	if h := android.Current(); h != nil && h.SetStartOnBoot(on) {
		return nil
	}
	return errNoApp
}

// Autostart reports whether the app starts with the phone.
func Autostart() bool {
	h := android.Current()
	return h != nil && h.StartsOnBoot()
}

// AddBookmark is a Linux feature.
func AddBookmark(string, string) error { return nil }

// RemoveBookmark is a Linux feature.
func RemoveBookmark(string) error { return nil }

func notifyWindows(string, string) {}

func notifyAndroid(title, body string) {
	if h := android.Current(); h != nil {
		h.Notify(title, body)
	}
}
