// Package desktop is the thin layer between the daemon and the user's
// desktop: opening folders and the UI window, file-manager bookmarks, and
// starting at login.
package desktop

import (
	"errors"
	"os"
	"os/exec"
)

var errSandboxed = errors.New("start at sign-in is disabled while ESL_HOME is set")

// detach starts a GUI helper without tying it to our lifetime or stdio.
func detach(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// Executable is the running binary, resolved through symlinks.
func Executable() string {
	p, err := os.Executable()
	if err != nil {
		return os.Args[0]
	}
	return p
}
