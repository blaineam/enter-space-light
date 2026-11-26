package daemon

import (
	"encoding/json"
	"net/http"
	"os"
	"runtime"

	"github.com/blaineam/enter-space-light/internal/android"
	"github.com/blaineam/enter-space-light/internal/androidfs"
	"github.com/blaineam/enter-space-light/internal/mounts"
	"github.com/blaineam/enter-space-light/internal/settings"
	"github.com/blaineam/enter-space-light/internal/tune"
)

// androidStatus is the extra part of the UI model on Android.
type androidStatus struct {
	Permissions json.RawMessage `json:"permissions"`
	Storage     string          `json:"storage"` // shared storage root
}

func androidStorage() string {
	if s := os.Getenv("EXTERNAL_STORAGE_ROOT"); s != "" {
		return s
	}
	return "/storage/emulated/0"
}

func androidDeviceName() string {
	if s := os.Getenv("ESL_DEVICE_NAME"); s != "" {
		return s
	}
	return "Android"
}

func androidSnapshot() *androidStatus {
	h := android.Current()
	if runtime.GOOS != "android" || h == nil {
		return nil
	}
	p := json.RawMessage(h.PermissionsJSON())
	if !json.Valid(p) {
		p = json.RawMessage("[]")
	}
	return &androidStatus{Permissions: p, Storage: androidStorage()}
}

func (d *Daemon) androidPermission(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := readJSON(r, &req); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	h := android.Current()
	if h == nil {
		httpErr(w, http.StatusBadRequest, "only the Android app has these permissions")
		return
	}
	h.RequestPermission(req.ID)
	writeJSON(w, map[string]bool{"ok": true})
}

// needsNetwork reports whether a job reads or writes a remote.
func needsNetwork(j settings.Job) bool {
	return tune.RemoteName(j.Source) != "" || tune.RemoteName(j.Dest) != ""
}

// hold keeps background syncs off mobile data and, if asked, off battery.
// Only the Android app reports power state, so desktops are never held.
func (d *Daemon) hold(j settings.Job) string {
	p, known := android.CurrentPower()
	if !known {
		return ""
	}
	prefs := d.store.Get().Prefs
	return android.Hold(p, needsNetwork(j), prefs.SyncOnMobileData, prefs.SyncOnlyCharging)
}

// Changed tells the engine that power, network, permissions or storage
// volumes changed.
func (d *Daemon) Changed() {
	d.sched.PauseHeld()
	d.sched.Tick()
	d.mounts.Kick()
	d.hub.notify()
}

// Tick runs the scheduler now; the app calls it from an alarm, because
// Android stops timers while the phone sleeps.
func (d *Daemon) Tick() {
	d.sched.Tick()
	d.mounts.Kick()
}

// Activity is what keeps the phone awake and what the notification says.
type Activity struct {
	Drives  int // connected
	Running int // syncs
	Uploads int // files waiting to reach a remote
}

// Activity reports what the engine is doing right now.
func (d *Daemon) Activity() Activity {
	a := Activity{Uploads: androidfs.PendingUploads()}
	for _, s := range d.mounts.Snapshot() {
		if s.Status == "mounted" {
			a.Drives++
		}
	}
	for _, s := range d.runner.Snapshot() {
		if s.Running != nil {
			a.Running++
		}
	}
	return a
}

// DriveNames maps connected drive IDs to their names and read-only flags.
func (d *Daemon) DriveNames() []settings.Mount {
	doc := d.store.Get()
	connected := map[string]bool{}
	for _, id := range androidfs.Connected() {
		connected[id] = true
	}
	out := []settings.Mount{}
	for _, m := range doc.Mounts {
		if connected[m.ID] {
			out = append(out, m)
		}
	}
	return out
}

// Subscribe delivers a ping whenever something the app shows has changed.
func (d *Daemon) Subscribe() (<-chan struct{}, func()) { return d.hub.subscribe() }

// MountStates is every configured drive with whether the user wants it
// connected and whether it is.
func (d *Daemon) MountStates() ([]settings.Mount, []mounts.State) {
	doc := d.store.Get()
	return doc.Mounts, d.mounts.Snapshot()
}

// LoginURL is a one-time link that signs the app's screen in.
func (d *Daemon) LoginURL() string { return "http://" + d.host + "/login?code=" + d.newCode() }

// SetNotice shows a banner in the UI.
func (d *Daemon) SetNotice(s string) { d.notice.Store(s) }
