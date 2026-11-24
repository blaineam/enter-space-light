// Package settings is the user's mounts, sync jobs and preferences, stored
// as settings.json next to (not inside) rclone.conf.
package settings

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// CurrentVersion is the settings schema version.
const CurrentVersion = 1

// Settings is the whole file.
type Settings struct {
	Version int     `json:"version"`
	Mounts  []Mount `json:"mounts"`
	Jobs    []Job   `json:"jobs"`
	Prefs   Prefs   `json:"prefs"`
}

// Mount is one remote shown as a drive (Windows) or folder (Linux).
type Mount struct {
	ID   string `json:"id"`
	Name string `json:"name"` // label shown in Explorer / the file manager
	// Remote is an rclone path: "gdrive:", "gdrive:Photos", "nas:/srv".
	Remote string `json:"remote"`
	// MountPoint: "" = automatic (next free drive letter on Windows,
	// <mountRoot>/<Name> on Linux). "X:" or a folder path otherwise.
	MountPoint string `json:"mountPoint"`
	AutoMount  bool   `json:"autoMount"`
	ReadOnly   bool   `json:"readOnly"`
	// Options are raw rclone option overrides by CLI name without "--"
	// (e.g. "vfs_cache_mode": "writes"). They win over automatic tuning.
	Options map[string]string `json:"options,omitempty"`
}

// JobKind is the rclone operation a sync job runs.
type JobKind string

// Supported job kinds.
const (
	KindSync   JobKind = "sync"   // make Dest identical to Source (deletes extras)
	KindCopy   JobKind = "copy"   // copy new/changed files, never delete
	KindMove   JobKind = "move"   // copy then delete from Source
	KindBisync JobKind = "bisync" // two-way
)

// VolumeMatch selects a removable drive by stable identity, never by drive
// letter or mount path (both change between plugs).
type VolumeMatch struct {
	ID    string `json:"id,omitempty"`    // volume serial (Windows) or filesystem UUID (Linux)
	Label string `json:"label,omitempty"` // fallback when ID is empty
}

// Triggers decide when a job runs. Any combination is allowed; manual runs
// are always possible.
type Triggers struct {
	OnStart  bool         `json:"onStart,omitempty"`  // shortly after the daemon starts (login)
	Every    string       `json:"every,omitempty"`    // Go duration, e.g. "30m", "6h"
	Daily    string       `json:"daily,omitempty"`    // "HH:MM" local time; a missed run fires on wake
	OnVolume *VolumeMatch `json:"onVolume,omitempty"` // when this drive is connected
}

// Job is a scheduled sync.
type Job struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Kind    JobKind `json:"kind"`
	Enabled bool    `json:"enabled"`
	// Source and Dest are rclone paths or local paths. "{volume}" expands to
	// the root of the drive that triggered the job (e.g. "{volume}/Backup").
	Source   string            `json:"source"`
	Dest     string            `json:"dest"`
	Triggers Triggers          `json:"triggers"`
	Filters  []string          `json:"filters,omitempty"` // rclone filter rules: "+ /Photos/**", "- *.tmp"
	Options  map[string]string `json:"options,omitempty"` // raw rclone option overrides
	// Backup turns the job into a versioned backup browsable in Time Portal.
	Backup *BackupOptions `json:"backup,omitempty"`
}

// BackupOptions make a sync or copy job keep version history and run
// manifests in the format Enter Space uses on Apple platforms.
type BackupOptions struct {
	// ID is the backup's identity on the destination (an uppercase UUID,
	// the Apple app's sync ID). Fixed at creation.
	ID string `json:"id"`
	// Template the wizard used: "home", "folder", "drive" or "custom".
	Template string `json:"template,omitempty"`
	// Namespace names the versions folder (.previous-versions/<namespace>).
	Namespace string `json:"namespace"`
	// RetentionDays keeps replaced and deleted files this long; 0 = forever.
	RetentionDays int `json:"retentionDays"`
	// ExcludeGroups are the wizard's "skip caches…" choices (backup.Groups).
	ExcludeGroups []string `json:"excludeGroups,omitempty"`
	// AllowLargeDeletes turns off the guard that stops a run which would
	// delete more than a quarter of the backup (a disconnected drive…).
	AllowLargeDeletes bool `json:"allowLargeDeletes,omitempty"`
	// Created is when the backup was set up (for "never finished" alerts).
	Created time.Time `json:"created,omitzero"`
}

// Prefs are global preferences.
type Prefs struct {
	// MountRoot is where automatic Linux mount points go. "" = ~/Cloud.
	MountRoot string `json:"mountRoot,omitempty"`
	// CacheMaxSize caps the VFS cache per mount ("" = automatic from free disk).
	CacheMaxSize string `json:"cacheMaxSize,omitempty"`
	// BWLimit is an rclone bandwidth limit or timetable ("" = unlimited),
	// e.g. "08:00,2M 23:00,off".
	BWLimit string `json:"bwLimit,omitempty"`
	// Bookmarks adds Linux mounts to the GTK file manager sidebar.
	Bookmarks *bool `json:"bookmarks,omitempty"`
	// LogLevel: "ERROR", "NOTICE" (default), "INFO", "DEBUG".
	LogLevel string `json:"logLevel,omitempty"`
}

// BookmarksEnabled defaults to true.
func (p Prefs) BookmarksEnabled() bool { return p.Bookmarks == nil || *p.Bookmarks }

// NewID returns a short random identifier.
func NewID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

var (
	dailyRe  = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)
	letterRe = regexp.MustCompile(`^[A-Za-z]:\\?$`)
	optKeyRe = regexp.MustCompile(`^[a-z0-9_]+$`)
	uuidRe   = regexp.MustCompile(`^[0-9A-F]{8}-[0-9A-F]{4}-[0-9A-F]{4}-[0-9A-F]{4}-[0-9A-F]{12}$`)
)

// IsDriveLetter reports whether s is "X:" or "X:\".
func IsDriveLetter(s string) bool { return letterRe.MatchString(s) }

// Validate checks the whole document, returning the first problem found.
func (s *Settings) Validate() error {
	ids := map[string]bool{}
	points := map[string]string{}
	for i := range s.Mounts {
		m := &s.Mounts[i]
		if m.ID == "" {
			return fmt.Errorf("mount %d has no id", i)
		}
		if ids[m.ID] {
			return fmt.Errorf("duplicate id %q", m.ID)
		}
		ids[m.ID] = true
		if strings.TrimSpace(m.Name) == "" {
			return fmt.Errorf("mount %q needs a name", m.ID)
		}
		if strings.ContainsAny(m.Name, `/\:*?"<>|`) {
			return fmt.Errorf("mount name %q cannot contain / \\ : * ? \" < > |", m.Name)
		}
		if !strings.Contains(m.Remote, ":") && !filepath.IsAbs(m.Remote) {
			return fmt.Errorf("mount %q: remote %q must look like name: or name:path", m.Name, m.Remote)
		}
		if m.MountPoint != "" {
			key := strings.ToUpper(strings.TrimSuffix(m.MountPoint, `\`))
			if other, dup := points[key]; dup {
				return fmt.Errorf("mounts %q and %q use the same mount point %s", other, m.Name, m.MountPoint)
			}
			points[key] = m.Name
		}
		if err := checkOptions(m.Options); err != nil {
			return fmt.Errorf("mount %q: %w", m.Name, err)
		}
	}
	for i := range s.Jobs {
		j := &s.Jobs[i]
		if j.ID == "" {
			return fmt.Errorf("job %d has no id", i)
		}
		if ids[j.ID] {
			return fmt.Errorf("duplicate id %q", j.ID)
		}
		ids[j.ID] = true
		if strings.TrimSpace(j.Name) == "" {
			return fmt.Errorf("sync job %q needs a name", j.ID)
		}
		switch j.Kind {
		case KindSync, KindCopy, KindMove, KindBisync:
		default:
			return fmt.Errorf("sync job %q: unknown kind %q", j.Name, j.Kind)
		}
		if strings.TrimSpace(j.Source) == "" || strings.TrimSpace(j.Dest) == "" {
			return fmt.Errorf("sync job %q needs a source and a destination", j.Name)
		}
		if j.Source == j.Dest {
			return fmt.Errorf("sync job %q: source and destination are the same", j.Name)
		}
		usesVolume := strings.Contains(j.Source, "{volume}") || strings.Contains(j.Dest, "{volume}")
		if usesVolume && j.Triggers.OnVolume == nil {
			return fmt.Errorf("sync job %q uses {volume} but has no drive trigger", j.Name)
		}
		if usesVolume && (j.Triggers.Every != "" || j.Triggers.Daily != "" || j.Triggers.OnStart) {
			return fmt.Errorf("sync job %q uses {volume}, so it can only run when the drive connects", j.Name)
		}
		if t := j.Triggers.Every; t != "" {
			d, err := time.ParseDuration(t)
			if err != nil {
				return fmt.Errorf("sync job %q: interval %q: %w", j.Name, t, err)
			}
			if d < time.Minute {
				return fmt.Errorf("sync job %q: interval must be at least 1m", j.Name)
			}
		}
		if t := j.Triggers.Daily; t != "" && !dailyRe.MatchString(t) {
			return fmt.Errorf("sync job %q: daily time %q must be HH:MM", j.Name, t)
		}
		if v := j.Triggers.OnVolume; v != nil && v.ID == "" && v.Label == "" {
			return fmt.Errorf("sync job %q: drive trigger needs a drive", j.Name)
		}
		for _, f := range j.Filters {
			f = strings.TrimSpace(f)
			if f == "" || strings.HasPrefix(f, "#") {
				continue
			}
			if !(strings.HasPrefix(f, "+ ") || strings.HasPrefix(f, "- ")) {
				return fmt.Errorf("sync job %q: filter %q must start with \"+ \" or \"- \"", j.Name, f)
			}
		}
		if err := checkOptions(j.Options); err != nil {
			return fmt.Errorf("sync job %q: %w", j.Name, err)
		}
		if b := j.Backup; b != nil {
			if j.Kind != KindSync && j.Kind != KindCopy {
				return fmt.Errorf("backup %q must be a mirror or copy", j.Name)
			}
			if !uuidRe.MatchString(b.ID) {
				return fmt.Errorf("backup %q has an invalid id", j.Name)
			}
			if b.Namespace == "" || strings.ContainsAny(b.Namespace, `/\:`) {
				return fmt.Errorf("backup %q has an invalid versions folder name", j.Name)
			}
			if b.RetentionDays < 0 {
				return fmt.Errorf("backup %q: retention can't be negative", j.Name)
			}
			if i := strings.Index(j.Dest, ":"); i > 1 && strings.Trim(j.Dest[i+1:], "/") == "" {
				return fmt.Errorf("backup %q: choose a folder on %s, not the top of the remote (old versions are kept beside it)", j.Name, j.Dest)
			}
		}
	}
	return nil
}

func checkOptions(o map[string]string) error {
	for k := range o {
		if !optKeyRe.MatchString(k) {
			return fmt.Errorf("option %q: use rclone's option name without -- and with _ (e.g. vfs_cache_mode)", k)
		}
	}
	return nil
}

// Store loads and saves settings.json atomically and serialises access.
type Store struct {
	path string
	mu   sync.RWMutex
	cur  Settings
}

// Open loads path, creating an empty document if it does not exist.
func Open(path string) (*Store, error) {
	s := &Store{path: path}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		s.cur = Settings{Version: CurrentVersion}
		return s, nil
	case err != nil:
		return nil, err
	}
	var doc Settings
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %w", path, err)
	}
	if doc.Version > CurrentVersion {
		return nil, fmt.Errorf("%s was written by a newer Enter Space Light (schema %d)", path, doc.Version)
	}
	doc.Version = CurrentVersion
	if err := doc.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	s.cur = doc
	return s, nil
}

// Get returns a deep copy of the current settings.
func (s *Store) Get() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return clone(s.cur)
}

// Update applies fn to a copy, validates, saves, and only then publishes.
func (s *Store) Update(fn func(*Settings) error) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := clone(s.cur)
	if err := fn(&next); err != nil {
		return Settings{}, err
	}
	next.Version = CurrentVersion
	if err := next.Validate(); err != nil {
		return Settings{}, err
	}
	if err := writeAtomic(s.path, next); err != nil {
		return Settings{}, err
	}
	s.cur = next
	return clone(next), nil
}

func clone(in Settings) Settings {
	b, _ := json.Marshal(in)
	var out Settings
	_ = json.Unmarshal(b, &out)
	if out.Mounts == nil {
		out.Mounts = []Mount{}
	}
	if out.Jobs == nil {
		out.Jobs = []Job{}
	}
	return out
}

// WriteJSONAtomic writes v as indented JSON via a temp file and rename, so
// a crash or power cut never leaves a half-written file behind.
func WriteJSONAtomic(path string, v any) error { return writeAtomic(path, v) }

func writeAtomic(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, werr := tmp.Write(append(b, '\n'))
	serr := tmp.Sync()
	cerr := tmp.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

// FindMount returns the mount with id.
func (s *Settings) FindMount(id string) (Mount, bool) {
	for _, m := range s.Mounts {
		if m.ID == id {
			return m, true
		}
	}
	return Mount{}, false
}

// FindJob returns the job with id.
func (s *Settings) FindJob(id string) (Job, bool) {
	for _, j := range s.Jobs {
		if j.ID == id {
			return j, true
		}
	}
	return Job{}, false
}
