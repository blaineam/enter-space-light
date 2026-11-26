package android

import "testing"

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
