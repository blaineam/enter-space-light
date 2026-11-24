//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// detachProcess starts the daemon in its own process group with no console.
func detachProcess(cmd *exec.Cmd) {
	const detached = 0x00000008 // DETACHED_PROCESS
	const newGroup = 0x00000200 // CREATE_NEW_PROCESS_GROUP
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detached | newGroup, HideWindow: true}
}
