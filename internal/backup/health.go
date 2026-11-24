package backup

import (
	"fmt"
	"time"
)

// Alert is something wrong with a backup that the user should know about.
type Alert struct {
	Kind     string `json:"kind"` // neverRan, repeatedFailure, guardAborted, missedSchedule, flatline
	Critical bool   `json:"critical"`
	Message  string `json:"message"`
}

// Health thresholds, as the Apple app's BackupWatchdog.
const (
	missedWindowMultiplier = 2.5
	repeatedFailures       = 3
	flatlineWindow         = 30 * 24 * time.Hour
	neverRanGrace          = 24 * time.Hour
	neverRanUnscheduled    = 7 * 24 * time.Hour
	minMissedWindow        = 6 * time.Hour
	driveMissedWindow      = 30 * 24 * time.Hour
)

// HealthInput describes one backup.
type HealthInput struct {
	Runs     []RunSummary  // newest first
	Created  time.Time     // when the backup was set up
	Interval time.Duration // expected gap between runs; 0 = no schedule
	OnDrive  bool          // runs when a drive is plugged in (may legitimately wait weeks)
	Now      time.Time
}

// Evaluate returns the most important alert, or nil if healthy.
// Precedence: never ran → repeated failure → guard abort → missed schedule → flatline.
func Evaluate(in HealthInput) *Alert {
	var lastOK *RunSummary
	for i := range in.Runs {
		if in.Runs[i].Outcome == Completed || in.Runs[i].Outcome == CompletedWithErrors {
			lastOK = &in.Runs[i]
			break
		}
	}
	window := missedWindow(in)

	if lastOK == nil {
		grace := neverRanUnscheduled
		if in.Interval > 0 {
			grace = maxDur(time.Duration(float64(in.Interval)*missedWindowMultiplier), neverRanGrace)
		}
		if in.OnDrive {
			grace = driveMissedWindow
		}
		if !in.Created.IsZero() && in.Now.Sub(in.Created) > grace {
			return &Alert{Kind: "neverRan", Critical: !in.OnDrive, Message: "This backup has never finished. Nothing is protected yet."}
		}
	}

	bad, critical := 0, false
	for _, r := range in.Runs {
		if r.Outcome == Completed {
			break
		}
		bad++
		critical = critical || r.Outcome == Failed || r.Outcome == Aborted
	}
	if bad >= repeatedFailures {
		return &Alert{Kind: "repeatedFailure", Critical: critical, Message: fmt.Sprintf("The last %d backups didn't complete.", bad)}
	}
	if len(in.Runs) > 0 && in.Runs[0].Outcome == Aborted {
		msg := "The last backup was stopped to protect your files."
		if r := in.Runs[0].Stats.AbortReason; r != nil && *r != "" {
			msg = *r
		}
		return &Alert{Kind: "guardAborted", Critical: true, Message: msg}
	}
	if lastOK != nil && window > 0 {
		since := in.Now.Sub(lastOK.FinishedAt)
		if since > window {
			return &Alert{Kind: "missedSchedule", Critical: since > 3*window,
				Message: fmt.Sprintf("No successful backup for %s.", roughly(since))}
		}
	}
	if flat := flatline(in); flat {
		return &Alert{Kind: "flatline", Message: "Backups keep succeeding but nothing has been copied for weeks. Is the source still the right folder?"}
	}
	return nil
}

func missedWindow(in HealthInput) time.Duration {
	if in.OnDrive {
		return driveMissedWindow
	}
	if in.Interval <= 0 {
		return 0
	}
	return maxDur(time.Duration(float64(in.Interval)*missedWindowMultiplier), minMissedWindow)
}

// flatline: ≥3 successes in 30 days, the oldest of them early in that
// window, all moving nothing while still checking files — after a past
// success that did move data.
func flatline(in HealthInput) bool {
	cut := in.Now.Add(-flatlineWindow)
	var recent []RunSummary
	movedBefore := false
	for _, r := range in.Runs {
		ok := r.Outcome == Completed || r.Outcome == CompletedWithErrors
		if !ok {
			continue
		}
		if r.FinishedAt.After(cut) {
			recent = append(recent, r)
		} else if r.Stats.BytesTransferred > 0 {
			movedBefore = true
		}
	}
	if len(recent) < 3 || !movedBefore {
		return false
	}
	if recent[len(recent)-1].FinishedAt.After(cut.Add(flatlineWindow / 10)) {
		return false
	}
	checked := false
	for _, r := range recent {
		if r.Stats.BytesTransferred > 0 || r.Stats.FilesTransferred > 0 {
			return false
		}
		checked = checked || r.Stats.FilesChecked > 0
	}
	return checked
}

func maxDur(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

func roughly(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	case d >= 2*time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	default:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	}
}
