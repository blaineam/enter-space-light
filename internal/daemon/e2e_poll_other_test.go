//go:build e2e && !linux

package daemon_test

import "testing"

// primePoll is only needed with Linux FUSE (see e2e_poll_linux_test.go).
func primePoll(*testing.T, string) {}
