// Package mobile is the Go engine's face to the Android app, bound with
// gomobile (scripts/build-android.sh). It uses only types gomobile can
// carry: strings, numbers, byte slices, errors and the Host interface.
//
// The engine is the same daemon the desktop runs, with the same settings
// screens served on 127.0.0.1 and shown in the app's WebView.
package mobile

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	// The phone's zone is applied by name; carry the database in case the
	// system copy can't be read.
	_ "time/tzdata"

	"github.com/blaineam/enter-space-light/internal/android"
	"github.com/blaineam/enter-space-light/internal/androidfs"
	"github.com/blaineam/enter-space-light/internal/daemon"
	"github.com/blaineam/enter-space-light/internal/mounts"
	"github.com/blaineam/enter-space-light/internal/paths"
	"github.com/blaineam/enter-space-light/internal/version"
	"github.com/blaineam/enter-space-light/internal/volumes"
)

// Host is implemented by the Android app. See internal/android.Host, which
// has the same methods.
type Host interface {
	OpenURL(url string) bool
	OpenDrive(id string) bool
	Notify(title, message string)
	StartsOnBoot() bool
	SetStartOnBoot(on bool) bool
	LoadSecret() string
	SaveSecret(value string) bool
	PermissionsJSON() string
	RequestPermission(id string)
	DrivesChanged()
	Stopped()
}

var _ android.Host = Host(nil)

var (
	mu      sync.Mutex
	engine  *daemon.Daemon
	stopped chan struct{} // closed when the running engine has shut down
)

var errNotRunning = errors.New("Enter Space Light isn't running")

func current() (*daemon.Daemon, error) {
	mu.Lock()
	defer mu.Unlock()
	if engine == nil {
		return nil, errNotRunning
	}
	return engine, nil
}

// SetHost registers the app. Call it before Start.
func SetHost(h Host) { android.SetHost(h) }

// Version is the app's version.
func Version() string { return version.Version }

// Start runs the engine, doing nothing if it already runs. filesDir and
// cacheDir are the app's private directories, storageRoot is shared storage
// (/storage/emulated/0), and timezone is the phone's zone ID: Go can't find
// it on Android by itself, and daily schedules would run on UTC.
func Start(filesDir, cacheDir, storageRoot, deviceName, timezone string) error {
	mu.Lock()
	defer mu.Unlock()
	if engine != nil {
		return nil
	}
	if stopped != nil {
		<-stopped // a previous engine is still shutting down
	}
	if loc, err := time.LoadLocation(timezone); err == nil && timezone != "" {
		time.Local = loc
		android.SetZone(loc)
	}
	tmp := filepath.Join(cacheDir, "tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return err
	}
	for k, v := range map[string]string{
		// Android's defaults (/data/local/tmp, /) aren't writable by apps.
		"TMPDIR": tmp, "HOME": filesDir, "XDG_CACHE_HOME": cacheDir, "XDG_CONFIG_HOME": filesDir,
		"EXTERNAL_STORAGE_ROOT": storageRoot, "ESL_DEVICE_NAME": deviceName,
	} {
		if err := os.Setenv(k, v); err != nil {
			return err
		}
	}
	paths.SetBase(filepath.Join(filesDir, "config"), filepath.Join(filesDir, "state"), filepath.Join(cacheDir, "vfs"))
	androidfs.Register()

	d, err := daemon.Start(daemon.Options{})
	if err != nil {
		return err
	}
	engine = d
	done := make(chan struct{})
	stopped = done

	changes, unsubscribe := d.Subscribe()
	quit := make(chan struct{})
	go watchDrives(d, changes, quit)
	go func() {
		d.Wait()
		close(quit)
		unsubscribe()
		close(done)
		mu.Lock()
		if engine == d {
			engine = nil
		}
		mu.Unlock()
		if h := android.Current(); h != nil {
			h.Stopped()
		}
	}()
	return nil
}

// watchDrives tells the app when the drive list changes, and only then:
// sync progress also pings, several times a second.
func watchDrives(d *daemon.Daemon, changes <-chan struct{}, quit <-chan struct{}) {
	last := ""
	for {
		select {
		case <-quit:
			return
		case <-changes:
		}
		if now := drivesJSON(d); now != last {
			last = now
			if h := android.Current(); h != nil {
				h.DrivesChanged()
			}
		}
	}
}

// Stop shuts the engine down and waits for it, flushing nothing that can't
// resume: pending uploads stay in the cache and continue next time.
func Stop() {
	mu.Lock()
	d, done := engine, stopped
	mu.Unlock()
	if d == nil {
		return
	}
	d.Quit()
	<-done
}

// Running reports whether the engine is up.
func Running() bool {
	_, err := current()
	return err == nil
}

// LoginURL is a one-time link for the app's WebView.
func LoginURL() string {
	d, err := current()
	if err != nil {
		return ""
	}
	return d.LoginURL()
}

// SetTimezone follows the phone to another zone, for daily schedules.
func SetTimezone(id string) {
	if loc, err := time.LoadLocation(id); err == nil && id != "" {
		android.SetZone(loc)
		Changed()
	}
}

// SetPower reports network and charging state.
func SetPower(online, unmetered, charging bool) {
	android.SetPower(android.Power{Online: online, Unmetered: unmetered, Charging: charging})
	Changed()
}

// SetVolumes replaces the storage volume list:
// [{"id","label","root","removable","system"}].
func SetVolumes(list string) error {
	var v []volumes.Volume
	if err := json.Unmarshal([]byte(list), &v); err != nil {
		return err
	}
	volumes.SetAndroid(v)
	return nil
}

// Changed asks the engine to re-check schedules, drives and permissions.
func Changed() {
	if d, err := current(); err == nil {
		d.Changed()
	}
}

// Tick runs due schedules; call it from an alarm.
func Tick() {
	if d, err := current(); err == nil {
		d.Tick()
	}
}

// ActivityJSON is {"drives","running","uploads","openFiles"}.
func ActivityJSON() string {
	d, err := current()
	if err != nil {
		return `{"drives":0,"running":0,"uploads":0,"openFiles":0}`
	}
	a := d.Activity()
	return toJSON(map[string]int{"drives": a.Drives, "running": a.Running, "uploads": a.Uploads, "openFiles": androidfs.OpenCount()})
}

// Drive is one root in the Files app.
type drive struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Remote    string `json:"remote"`
	ReadOnly  bool   `json:"readOnly"`
	Connected bool   `json:"connected"`
}

func drivesJSON(d *daemon.Daemon) string {
	cfg, states := d.MountStates()
	byID := map[string]mounts.State{}
	for _, s := range states {
		byID[s.ID] = s
	}
	out := []drive{}
	for _, m := range cfg {
		s := byID[m.ID]
		// A drive that is connecting is listed too, so it doesn't vanish
		// from a file picker while the network comes back.
		if s.Want || s.Status == mounts.StatusMounted {
			out = append(out, drive{m.ID, m.Name, m.Remote, m.ReadOnly, s.Status == mounts.StatusMounted})
		}
	}
	return toJSON(out)
}

// DrivesJSON is [{"id","name","remote","readOnly","connected"}] for every
// drive that is connected or connecting.
func DrivesJSON() string {
	d, err := current()
	if err != nil {
		return "[]"
	}
	return drivesJSON(d)
}

// WaitConnected waits up to timeoutMillis for a drive to be ready, for the
// moment after the app is started by another app opening a file.
func WaitConnected(driveID string, timeoutMillis int) bool {
	deadline := time.Now().Add(time.Duration(timeoutMillis) * time.Millisecond)
	for {
		for _, id := range androidfs.Connected() {
			if id == driveID {
				return true
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func toJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

// List returns a folder: [{"name","path","dir","size","modified"}].
func List(driveID, dir string) (string, error) {
	e, err := androidfs.List(driveID, dir)
	if err != nil {
		return "", err
	}
	return toJSON(e), nil
}

// Stat returns one entry.
func Stat(driveID, path string) (string, error) {
	e, err := androidfs.Stat(driveID, path)
	if err != nil {
		return "", err
	}
	return toJSON(e), nil
}

// SpaceJSON is {"total","free"} in bytes, -1 where the remote doesn't say.
func SpaceJSON(driveID string) string {
	total, free, err := androidfs.Space(driveID)
	if err != nil {
		total, free = -1, -1
	}
	return toJSON(map[string]int64{"total": total, "free": free})
}

// Create makes a file or folder without replacing anything; returns its path.
func Create(driveID, dir, name string, folder bool) (string, error) {
	return androidfs.Create(driveID, dir, name, folder)
}

// Delete removes a file or a folder with its contents.
func Delete(driveID, path string) error { return androidfs.Delete(driveID, path) }

// Rename returns the new path.
func Rename(driveID, path, name string) (string, error) { return androidfs.Rename(driveID, path, name) }

// Move returns the new path.
func Move(driveID, path, newDir string) (string, error) {
	return androidfs.Move(driveID, path, newDir)
}

// Open returns a file handle. mode is Android's: r, w, wt, wa, rw, rwt.
func Open(driveID, path, mode string) (int64, error) { return androidfs.Open(driveID, path, mode) }

// Size of an open file.
func Size(handle int64) (int64, error) { return androidfs.Size(handle) }

// ReadAt fills buf from offset. The slice is shared with Java, not copied.
func ReadAt(handle int64, offset int64, buf []byte, length int) (int, error) {
	if length < 0 || length > len(buf) {
		length = len(buf)
	}
	return androidfs.ReadAt(handle, buf[:length], offset)
}

// WriteAt writes the first length bytes of buf at offset.
func WriteAt(handle int64, offset int64, buf []byte, length int) (int, error) {
	if length < 0 || length > len(buf) {
		return 0, errors.New("bad length")
	}
	return androidfs.WriteAt(handle, buf[:length], offset)
}

// Sync flushes an open file.
func Sync(handle int64) error { return androidfs.Sync(handle) }

// CloseFile closes a handle; changes upload in the background.
func CloseFile(handle int64) error { return androidfs.Close(handle) }
