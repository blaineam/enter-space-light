//go:build !windows

package winfsp

import "errors"

// InstallDir is empty off Windows.
func InstallDir() string { return "" }

// Installed is true off Windows: nothing to install.
func Installed() bool { return true }

// Install is unsupported off Windows.
func Install() error { return errors.New("WinFsp is Windows-only") }
