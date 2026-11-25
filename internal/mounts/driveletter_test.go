package mounts

import "testing"

func TestPickLetterSkipsTakenAndFailedLetters(t *testing.T) {
	taken := map[byte]bool{'Z': true, 'X': true}
	failed := map[byte]bool{}
	pick := func() string {
		got, err := pickLetter(func(l byte) bool { return taken[l] }, failed)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := pick(); got != "Y:" {
		t.Fatalf("Z: is taken (a disconnected network drive): got %s", got)
	}
	failed['Y'] = true
	if got := pick(); got != "W:" {
		t.Fatalf("after Y: fails, move on: got %s", got)
	}
}

func TestPickLetterStartsOverWhenEveryFreeLetterFailed(t *testing.T) {
	failed := map[byte]bool{'E': true, 'D': true}
	onlyDE := func(l byte) bool { return l > 'E' }
	got, err := pickLetter(onlyDE, failed)
	if err != nil || got != "E:" || len(failed) != 0 {
		t.Fatalf("got %q %v, failed=%v", got, err, failed)
	}
	if _, err := pickLetter(func(byte) bool { return true }, map[byte]bool{}); err == nil {
		t.Fatal("no free letter must be an error")
	}
}
