//go:build e2e && windows

package daemon_test

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

// ghostDrive recreates a disconnected mapped network drive on letter: the
// DOS device exists (the letter is taken), but opening it fails with
// "network path not found", which Go reports as os.IsNotExist.
func ghostDrive(t *testing.T, letter string) bool {
	t.Helper()
	name, _ := windows.UTF16PtrFromString(letter)
	target, _ := windows.UTF16PtrFromString(`\Device\LanmanRedirector\;` + letter + `0000000000000000\esl-no-such-host\share`)
	if err := windows.DefineDosDevice(windows.DDD_RAW_TARGET_PATH, name, target); err != nil {
		t.Fatalf("creating the ghost drive: %v", err)
	}
	t.Cleanup(func() {
		_ = windows.DefineDosDevice(windows.DDD_REMOVE_DEFINITION, name, nil)
	})
	_, err := os.Stat(letter + `\`)
	t.Logf("ghost %s stat: %v (IsNotExist=%v)", letter, err, os.IsNotExist(err))
	return true
}
