//go:build !windows && !linux

package volumes

// List is unsupported here; the daemon still runs (for development on
// macOS) but drive triggers never fire.
func List() ([]Volume, error) { return nil, nil }
