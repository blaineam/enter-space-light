//go:build linux

package desktop

import (
	"bufio"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/blaineam/enter-space-light/internal/paths"
)

func hideWindow(*exec.Cmd) {}

// HasDesktop reports a graphical session.
func HasDesktop() bool {
	return os.Getenv("WAYLAND_DISPLAY") != "" || os.Getenv("DISPLAY") != ""
}

// OpenFolder shows a folder in the user's file manager.
func OpenFolder(p string) error { return detach("xdg-open", p) }

// OpenURL opens the UI as an app window in a Chromium-family browser when
// one is installed (no tabs or address bar), else in the default browser.
func OpenURL(u string) error {
	for _, b := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable", "microsoft-edge", "brave-browser", "vivaldi"} {
		if p, err := exec.LookPath(b); err == nil {
			profile := filepath.Join(configHome(), "enter-space-light", "ui-profile")
			if detach(p, "--app="+u, "--window-size=1040,760", "--user-data-dir="+profile, "--no-first-run", "--class=EnterSpaceLight") == nil {
				return nil
			}
		}
	}
	return detach("xdg-open", u)
}

func configHome() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return d
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".config")
}

func autostartFile() string {
	return filepath.Join(configHome(), "autostart", "enter-space-light.desktop")
}

func unitFile() string {
	return filepath.Join(configHome(), "systemd", "user", "enter-space-light.service")
}

// SetAutostart installs an XDG autostart entry (desktop sessions, shows the
// tray) or, with headless, a systemd user service (servers; no tray).
func SetAutostart(on bool, headless bool) error {
	if paths.Sandboxed() {
		return errSandboxed
	}
	exe := Executable()
	if !on {
		e1 := os.Remove(autostartFile())
		if _, err := os.Stat(unitFile()); err == nil {
			_ = exec.Command("systemctl", "--user", "disable", "enter-space-light.service").Run()
		}
		e2 := os.Remove(unitFile())
		if errors.Is(e1, os.ErrNotExist) {
			e1 = nil
		}
		if errors.Is(e2, os.ErrNotExist) {
			e2 = nil
		}
		return errors.Join(e1, e2)
	}
	if headless {
		unit := fmt.Sprintf(`[Unit]
Description=Enter Space Light (rclone mounts and scheduled syncs)
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
ExecStart=%s run --no-tray
Restart=on-failure
RestartSec=10

[Install]
WantedBy=default.target
`, quoteExec(exe))
		if err := os.MkdirAll(filepath.Dir(unitFile()), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(unitFile(), []byte(unit), 0o644); err != nil {
			return err
		}
		_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
		return exec.Command("systemctl", "--user", "enable", "enter-space-light.service").Run()
	}
	entry := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=Enter Space Light
Comment=Keep cloud remotes mounted and synced
Exec=%s run
Icon=enter-space-light
Terminal=false
X-GNOME-Autostart-enabled=true
X-GNOME-Autostart-Delay=5
`, quoteExec(exe))
	if err := os.MkdirAll(filepath.Dir(autostartFile()), 0o755); err != nil {
		return err
	}
	return os.WriteFile(autostartFile(), []byte(entry), 0o644)
}

func quoteExec(p string) string {
	if strings.ContainsAny(p, " \t\"'\\") {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(p) + `"`
	}
	return p
}

// Autostart reports whether either autostart mechanism is installed.
func Autostart() bool {
	if paths.Sandboxed() {
		return false
	}
	if _, err := os.Stat(autostartFile()); err == nil {
		return true
	}
	_, err := os.Stat(unitFile())
	return err == nil
}

func bookmarksFile() string { return filepath.Join(configHome(), "gtk-3.0", "bookmarks") }

func bookmarkURI(p string) string {
	return (&url.URL{Scheme: "file", Path: p}).String()
}

// AddBookmark puts the mount in the sidebar of GTK file managers (Files,
// Nemo, Caja, Thunar). KDE Dolphin lists FUSE mounts under Devices itself.
func AddBookmark(p, name string) error {
	return editBookmarks(func(lines []string) []string {
		uri := bookmarkURI(p)
		for _, l := range lines {
			if strings.Fields(l + " ")[0] == uri {
				return lines
			}
		}
		return append(lines, uri+" "+name)
	})
}

// RemoveBookmark deletes the mount's sidebar entry.
func RemoveBookmark(p string) error {
	return editBookmarks(func(lines []string) []string {
		uri := bookmarkURI(p)
		out := lines[:0]
		for _, l := range lines {
			if f := strings.Fields(l); len(f) > 0 && f[0] == uri {
				continue
			}
			out = append(out, l)
		}
		return out
	})
}

func editBookmarks(fn func([]string) []string) error {
	if paths.Sandboxed() {
		return nil
	}
	path := bookmarksFile()
	var lines []string
	if f, err := os.Open(path); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if strings.TrimSpace(sc.Text()) != "" {
				lines = append(lines, sc.Text())
			}
		}
		_ = f.Close()
	}
	next := fn(append([]string(nil), lines...))
	if strings.Join(next, "\n") == strings.Join(lines, "\n") {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".esl-tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(next, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func notifyWindows(string, string) {}
