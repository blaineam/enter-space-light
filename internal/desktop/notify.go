package desktop

import (
	"os/exec"
	"runtime"
)

// Notify shows a desktop notification where one is available. It never
// blocks and never fails loudly: the tray badge and the UI carry the same
// information.
func Notify(title, body string) {
	switch runtime.GOOS {
	case "linux", "freebsd":
		if p, err := exec.LookPath("notify-send"); err == nil {
			_ = detach(p, "--app-name=Enter Space Light", "--icon=enter-space-light", title, body)
		}
	case "windows":
		notifyWindows(title, body)
	}
}
