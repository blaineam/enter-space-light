// Package lock guarantees one daemon per user. A second `esl run` (a
// double-click, autostart racing a manual start) finds the lock held and
// just opens the UI of the running one instead.
package lock

import (
	"errors"

	"github.com/gofrs/flock"
)

// ErrHeld means another daemon owns the lock.
var ErrHeld = errors.New("Enter Space Light is already running")

// Lock is a held instance lock.
type Lock struct{ f *flock.Flock }

// Acquire takes the lock at path without blocking. The OS releases it if
// the process dies, so a crash never leaves a stale lock behind.
func Acquire(path string) (*Lock, error) {
	f := flock.New(path)
	ok, err := f.TryLock()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrHeld
	}
	return &Lock{f: f}, nil
}

// Release drops the lock.
func (l *Lock) Release() {
	if l != nil && l.f != nil {
		_ = l.f.Unlock()
	}
}
