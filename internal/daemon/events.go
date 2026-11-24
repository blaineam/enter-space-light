package daemon

import (
	"sync"
	"time"
)

// hub coalesces "something changed" notifications from the mount manager,
// job runner and volume watcher, and fans them out to UI event streams and
// the tray. Bursts (a sync's progress ticks) collapse into at most one
// notification per interval.
type hub struct {
	mu      sync.Mutex
	subs    map[chan struct{}]struct{}
	pending bool
	every   time.Duration
}

func newHub(every time.Duration) *hub {
	return &hub{subs: map[chan struct{}]struct{}{}, every: every}
}

// notify schedules a broadcast.
func (h *hub) notify() {
	h.mu.Lock()
	if h.pending {
		h.mu.Unlock()
		return
	}
	h.pending = true
	h.mu.Unlock()
	time.AfterFunc(h.every, func() {
		h.mu.Lock()
		h.pending = false
		subs := make([]chan struct{}, 0, len(h.subs))
		for c := range h.subs {
			subs = append(subs, c)
		}
		h.mu.Unlock()
		for _, c := range subs {
			select {
			case c <- struct{}{}:
			default: // subscriber is behind; it will read the latest state anyway
			}
		}
	})
}

// subscribe returns a channel and its cancel func.
func (h *hub) subscribe() (<-chan struct{}, func()) {
	c := make(chan struct{}, 1)
	h.mu.Lock()
	h.subs[c] = struct{}{}
	h.mu.Unlock()
	return c, func() {
		h.mu.Lock()
		delete(h.subs, c)
		h.mu.Unlock()
	}
}
