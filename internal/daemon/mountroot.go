package daemon

import (
	"runtime"

	"github.com/blaineam/enter-space-light/internal/paths"
	"github.com/blaineam/enter-space-light/internal/settings"
)

// mountRoot is where automatic mount points go, for display.
func mountRoot(p settings.Prefs) string {
	if runtime.GOOS == "windows" {
		return ""
	}
	return paths.MountRoot(p.MountRoot)
}
