//go:build windows

package desktop

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/blaineam/enter-space-light/internal/paths"
	"golang.org/x/sys/windows/registry"
)

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`
const runValue = "EnterSpaceLight"

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}

// OpenFolder shows a folder or drive in Explorer.
func OpenFolder(p string) error {
	if len(p) == 2 && p[1] == ':' {
		p += `\`
	}
	return detach("explorer.exe", p)
}

// OpenURL opens the UI as a standalone app window (Edge --app is on every
// supported Windows), falling back to the default browser.
func OpenURL(url string) error {
	if edge := findEdge(); edge != "" {
		profile := filepath.Join(os.Getenv("LOCALAPPDATA"), "EnterSpaceLight", "ui-profile")
		if err := detach(edge, "--app="+url, "--window-size=1040,760", "--user-data-dir="+profile, "--no-first-run"); err == nil {
			return nil
		}
	}
	return detach("rundll32.exe", "url.dll,FileProtocolHandler", url)
}

func findEdge() string {
	for _, base := range []string{os.Getenv("ProgramFiles(x86)"), os.Getenv("ProgramFiles"), os.Getenv("LOCALAPPDATA")} {
		if base == "" {
			continue
		}
		p := filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// guiExecutable prefers the windowless EnterSpaceLight.exe next to us, so
// login doesn't flash a console window.
func guiExecutable() string {
	exe := Executable()
	gui := filepath.Join(filepath.Dir(exe), "EnterSpaceLight.exe")
	if _, err := os.Stat(gui); err == nil {
		return gui
	}
	return exe
}

// SetAutostart adds or removes the per-user Run entry. No admin needed.
func SetAutostart(on bool, _ bool) error {
	if paths.Sandboxed() {
		return errSandboxed
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer func() { _ = k.Close() }()
	if !on {
		if err := k.DeleteValue(runValue); err != nil && err != registry.ErrNotExist {
			return err
		}
		return nil
	}
	return k.SetStringValue(runValue, fmt.Sprintf(`"%s" run`, guiExecutable()))
}

// Autostart reports whether the Run entry exists and points at us.
func Autostart() bool {
	if paths.Sandboxed() {
		return false
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer func() { _ = k.Close() }()
	v, _, err := k.GetStringValue(runValue)
	return err == nil && strings.Contains(strings.ToLower(v), strings.ToLower(filepath.Dir(Executable())))
}

// AddBookmark is unnecessary on Windows: the drive letter is the bookmark.
func AddBookmark(string, string) error { return nil }

// RemoveBookmark is unnecessary on Windows.
func RemoveBookmark(string) error { return nil }

// HasDesktop is always true on Windows.
func HasDesktop() bool { return true }

// AppUserModelID the installer gives the Start-menu shortcut; Windows only
// shows toasts for an app ID that has one.
const AppUserModelID = "EnterSpaceLight"

const toastScript = `$ErrorActionPreference='SilentlyContinue'
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] > $null
$t = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02)
$x = $t.GetElementsByTagName('text')
$x.Item(0).AppendChild($t.CreateTextNode($env:ESL_TOAST_TITLE)) > $null
$x.Item(1).AppendChild($t.CreateTextNode($env:ESL_TOAST_BODY)) > $null
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($env:ESL_TOAST_APP).Show([Windows.UI.Notifications.ToastNotification]::new($t))`

// notifyWindows raises a toast. The text travels in environment variables,
// never in the script, so nothing in it can be interpreted by PowerShell.
func notifyWindows(title, body string) {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", toastScript)
	cmd.Env = append(os.Environ(), "ESL_TOAST_TITLE="+title, "ESL_TOAST_BODY="+body, "ESL_TOAST_APP="+AppUserModelID)
	hideWindow(cmd)
	if cmd.Start() == nil {
		go func() { _ = cmd.Wait() }()
	}
}
