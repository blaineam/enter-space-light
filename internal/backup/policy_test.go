package backup

import (
	"testing"
	"time"
)

func TestRetentionNeverDeletesTheLastCopy(t *testing.T) {
	live := map[string]bool{"a.txt": true}
	expired := []Snapshot{
		{Name: "old", At: at(1), Paths: []string{"gone.txt", "a.txt"}}, // older copy of gone.txt
		{Name: "newer", At: at(2), Paths: []string{"gone.txt"}},        // newest copy of a deleted file
		{Name: "dup", At: at(3), Paths: []string{"a.txt"}},             // a.txt still live
	}
	got := PlanRetention(expired, live)
	want := map[string]bool{"dup": true, "old": true}
	if len(got) != 2 || !want[got[0]] || !want[got[1]] {
		t.Fatalf("prune %v: keep 'newer' (last copy of gone.txt), drop the rest", got)
	}
}

func TestRetentionKeepsEverythingWhenNothingSurvives(t *testing.T) {
	got := PlanRetention([]Snapshot{{Name: "only", At: at(1), Paths: []string{"x"}}}, map[string]bool{})
	if len(got) != 0 {
		t.Fatalf("pruned the only copy: %v", got)
	}
}

func TestParseRunDir(t *testing.T) {
	for _, n := range []string{"2026-11-23_02-00-00", "20261123-020000", "2026-11-23"} {
		if _, ok := ParseRunDir(n, time.Local); !ok {
			t.Errorf("%s should parse", n)
		}
	}
	if _, ok := ParseRunDir("my-stuff", time.Local); ok {
		t.Error("unparseable names must never be pruned")
	}
}

func rs(d int, o Outcome, bytes int64) RunSummary {
	return RunSummary{RunID: "r", FinishedAt: at(d), Outcome: o, Stats: Stats{BytesTransferred: bytes, FilesChecked: 10}}
}

func TestHealthRules(t *testing.T) {
	daily := 24 * time.Hour
	cases := []struct {
		name string
		in   HealthInput
		want string
	}{
		{"healthy", HealthInput{Runs: []RunSummary{rs(10, Completed, 5)}, Interval: daily, Now: at(10).Add(time.Hour)}, ""},
		{"never ran", HealthInput{Created: at(0), Interval: daily, Now: at(3)}, "neverRan"},
		{"new is fine", HealthInput{Created: at(0), Interval: daily, Now: at(1)}, ""},
		{"repeated failure", HealthInput{Runs: []RunSummary{rs(5, Failed, 0), rs(4, Failed, 0), rs(3, CompletedWithErrors, 0), rs(2, Completed, 1)}, Interval: daily, Now: at(5)}, "repeatedFailure"},
		{"guard", HealthInput{Runs: []RunSummary{rs(5, Aborted, 0), rs(4, Completed, 1)}, Interval: daily, Now: at(5)}, "guardAborted"},
		{"missed", HealthInput{Runs: []RunSummary{rs(1, Completed, 1)}, Interval: daily, Now: at(5)}, "missedSchedule"},
		{"drive waits a month", HealthInput{Runs: []RunSummary{rs(1, Completed, 1)}, OnDrive: true, Now: at(20)}, ""},
	}
	for _, c := range cases {
		a := Evaluate(c.in)
		got := ""
		if a != nil {
			got = a.Kind
		}
		if got != c.want {
			t.Errorf("%s: got %q want %q (%+v)", c.name, got, c.want, a)
		}
	}
}

func TestFlatline(t *testing.T) {
	now := at(40)
	runs := []RunSummary{rs(39, Completed, 0), rs(30, Completed, 0), rs(20, Completed, 0), rs(11, Completed, 0), rs(5, Completed, 900)}
	if a := Evaluate(HealthInput{Runs: runs, Interval: 7 * 24 * time.Hour, Now: now}); a == nil || a.Kind != "flatline" {
		t.Fatalf("want flatline, got %+v", a)
	}
	runs[0].Stats.BytesTransferred = 1
	if a := Evaluate(HealthInput{Runs: runs, Interval: 7 * 24 * time.Hour, Now: now}); a != nil {
		t.Fatalf("one run moved data: %+v", a)
	}
}
