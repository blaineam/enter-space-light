//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// detachProcess starts the daemon in its own session so closing the
// terminal doesn't take it down.
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
