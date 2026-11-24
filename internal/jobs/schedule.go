package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/blaineam/enter-space-light/internal/settings"
	"github.com/blaineam/enter-space-light/internal/volumes"
)

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// Scheduler decides when jobs run. It is a pure function of the clock, the
// settings and the runner's bookkeeping, evaluated on a tick, so it
// naturally catches up after sleep: an interval that elapsed while the
// laptop was closed fires once on wake, and so does a missed daily slot.
type Scheduler struct {
	Runner *Runner
	Store  *settings.Store
	Now    func() time.Time
	Logf   func(format string, args ...any)
	// StartDelay holds off "at start" jobs so the network is up after login.
	StartDelay time.Duration
	// VolumeCooldown ignores a drive re-appearing (a flaky hub) this soon.
	VolumeCooldown time.Duration

	mu        sync.Mutex
	started   time.Time
	startDone map[string]bool
}

func (s *Scheduler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Scheduler) logf(f string, a ...any) {
	if s.Logf != nil {
		s.Logf(f, a...)
	}
}

// Run ticks every interval until ctx ends.
func (s *Scheduler) Run(ctx context.Context, every time.Duration) {
	s.mu.Lock()
	s.started = s.now()
	s.mu.Unlock()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		s.Tick()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick evaluates every job once.
func (s *Scheduler) Tick() {
	now := s.now()
	doc := s.Store.Get()
	s.mu.Lock()
	if s.started.IsZero() {
		s.started = now
	}
	if s.startDone == nil {
		s.startDone = map[string]bool{}
	}
	started := s.started
	s.mu.Unlock()

	for _, j := range doc.Jobs {
		if !j.Enabled || s.Runner.Running(j.ID) {
			continue
		}
		if reason := s.due(j, now, started); reason != "" {
			s.fire(j, reason, "")
		}
	}
}

func (s *Scheduler) due(j settings.Job, now, started time.Time) string {
	tr := j.Triggers
	last := s.Runner.LastStart(j.ID)

	if tr.OnStart {
		s.mu.Lock()
		done := s.startDone[j.ID]
		s.mu.Unlock()
		if !done && now.Sub(started) >= s.StartDelay {
			s.mu.Lock()
			s.startDone[j.ID] = true
			s.mu.Unlock()
			return "start"
		}
	}
	if tr.Every != "" {
		if d, err := time.ParseDuration(tr.Every); err == nil {
			// A never-run job waits one interval from daemon start rather
			// than firing the instant it is created.
			ref := last
			if ref.IsZero() {
				ref = started
			}
			if now.Sub(ref) >= d {
				return "schedule"
			}
		}
	}
	if tr.Daily != "" {
		slot := DailySlot(tr.Daily, now)
		ld := s.Runner.LastDaily(j.ID)
		switch {
		case slot.IsZero():
		case ld.IsZero():
			// First sighting of this job: the slot that already passed
			// predates it, so arm for the next one instead of firing now.
			s.Runner.MarkDaily(j.ID, slot)
		case ld.Before(slot):
			s.Runner.MarkDaily(j.ID, slot)
			// A run that already happened after the slot (manual, drive,
			// interval) satisfies it.
			if last.Before(slot) {
				return "daily"
			}
		}
	}
	return ""
}

// DailySlot is the most recent occurrence of "HH:MM" at or before now, in
// local time (so it follows daylight-saving changes).
func DailySlot(hhmm string, now time.Time) time.Time {
	t, err := time.ParseInLocation("15:04", hhmm, now.Location())
	if err != nil {
		return time.Time{}
	}
	slot := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, now.Location())
	if slot.After(now) {
		slot = slot.AddDate(0, 0, -1)
	}
	return slot
}

// VolumeArrived fires every enabled job watching v.
func (s *Scheduler) VolumeArrived(v volumes.Volume) {
	if v.System {
		return
	}
	now := s.now()
	for _, j := range s.Store.Get().Jobs {
		tr := j.Triggers.OnVolume
		if !j.Enabled || tr == nil || !v.Matches(tr.ID, tr.Label) {
			continue
		}
		if last := s.Runner.LastStart(j.ID); !last.IsZero() && now.Sub(last) < s.VolumeCooldown {
			s.logf("drive %s reconnected; %q ran %s ago, skipping", v.Label, j.Name, now.Sub(last).Round(time.Second))
			continue
		}
		s.fire(j, "drive "+v.Label, v.Root)
	}
}

func (s *Scheduler) fire(j settings.Job, reason, root string) {
	err := s.Runner.Start(j.ID, reason, root)
	switch {
	case err == nil:
		s.logf("starting %q (%s)", j.Name, reason)
	case errors.Is(err, ErrAlreadyRunning):
	case strings.Contains(err.Error(), "encrypted"):
		// Quietly wait for the unlock; the UI already says so.
	default:
		s.logf("cannot start %q: %v", j.Name, err)
	}
}
