package jobs

import (
	"testing"
	"time"

	"github.com/blaineam/enter-space-light/internal/settings"
	"github.com/blaineam/enter-space-light/internal/volumes"
)

func TestDeferredRunBookkeeping(t *testing.T) {
	s := &Scheduler{}
	s.Defer("a", "daily", "")
	s.Defer("a", "drive USB", "/storage/1234-ABCD")
	if d, ok := s.takeDeferred("a"); !ok || d.reason != "drive USB" || d.volumeRoot != "/storage/1234-ABCD" {
		t.Fatalf("deferred = %+v %v", d, ok)
	}
	if _, ok := s.takeDeferred("a"); ok {
		t.Fatal("a deferred run happens once")
	}
}

// A daily backup that comes due on mobile data runs once Wi-Fi is back,
// exactly once, and is not run again for the same slot.
func TestHeldDailyRunHappensOnceWhenAllowed(t *testing.T) {
	j := job("d")
	j.Triggers.Daily = "02:30"
	r, rc, st, _ := setup(t, j)
	ck := &clock{t: time.Date(2026, 11, 23, 9, 0, 0, 0, time.Local)}
	r.c.Now = ck.Now
	s := scheduler(r, st, ck)
	why := ""
	s.Hold = func(settings.Job) string { return why }
	tick := func(at time.Time) {
		ck.Set(at)
		s.Tick()
		r.Wait()
	}
	tick(ck.Now())
	why = "waiting for Wi-Fi"
	tick(time.Date(2026, 11, 24, 2, 30, 0, 0, time.Local))
	tick(time.Date(2026, 11, 24, 3, 0, 0, 0, time.Local))
	if len(rc.calls) != 0 {
		t.Fatal("ran while held")
	}
	if got := s.Waiting(j); got != why {
		t.Fatalf("Waiting = %q", got)
	}
	why = ""
	tick(time.Date(2026, 11, 24, 8, 0, 0, 0, time.Local))
	tick(time.Date(2026, 11, 24, 8, 1, 0, 0, time.Local))
	if len(rc.calls) != 1 {
		t.Fatalf("want exactly one run, got %d", len(rc.calls))
	}
}

func TestPauseHeldStopsOnlyBackgroundRunsAndOwesThem(t *testing.T) {
	bg, manual := job("bg"), job("manual")
	r, rc, st, _ := setup(t, bg, manual)
	rc.block = make(chan struct{})
	ck := &clock{t: time.Date(2026, 11, 23, 9, 0, 0, 0, time.Local)}
	r.c.Now = ck.Now
	s := scheduler(r, st, ck)
	why := ""
	s.Hold = func(settings.Job) string { return why }
	if err := r.Start("bg", "schedule", ""); err != nil {
		t.Fatal(err)
	}
	if err := r.Start("manual", "manual", ""); err != nil {
		t.Fatal(err)
	}
	why = "waiting for Wi-Fi"
	if n := s.PauseHeld(); n != 1 {
		t.Fatalf("paused %d", n)
	}
	for deadline := time.Now().Add(5 * time.Second); r.Running("bg"); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the paused run did not stop")
		}
	}
	if !r.Running("manual") {
		t.Fatal("a run the user started was stopped")
	}
	close(rc.block)
	r.Wait()
	why = ""
	s.Tick()
	r.Wait()
	if len(rc.calls) != 3 {
		t.Fatalf("the paused job should run again: %d calls", len(rc.calls))
	}
}

func TestRemovedDriveDropsItsOwedRun(t *testing.T) {
	s := &Scheduler{}
	s.Defer("v", "drive USB", "/storage/AAAA-1111")
	s.VolumeRemoved(volumes.Volume{Root: "/storage/AAAA-1111"})
	if _, ok := s.takeDeferred("v"); ok {
		t.Fatal("still owed a run for a drive that is gone")
	}
}
