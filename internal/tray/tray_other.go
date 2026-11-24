//go:build !windows && !linux

package tray

// Run has no tray on this platform; it waits for quit.
func Run(_ func() Model, _ <-chan struct{}, _ Actions, quit <-chan struct{}) { <-quit }

// Quit is a no-op here.
func Quit() {}
