// Package android is the contract between the Go engine and the Android app
// around it. The app implements Host (through gomobile) and registers it
// before the engine starts. Nothing here is Android-specific, so it builds
// and is tested everywhere.
package android

import "sync"

// Host is what the engine needs from the app. Every method uses only types
// gomobile can bind.
type Host interface {
	// OpenURL shows a web page in the browser (cloud account sign-in).
	OpenURL(url string) bool
	// OpenDrive shows a connected drive in the Files app.
	OpenDrive(id string) bool
	// Notify posts a notification.
	Notify(title, message string)
	// StartsOnBoot and SetStartOnBoot read and change "start when the
	// phone starts".
	StartsOnBoot() bool
	SetStartOnBoot(on bool) bool
	// LoadSecret and SaveSecret keep the rclone.conf password; saving ""
	// removes it.
	LoadSecret() string
	SaveSecret(value string) bool
	// PermissionsJSON is [{"id","title","why","granted","required"}].
	PermissionsJSON() string
	// RequestPermission opens the system screen that grants id.
	RequestPermission(id string)
	// DrivesChanged means the list of drives, or whether they are
	// connected, changed; the Files app should reload.
	DrivesChanged()
	// Stopped means the engine shut down (the user chose Quit).
	Stopped()
}

var (
	mu    sync.RWMutex
	host  Host
	power Power
	known bool
)

// SetHost registers the app.
func SetHost(h Host) {
	mu.Lock()
	defer mu.Unlock()
	host = h
}

// Current returns the registered app, or nil.
func Current() Host {
	mu.RLock()
	defer mu.RUnlock()
	return host
}

// Power is what decides whether background syncs may run now.
type Power struct {
	Online    bool // any network
	Unmetered bool // Wi-Fi or Ethernet that isn't marked metered
	Charging  bool
}

// SetPower records the latest network and charging state.
func SetPower(p Power) {
	mu.Lock()
	defer mu.Unlock()
	power, known = p, true
}

// CurrentPower returns the last reported state and whether any was reported.
func CurrentPower() (Power, bool) {
	mu.RLock()
	defer mu.RUnlock()
	return power, known
}

// Hold says why a background sync must wait, or "" if it may run.
// needsNetwork is false for a sync between two places on the phone or its
// drives. allowMetered and chargingOnly are the user's settings.
func Hold(p Power, needsNetwork, allowMetered, chargingOnly bool) string {
	switch {
	case needsNetwork && !p.Online:
		return "waiting for a network"
	case needsNetwork && !allowMetered && !p.Unmetered:
		return "waiting for Wi-Fi"
	case chargingOnly && !p.Charging:
		return "waiting for the charger"
	}
	return ""
}
