//go:build windows

package winfsp

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"golang.org/x/sys/windows/registry"
)

// InstallDir returns WinFsp's install directory from the registry.
func InstallDir() string {
	for _, view := range []uint32{registry.WOW64_32KEY, registry.WOW64_64KEY} {
		k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\WinFsp`, registry.QUERY_VALUE|view)
		if err != nil {
			continue
		}
		dir, _, err := k.GetStringValue("InstallDir")
		_ = k.Close()
		if err == nil && dir != "" {
			return dir
		}
	}
	return ""
}

func dllName() string {
	switch runtime.GOARCH {
	case "arm64":
		return "winfsp-a64.dll"
	case "386":
		return "winfsp-x86.dll"
	default:
		return "winfsp-x64.dll"
	}
}

// Installed reports whether the WinFsp DLL for this architecture exists.
func Installed() bool {
	dir := InstallDir()
	if dir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, "bin", dllName()))
	return err == nil
}

// Install launches the Windows Package Manager to install WinFsp. Windows
// shows its own elevation prompt; this returns once winget exits.
func Install() error {
	cmd := exec.Command("winget", "install", "--exact", "--id", WingetID,
		"--accept-source-agreements", "--accept-package-agreements", "--silent")
	return cmd.Run()
}
