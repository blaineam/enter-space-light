package volumes

import "testing"

func TestPollReportsArrivalsAndRemovals(t *testing.T) {
	present := []Volume{{ID: "AAAA-1111", Root: `E:\`, Label: "USB"}}
	w := NewWatcher(func() ([]Volume, error) { return present, nil }, 0)

	arrived, removed, _ := w.Poll()
	if len(arrived) != 1 || len(removed) != 0 {
		t.Fatalf("first poll must report present drives as arrived: %v %v", arrived, removed)
	}
	arrived, removed, _ = w.Poll()
	if len(arrived) != 0 || len(removed) != 0 {
		t.Fatalf("nothing changed: %v %v", arrived, removed)
	}
	// Same drive comes back on a different letter: identity is the serial.
	present = []Volume{{ID: "aaaa-1111", Root: `F:\`, Label: "USB"}}
	arrived, removed, _ = w.Poll()
	if len(arrived) != 0 || len(removed) != 0 {
		t.Fatalf("a letter change is not a new drive: %v %v", arrived, removed)
	}
	present = nil
	_, removed, _ = w.Poll()
	if len(removed) != 1 {
		t.Fatalf("unplug not reported: %v", removed)
	}
}

func TestMatches(t *testing.T) {
	v := Volume{ID: "ABCD-1234", Label: "Backup"}
	if !v.Matches("abcd-1234", "") || v.Matches("FFFF-0000", "Backup") {
		t.Fatal("ID match must win and be case-insensitive")
	}
	if !v.Matches("", "backup") || v.Matches("", "") {
		t.Fatal("label fallback")
	}
}
