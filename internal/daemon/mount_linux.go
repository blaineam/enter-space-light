//go:build linux && !android

package daemon

// Every program that runs the daemon, tests included, needs the Linux drives
// that are safe for its own syncs to use.
import _ "github.com/blaineam/enter-space-light/internal/linuxmount"
