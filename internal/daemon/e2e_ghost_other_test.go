//go:build e2e && !windows

package daemon_test

import "testing"

func ghostDrive(*testing.T, string) bool { return false }
