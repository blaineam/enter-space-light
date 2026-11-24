// Package volumes detects drives being connected and disconnected, so sync
// jobs can run when a particular USB drive is plugged in.
//
// It polls: a full scan is a few syscalls, so a 3-second poll costs nothing
// measurable and is far more robust than device-notification plumbing
// (which differs per desktop, misses drives present at login, and needs a
// window handle on Windows).
package volumes

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

// Volume is a mounted filesystem the user could sync to or from.
type Volume struct {
	ID        string `json:"id"`        // volume serial (Windows) / filesystem UUID (Linux)
	Label     string `json:"label"`     // user-visible name
	Root      string `json:"root"`      // "E:\" or "/media/me/USB"
	Removable bool   `json:"removable"` // USB stick, SD card, external disk
	System    bool   `json:"system"`    // the OS drive; never a trigger target
}

// Key identifies a volume across polls.
func (v Volume) Key() string {
	if v.ID != "" {
		return "id:" + strings.ToUpper(v.ID)
	}
	return "root:" + v.Root
}

// Matches reports whether v is the drive a job asked for. ID beats label;
// label matching is case-insensitive.
func (v Volume) Matches(id, label string) bool {
	if id != "" {
		return strings.EqualFold(v.ID, id)
	}
	return label != "" && strings.EqualFold(v.Label, label)
}

// Lister returns the volumes present right now.
type Lister func() ([]Volume, error)

// Watcher reports arrivals and removals.
type Watcher struct {
	list  Lister
	every time.Duration

	mu      sync.RWMutex
	present map[string]Volume
}

// NewWatcher polls list every interval.
func NewWatcher(list Lister, every time.Duration) *Watcher {
	if list == nil {
		list = List
	}
	return &Watcher{list: list, every: every, present: map[string]Volume{}}
}

// Current returns the volumes seen at the last poll, sorted by root.
func (w *Watcher) Current() []Volume {
	w.mu.RLock()
	defer w.mu.RUnlock()
	out := make([]Volume, 0, len(w.present))
	for _, v := range w.present {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Root < out[j].Root })
	return out
}

// Poll scans once and returns what arrived and what left since last time.
// The very first poll reports everything present as arrived, so a drive
// plugged in before login still triggers its job.
func (w *Watcher) Poll() (arrived, removed []Volume, err error) {
	vols, err := w.list()
	if err != nil {
		return nil, nil, err
	}
	next := make(map[string]Volume, len(vols))
	for _, v := range vols {
		next[v.Key()] = v
	}
	w.mu.Lock()
	for k, v := range next {
		if _, ok := w.present[k]; !ok {
			arrived = append(arrived, v)
		}
	}
	for k, v := range w.present {
		if _, ok := next[k]; !ok {
			removed = append(removed, v)
		}
	}
	w.present = next
	w.mu.Unlock()
	return arrived, removed, nil
}

// Run polls until ctx ends, calling onArrive for each new volume.
func (w *Watcher) Run(ctx context.Context, onArrive, onRemove func(Volume)) {
	t := time.NewTicker(w.every)
	defer t.Stop()
	for {
		arrived, removed, err := w.Poll()
		if err == nil {
			for _, v := range arrived {
				if onArrive != nil {
					onArrive(v)
				}
			}
			for _, v := range removed {
				if onRemove != nil {
					onRemove(v)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
