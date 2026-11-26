// Package paths resolves where Enter Space Light keeps its settings, state,
// logs and VFS cache on each platform.
//
// Everything can be relocated under one directory with ESL_HOME, which the
// tests and the end-to-end suites use so they never touch a real profile.
package paths

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// AppName is the human-readable product name.
const AppName = "Enter Space Light"

// dirName is the per-platform folder name.
func dirName() string {
	if runtime.GOOS == "windows" {
		return "EnterSpaceLight"
	}
	return "enter-space-light"
}

// base, when set, replaces the per-user directories (Android, where the app
// passes its private storage). Unlike ESL_HOME it is not a sandbox.
var base struct{ config, state, cache string }

// SetBase puts settings, state and the VFS cache in these directories.
func SetBase(config, state, cache string) {
	base.config, base.state, base.cache = config, state, cache
}

// Home returns the ESL_HOME override, or "".
func Home() string { return os.Getenv("ESL_HOME") }

// ConfigDir holds settings.json. Roams on Windows (%APPDATA%).
func ConfigDir() string {
	if h := Home(); h != "" {
		return filepath.Join(h, "config")
	}
	if base.config != "" {
		return base.config
	}
	d, err := os.UserConfigDir()
	if err != nil {
		d = os.TempDir()
	}
	return filepath.Join(d, dirName())
}

// StateDir holds the API handshake file, the lock, job history, logs and
// bisync work files. Machine-local (%LOCALAPPDATA% / XDG_STATE_HOME).
func StateDir() string {
	if h := Home(); h != "" {
		return filepath.Join(h, "state")
	}
	if base.state != "" {
		return base.state
	}
	switch runtime.GOOS {
	case "windows":
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, dirName())
		}
	case "linux", "freebsd":
		if d := os.Getenv("XDG_STATE_HOME"); d != "" {
			return filepath.Join(d, dirName())
		}
		if hd, err := os.UserHomeDir(); err == nil {
			return filepath.Join(hd, ".local", "state", dirName())
		}
	}
	d, err := os.UserCacheDir()
	if err != nil {
		d = os.TempDir()
	}
	return filepath.Join(d, dirName(), "state")
}

// CacheDir holds the rclone VFS cache. It is deliberately NOT rclone's
// default cache dir: two processes sharing one VFS cache for the same remote
// corrupt each other, and users commonly also run the rclone CLI.
func CacheDir() string {
	if h := Home(); h != "" {
		return filepath.Join(h, "cache")
	}
	if base.cache != "" {
		return base.cache
	}
	if runtime.GOOS == "windows" {
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, dirName(), "cache")
		}
	}
	d, err := os.UserCacheDir()
	if err != nil {
		d = os.TempDir()
	}
	return filepath.Join(d, dirName())
}

// LogDir holds the rotating daemon log.
func LogDir() string { return filepath.Join(StateDir(), "logs") }

// LogFile is the current daemon log.
func LogFile() string { return filepath.Join(LogDir(), "esl.log") }

// SettingsFile is the user's mounts, sync jobs and preferences.
func SettingsFile() string { return filepath.Join(ConfigDir(), "settings.json") }

// APIFile is the handshake the daemon writes so the CLI, tray and UI can
// find it: address and bearer token. Readable only by the user.
func APIFile() string { return filepath.Join(StateDir(), "api.json") }

// LockFile guarantees a single daemon per user.
func LockFile() string { return filepath.Join(StateDir(), "daemon.lock") }

// HistoryFile keeps recent sync results and schedule bookkeeping.
func HistoryFile() string { return filepath.Join(StateDir(), "history.json") }

// BisyncDir is bisync's work directory (listings, locks).
func BisyncDir() string { return filepath.Join(StateDir(), "bisync") }

// RcloneConfig returns the rclone.conf path to use, or "" for rclone's own
// default (shared with the rclone CLI). ESL_HOME always wins, even over
// RCLONE_CONFIG: a sandbox must never read or write the user's real config.
func RcloneConfig() string {
	if h := Home(); h != "" {
		return filepath.Join(h, "rclone.conf")
	}
	if base.config != "" {
		return filepath.Join(base.config, "rclone.conf")
	}
	return os.Getenv("RCLONE_CONFIG")
}

// Sandboxed reports whether ESL_HOME is set. Every side effect outside it
// (file-manager bookmarks, autostart, the keychain) is disabled or moved.
func Sandboxed() bool { return Home() != "" }

// MountRoot is where automatic mount points go: the user's preference,
// else ~/Cloud — or <ESL_HOME>/mnt in a sandbox.
func MountRoot(pref string) string {
	pref = strings.TrimSpace(pref)
	home, _ := os.UserHomeDir()
	switch {
	case strings.HasPrefix(pref, "~/"):
		return filepath.Join(home, pref[2:])
	case pref != "":
		return pref
	case Sandboxed():
		return filepath.Join(Home(), "mnt")
	default:
		return filepath.Join(home, "Cloud")
	}
}

// EnsureAll creates every directory with user-only permissions.
func EnsureAll() error {
	for _, d := range []string{ConfigDir(), StateDir(), CacheDir(), LogDir(), BisyncDir()} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	return nil
}
