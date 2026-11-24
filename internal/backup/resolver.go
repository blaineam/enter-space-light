package backup

import (
	"sort"
	"strings"
	"time"
)

// Point-in-time reconstruction, ported rule for rule from Enter Space's
// PointInTimeResolver.swift so both apps give the same answer for the same
// backup.
//
// rclone's --backup-dir is a REVERSE-DELTA store: the live destination holds
// the current state, and each dated folder holds only what that run
// replaced or deleted. For path P at instant T:
//
//  1. The versioned entry from the EARLIEST run after T is the content at T.
//  2. Otherwise the live copy is, if it existed at T (modTime <= T).
//  3. Otherwise P is known-but-unavailable, never silently dropped.

// State of a resolved item at T.
type State string

// States.
const (
	UnchangedSince    State = "unchangedSince"
	ModifiedSince     State = "modifiedSince"
	DeletedSince      State = "deletedSince"
	PrunedUnavailable State = "prunedUnavailable"
	Unknown           State = "unknown"
)

// Confidence of a resolved item.
type Confidence string

// Confidences.
const (
	Authoritative Confidence = "authoritative"
	Inferred      Confidence = "inferred"
	Uncertain     Confidence = "uncertain"
)

// Item is one reconstructed file or folder at T.
type Item struct {
	Path       string     `json:"path"`
	Name       string     `json:"name"`
	IsDir      bool       `json:"isDir"`
	Size       int64      `json:"size"`
	ModTime    time.Time  `json:"modTime"`
	State      State      `json:"state"`
	Confidence Confidence `json:"confidence"`
	// ContentFs is the rclone path holding the bytes ("" when unavailable).
	ContentFs string `json:"contentFs,omitempty"`
	// FromRun is the run whose listing or versions folder supplied it.
	FromRun string `json:"fromRun,omitempty"`
}

type rowKind int

const (
	kindLive rowKind = iota
	kindVersioned
)

type row struct {
	kind       rowKind
	runID      string
	capturedAt time.Time
	size       int64
	modTime    time.Time
	presence   Presence
	contentFs  string
}

// RunSummary is the per-run part of a manifest.
type RunSummary struct {
	RunID       string    `json:"runId"`
	StartedAt   time.Time `json:"startedAt"`
	FinishedAt  time.Time `json:"finishedAt"`
	Outcome     Outcome   `json:"outcome"`
	Coverage    Coverage  `json:"coverage"`
	BackupDirFs string    `json:"backupDirFs,omitempty"`
	Stats       Stats     `json:"stats"`
}

// Index is one backup's history, built from its manifests. It is the Go
// counterpart of the Apple app's catalog, but always rebuilt from the
// destination, so it can never disagree with it.
type Index struct {
	SyncID   string
	Name     string
	runs     []RunSummary     // newest first
	rows     map[string][]row // path → rows, newest capturedAt first
	children map[string]map[string]bool
}

// Prunable lets the index learn which versions folders retention removed
// (the Apple app keeps that only in its local catalog). nil = assume present.
type Prunable func(backupDirFs string) bool

// NewIndex builds an index from one sync's manifests.
func NewIndex(manifests []*Manifest, gone Prunable) *Index {
	ix := &Index{rows: map[string][]row{}, children: map[string]map[string]bool{}}
	manifests = append([]*Manifest(nil), manifests...) // never reorder the caller's slice
	sort.Slice(manifests, func(i, j int) bool { return manifests[i].FinishedAt.After(manifests[j].FinishedAt.Time) })
	for _, m := range manifests {
		if ix.SyncID == "" {
			ix.SyncID, ix.Name = m.SyncID, m.SyncName
		}
		bd := ""
		if m.BackupDirFs != nil {
			bd = *m.BackupDirFs
		}
		ix.runs = append(ix.runs, RunSummary{RunID: m.RunID, StartedAt: m.StartedAt.Time, FinishedAt: m.FinishedAt.Time,
			Outcome: m.Outcome, Coverage: m.TreeCoverage, BackupDirFs: bd, Stats: m.Stats})
		prunedDir := bd != "" && gone != nil && gone(bd)
		for _, e := range m.VersionedEntries {
			p := e.Presence
			if prunedDir && p == Present {
				p = PrunedByRetention
			}
			ix.add(e.Path, row{kind: kindVersioned, runID: m.RunID, capturedAt: m.FinishedAt.Time, size: e.Size,
				modTime: e.ModTime.Time, presence: p, contentFs: FsJoin(bd, e.Path)})
		}
		for _, e := range m.LiveEntries {
			ix.add(e.Path, row{kind: kindLive, runID: m.RunID, capturedAt: m.FinishedAt.Time, size: e.Size,
				modTime: e.ModTime.Time, presence: e.Presence, contentFs: FsJoin(m.DestinationFs, e.Path)})
		}
	}
	for p := range ix.rows {
		rs := ix.rows[p]
		sort.SliceStable(rs, func(i, j int) bool { return rs[i].capturedAt.After(rs[j].capturedAt) })
	}
	return ix
}

func (ix *Index) add(p string, r row) {
	p = strings.Trim(p, "/")
	if p == "" || IsControlPath(p) {
		return
	}
	ix.rows[p] = append(ix.rows[p], r)
	for {
		parent := parentOf(p)
		if ix.children[parent] == nil {
			ix.children[parent] = map[string]bool{}
		}
		if ix.children[parent][p] {
			return
		}
		ix.children[parent][p] = true
		if parent == "" {
			return
		}
		p = parent
	}
}

func parentOf(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return ""
}

func lastComponent(p string) string { return p[strings.LastIndex(p, "/")+1:] }

// Runs are this backup's runs, newest first.
func (ix *Index) Runs() []RunSummary { return append([]RunSummary(nil), ix.runs...) }

// Timeline is the distinct instants the user can pick (run finish times),
// newest first.
func (ix *Index) Timeline() []time.Time {
	var out []time.Time
	seen := map[int64]bool{}
	for _, r := range ix.runs {
		if r.Coverage == CoverageNone && r.Outcome != Completed {
			continue
		}
		k := r.FinishedAt.Unix()
		if !seen[k] {
			seen[k] = true
			out = append(out, r.FinishedAt)
		}
	}
	return out
}

func (ix *Index) isDir(p string) bool { _, ok := ix.children[p]; return ok }

type window struct {
	runs           []RunSummary
	newestObserved *RunSummary
	newestFull     *RunSummary
	sound          bool
	note           string
}

func (ix *Index) window(asOf time.Time) window {
	w := window{runs: ix.runs}
	for i := range ix.runs {
		if w.newestObserved == nil && ix.runs[i].Coverage != CoverageNone {
			w.newestObserved = &ix.runs[i]
		}
		if w.newestFull == nil && ix.runs[i].Coverage == CoverageFull {
			w.newestFull = &ix.runs[i]
		}
	}
	if len(ix.runs) == 0 {
		w.note = "There are no backups to look through yet."
		return w
	}
	oldest := ix.runs[len(ix.runs)-1]
	historyOff, partial := false, false
	for _, r := range ix.runs {
		if r.FinishedAt.After(asOf) {
			historyOff = historyOff || r.BackupDirFs == ""
			partial = partial || r.Coverage != CoverageFull
		}
	}
	startsAfter := oldest.StartedAt.After(asOf)
	w.sound = !historyOff && !partial && !startsAfter
	switch {
	case startsAfter:
		w.note = "Backup history only goes back to " + oldest.StartedAt.Local().Format("Jan 2, 2006 3:04 PM") + ". Anything from before then is not recorded."
	case historyOff:
		w.note = "Version history was off for part of this period, so files changed while it was off can't be recovered as they were."
	case partial:
		w.note = "At least one backup in this period didn't finish listing the destination, so this view may be missing files."
	}
	return w
}

func (ix *Index) resolve(p string, asOf time.Time, w window) *Item {
	rows := ix.rows[p]
	name := lastComponent(p)

	// RULE 1: the earliest version displaced after T is the content at T.
	var displaced *row
	for i := len(rows) - 1; i >= 0; i-- { // oldest → newest
		if rows[i].kind == kindVersioned && rows[i].capturedAt.After(asOf) {
			displaced = &rows[i]
			break
		}
	}
	if displaced != nil {
		if displaced.presence != Present {
			return &Item{Path: p, Name: name, Size: displaced.size, ModTime: displaced.modTime, State: PrunedUnavailable, Confidence: Authoritative}
		}
		seenLiveAfter := false
		for _, r := range rows {
			if r.kind == kindLive && !r.capturedAt.Before(displaced.capturedAt) {
				seenLiveAfter = true
				break
			}
		}
		provenGone := false
		if !seenLiveAfter {
			for _, r := range w.runs {
				if r.Coverage == CoverageFull && (r.RunID == displaced.runID || !r.FinishedAt.Before(displaced.capturedAt)) {
					provenGone = true
					break
				}
			}
		}
		st, conf := ModifiedSince, Uncertain
		if provenGone {
			st = DeletedSince
		}
		if seenLiveAfter || provenGone {
			conf = Authoritative
		}
		return &Item{Path: p, Name: name, Size: displaced.size, ModTime: displaced.modTime, State: st, Confidence: conf, ContentFs: displaced.contentFs, FromRun: displaced.runID}
	}

	// RULE 2: the live copy, if it existed at T and is still current.
	var live *row
	for i := range rows {
		if rows[i].kind == kindLive {
			live = &rows[i]
			break
		}
	}
	contradicted := live != nil && w.newestFull != nil && live.capturedAt.Before(w.newestFull.FinishedAt)
	if live != nil && !contradicted {
		if live.modTime.After(asOf) {
			return nil // written after T, nothing older captured: didn't exist at T
		}
		if live.presence != Present {
			return &Item{Path: p, Name: name, Size: live.size, ModTime: live.modTime, State: PrunedUnavailable, Confidence: Authoritative}
		}
		confirmed := w.newestObserved != nil && !live.capturedAt.Before(w.newestObserved.FinishedAt)
		conf := Uncertain
		if confirmed && w.sound {
			conf = Inferred
		}
		return &Item{Path: p, Name: name, Size: live.size, ModTime: live.modTime, State: UnchangedSince, Confidence: conf, ContentFs: live.contentFs, FromRun: live.runID}
	}

	// RULE 3: it left the live tree without a version after T.
	if live != nil {
		for _, r := range w.runs {
			if r.Coverage == CoverageFull && !r.FinishedAt.After(asOf) && r.FinishedAt.After(live.capturedAt) {
				return nil // a full listing saw it gone before T
			}
		}
		if live.modTime.After(asOf) {
			return nil
		}
		return &Item{Path: p, Name: name, Size: live.size, ModTime: live.modTime, State: Unknown, Confidence: Uncertain}
	}
	if len(rows) > 0 {
		return &Item{Path: p, Name: name, Size: rows[0].size, ModTime: rows[0].modTime, State: Unknown, Confidence: Uncertain}
	}
	return nil
}

const dirProbeBudget = 256

func (ix *Index) resolveDir(p string, asOf time.Time, w window) *Item {
	queue := []string{p}
	visited := map[string]bool{}
	examined := 0
	var newest time.Time
	sawDeleted, sawUnavailable, exhausted := false, false, false
	item := func(st State, c Confidence) *Item {
		mt := newest
		if mt.IsZero() {
			mt = asOf
		}
		return &Item{Path: p, Name: lastComponent(p), IsDir: true, ModTime: mt, State: st, Confidence: c}
	}
probe:
	for len(queue) > 0 {
		d := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if visited[d] {
			continue
		}
		visited[d] = true
		for _, c := range sortedKeys(ix.children[d]) {
			if ix.isDir(c) && len(ix.rows[c]) == 0 {
				queue = append(queue, c)
				continue
			}
			examined++
			if it := ix.resolve(c, asOf, w); it != nil {
				if it.ModTime.After(newest) {
					newest = it.ModTime
				}
				switch it.State {
				case UnchangedSince, ModifiedSince:
					c := Uncertain
					if w.sound {
						c = Inferred
					}
					return item(UnchangedSince, c)
				case DeletedSince:
					sawDeleted = true
				default:
					sawUnavailable = true
				}
			}
			if examined >= dirProbeBudget {
				exhausted = true
				break probe
			}
		}
	}
	switch {
	case sawDeleted:
		return item(DeletedSince, Authoritative)
	case sawUnavailable:
		return item(PrunedUnavailable, Authoritative)
	case exhausted:
		return item(Unknown, Uncertain)
	}
	return nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Listing is a folder as it was at T.
type Listing struct {
	AsOf  time.Time `json:"asOf"`
	Path  string    `json:"path"`
	Items []Item    `json:"items"`
	Note  string    `json:"note,omitempty"` // coverage caveat to show the user
}

// List returns the children of dir as they were at asOf.
func (ix *Index) List(dir string, asOf time.Time) Listing {
	dir = strings.Trim(dir, "/")
	w := ix.window(asOf)
	out := Listing{AsOf: asOf, Path: dir, Note: w.note, Items: []Item{}}
	for _, c := range sortedKeys(ix.children[dir]) {
		var it *Item
		if ix.isDir(c) && len(ix.rows[c]) == 0 {
			it = ix.resolveDir(c, asOf, w)
		} else {
			it = ix.resolve(c, asOf, w)
		}
		if it != nil {
			out.Items = append(out.Items, *it)
		}
	}
	sort.SliceStable(out.Items, func(i, j int) bool {
		if out.Items[i].IsDir != out.Items[j].IsDir {
			return out.Items[i].IsDir
		}
		return strings.ToLower(out.Items[i].Name) < strings.ToLower(out.Items[j].Name)
	})
	return out
}

// Files expands paths (files or folders) into every file that existed at
// asOf, for a restore plan.
func (ix *Index) Files(paths []string, asOf time.Time) (ok []Item, unavailable []Item) {
	w := ix.window(asOf)
	seen := map[string]bool{}
	var walk func(p string)
	walk = func(p string) {
		if seen[p] {
			return
		}
		seen[p] = true
		if ix.isDir(p) && len(ix.rows[p]) == 0 {
			for _, c := range sortedKeys(ix.children[p]) {
				walk(c)
			}
			return
		}
		it := ix.resolve(p, asOf, w)
		if it == nil {
			return
		}
		if it.ContentFs == "" {
			unavailable = append(unavailable, *it)
		} else {
			ok = append(ok, *it)
		}
	}
	for _, p := range paths {
		walk(strings.Trim(p, "/"))
	}
	return ok, unavailable
}
