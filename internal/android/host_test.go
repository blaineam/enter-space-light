package android

import (
	"testing"
	"time"
)

func TestNowFollowsTheZone(t *testing.T) {
	defer zone.Store(nil)
	for _, offset := range []int{19800, -21600} {
		SetZone(time.FixedZone("z", offset))
		if _, got := Now().Zone(); got != offset {
			t.Fatalf("offset %d, want %d", got, offset)
		}
	}
}

func TestHold(t *testing.T) {
	mobile := Power{Online: true}
	wifi := Power{Online: true, Unmetered: true}
	for _, c := range []struct {
		name                              string
		p                                 Power
		network, allowMetered, chargeOnly bool
		want                              string
	}{
		{"wifi runs", wifi, true, false, false, ""},
		{"mobile data waits by default", mobile, true, false, false, "waiting for Wi-Fi"},
		{"mobile data when allowed", mobile, true, true, false, ""},
		{"offline", Power{}, true, true, false, "waiting for a network"},
		{"local sync ignores the network", Power{}, false, false, false, ""},
		{"charger required", wifi, true, false, true, "waiting for the charger"},
		{"charging", Power{Online: true, Unmetered: true, Charging: true}, true, false, true, ""},
	} {
		if got := Hold(c.p, c.network, c.allowMetered, c.chargeOnly); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
