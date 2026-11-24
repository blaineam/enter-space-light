package backup

import (
	"bytes"
	"compress/flate"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// The on-destination format is shared with Enter Space for iPhone, iPad and
// Mac (EnterSpaceShared/BackupManifest.swift), so a backup made here can be
// browsed there and vice versa. Change nothing here without changing both.

// FormatVersion is the manifest schema version. Readers reject newer ones.
const FormatVersion = 1

// ManifestDir is where manifests live inside the destination.
const ManifestDir = ".enterspace/manifests"

// ManifestExt is the manifest file extension (raw DEFLATE of JSON).
const ManifestExt = ".json.deflate"

// VersionsRoot holds the dated folders of replaced/deleted files.
const VersionsRoot = ".previous-versions"

// RunDirFormat names a run's versions folder (local time, like the Apple app).
const RunDirFormat = "2006-01-02_15-04-05"

// Presence of an entry's content.
type Presence string

// Presences.
const (
	Present             Presence = "present"
	PrunedByRetention   Presence = "prunedByRetention"
	MissingUnexpectedly Presence = "missingUnexpectedly"
)

// Outcome of a run.
type Outcome string

// Outcomes.
const (
	Completed           Outcome = "completed"
	CompletedWithErrors Outcome = "completedWithErrors"
	Failed              Outcome = "failed"
	Aborted             Outcome = "aborted"
)

// Coverage says how complete a manifest's live tree is.
type Coverage string

// Coverages.
const (
	CoverageFull    Coverage = "full"
	CoveragePartial Coverage = "partial"
	CoverageNone    Coverage = "none"
)

// Time encodes as ISO-8601 with whole seconds in UTC — the only form
// Swift's .iso8601 decoder accepts (fractional seconds are rejected).
type Time struct{ time.Time }

// T wraps a time.
func T(t time.Time) Time { return Time{t} }

// MarshalJSON writes 2026-08-03T02:00:00Z.
func (t Time) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.UTC().Truncate(time.Second).Format("2006-01-02T15:04:05Z"))
}

// UnmarshalJSON accepts any RFC 3339 form.
func (t *Time) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	p, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return err
	}
	t.Time = p
	return nil
}

// Entry is one file in a listing.
type Entry struct {
	Path     string   `json:"path"` // relative, "/"-separated, no leading/trailing "/"
	Size     int64    `json:"size"`
	ModTime  Time     `json:"modTime"`
	Hash     *string  `json:"hash,omitempty"`
	HashKind *string  `json:"hashKind,omitempty"`
	Presence Presence `json:"presence"`
}

// Stats of a run.
type Stats struct {
	FilesTransferred int64   `json:"filesTransferred"`
	FilesDeleted     int64   `json:"filesDeleted"`
	FilesChecked     int64   `json:"filesChecked"`
	BytesTransferred int64   `json:"bytesTransferred"`
	Errors           int64   `json:"errors"`
	AbortReason      *string `json:"abortReason,omitempty"`
}

// Manifest records one backup run.
type Manifest struct {
	FormatVersion    int      `json:"formatVersion"`
	RunID            string   `json:"runID"`
	SyncID           string   `json:"syncID"`
	SyncName         string   `json:"syncName"`
	StartedAt        Time     `json:"startedAt"`
	FinishedAt       Time     `json:"finishedAt"`
	SourceFs         string   `json:"sourceFs"`
	DestinationFs    string   `json:"destinationFs"`
	BackupDirFs      *string  `json:"backupDirFs,omitempty"`
	Outcome          Outcome  `json:"outcome"`
	Stats            Stats    `json:"stats"`
	VersionedEntries []Entry  `json:"versionedEntries"`
	LiveEntries      []Entry  `json:"liveEntries"`
	TreeCoverage     Coverage `json:"treeCoverage"`
}

// NewUUID returns an uppercase RFC 4122 v4 UUID, as Swift's uuidString.
func NewUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return strings.ToUpper(fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]))
}

// ValidUUID reports whether s parses as a UUID (Swift requires it).
func ValidUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
				return false
			}
		}
	}
	return true
}

// Encode returns the raw-DEFLATE JSON the Apple app writes.
func Encode(m *Manifest) ([]byte, error) {
	if m.VersionedEntries == nil {
		m.VersionedEntries = []Entry{}
	}
	if m.LiveEntries == nil {
		m.LiveEntries = []Entry{}
	}
	js, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(js); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ErrNewerFormat means a manifest from a newer app version.
var ErrNewerFormat = errors.New("manifest written by a newer version")

// Decode reads raw DEFLATE or plain JSON.
func Decode(b []byte) (*Manifest, error) {
	raw := b
	if t := bytes.TrimSpace(b); len(t) == 0 || t[0] != '{' {
		r := flate.NewReader(bytes.NewReader(b))
		inflated, err := io.ReadAll(io.LimitReader(r, 1<<30))
		_ = r.Close()
		if err != nil {
			return nil, fmt.Errorf("manifest is neither DEFLATE nor JSON: %w", err)
		}
		raw = inflated
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if m.FormatVersion > FormatVersion {
		return nil, ErrNewerFormat
	}
	return &m, nil
}

// ManifestPath is the file's path inside the destination.
func ManifestPath(syncID, runID string) string {
	return ManifestDir + "/" + strings.ToUpper(syncID) + "/" + strings.ToUpper(runID) + ManifestExt
}

// IsControlPath reports files that belong to the backup machinery, never to
// the user's data (Apple: BackupControlFiles.isOurs).
func IsControlPath(p string) bool {
	for _, part := range strings.Split(p, "/") {
		if part == ".enterspace" || strings.HasPrefix(part, ".pbsdb") || part == ".pbson" {
			return true
		}
	}
	return false
}

// Fs strings ("remote:path" or "/local/path"), as BackupFs in Swift.

// FsSplit returns (remote, path); remote "" means local.
func FsSplit(fs string) (string, string) {
	if strings.HasPrefix(fs, "/") {
		return "", fs
	}
	i := strings.Index(fs, ":")
	if i < 0 {
		return "", fs
	}
	return fs[:i], fs[i+1:]
}

// FsJoin appends a tree-relative path to an fs string.
func FsJoin(fs, rel string) string {
	rel = strings.Trim(rel, "/")
	if rel == "" {
		return fs
	}
	base := strings.TrimSuffix(fs, "/")
	if strings.HasSuffix(base, ":") {
		return base + rel
	}
	return base + "/" + rel
}

// PortableFs writes a local Windows path so the Apple app reads it as
// local ("/C:/Users/me") instead of a remote named "C".
func PortableFs(fs string) string {
	if len(fs) >= 2 && fs[1] == ':' && ((fs[0] >= 'A' && fs[0] <= 'Z') || (fs[0] >= 'a' && fs[0] <= 'z')) {
		return "/" + strings.ReplaceAll(fs, `\`, "/")
	}
	return fs
}

// NativeFs reverses PortableFs for rclone on this machine.
func NativeFs(fs string, windows bool) string {
	if windows && len(fs) >= 3 && fs[0] == '/' && fs[2] == ':' {
		return strings.ReplaceAll(fs[1:], "/", `\`)
	}
	return fs
}

// Namespace turns a destination path into the versions folder name, as
// the Apple app does: "Backup/Home" → "Backup-Home".
func Namespace(destPath, fallback string) string {
	n := strings.Trim(strings.TrimSpace(destPath), "/")
	n = strings.NewReplacer("/", "-", `\`, "-", ":", "-").Replace(n)
	if n == "" {
		n = strings.TrimSpace(fallback)
	}
	if n == "" {
		n = "backup"
	}
	return n
}
