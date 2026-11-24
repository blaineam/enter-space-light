package backup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/blaineam/enter-space-light/internal/rcl"
	"github.com/blaineam/enter-space-light/internal/settings"
	"github.com/blaineam/enter-space-light/internal/tune"
)

// Engine runs the backup-specific parts of a sync job and serves Time
// Portal. The sync itself is still an ordinary rclone sync/copy run by the
// job runner; the engine adds version history, a delete guard, a manifest
// per run, retention, and point-in-time browsing and restore.
type Engine struct {
	GOOS        string
	CacheDir    string // local copy of manifests (offline browsing; never authoritative)
	Resolve     tune.Resolver
	MountPoints func() []string // our own mounts, excluded from local sources
	Logf        func(format string, args ...any)
	Now         func() time.Time

	freshMu sync.Mutex
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Engine) logf(f string, a ...any) {
	if e.Logf != nil {
		e.Logf(f, a...)
	}
}

func (e *Engine) windows() bool { return e.GOOS == "windows" }

// Run is one backup run in progress.
type Run struct {
	Job          settings.Job
	RunID        string
	Started      time.Time
	SourceFs     string // native, {volume} expanded
	DestFs       string
	VersionsBase string // ".previous-versions/<namespace>" location
	BackupDirFs  string // this run's dated folder
	Rules        []string
	Params       map[string]any // extra rclone parameters for the sync call
}

// ErrGuard marks a run stopped to protect the backup.
type ErrGuard struct{ Reason string }

func (g ErrGuard) Error() string { return g.Reason }

func isLocal(fs string) bool { return tune.RemoteName(fs) == "" }

// VersionsBase is where a destination's dated version folders live: beside
// the destination at the top of the remote (as the Apple app), inside the
// bucket for bucket storage, and at the drive root or beside the folder for
// local destinations. rclone requires it to be outside the destination.
func (e *Engine) VersionsBase(destFs, volumeRoot, namespace string) string {
	if isLocal(destFs) {
		root := volumeRoot
		if root == "" {
			root = filepath.Dir(strings.TrimRight(destFs, `/\`))
		}
		return filepath.Join(root, VersionsRoot, namespace)
	}
	name, p := FsSplit(destFs)
	if tune.ProfileFor(tune.BackendOf(destFs, e.Resolve)) == tune.ProfileObject {
		bucket := strings.SplitN(strings.Trim(p, "/"), "/", 2)[0]
		return name + ":" + bucket + "/" + VersionsRoot + "/" + namespace
	}
	return name + ":" + VersionsRoot + "/" + namespace
}

func (e *Engine) join(base, leaf string) string {
	if isLocal(base) {
		return filepath.Join(base, leaf)
	}
	return FsJoin(base, leaf)
}

// Prepare readies a run: version folder, filters, delete guard. On a guard
// trip it returns the Run (so the abort is recorded) and an ErrGuard.
func (e *Engine) Prepare(ctx context.Context, j settings.Job, volumeRoot string) (*Run, error) {
	b := j.Backup
	r := &Run{Job: j, RunID: NewUUID(), Started: e.now(), Params: map[string]any{}}
	r.SourceFs = tune.Expand(j.Source, volumeRoot)
	r.DestFs = tune.Expand(j.Dest, volumeRoot)
	r.VersionsBase = e.VersionsBase(r.DestFs, volumeRoot, b.Namespace)
	r.BackupDirFs = e.join(r.VersionsBase, r.Started.Local().Format(RunDirFormat))
	r.Params["backup_dir"] = r.BackupDirFs

	// Our manifests must never be touched by the sync, whatever the user's
	// own rules say, so this rule comes first.
	r.Rules = []string{tune.AppStateRule}
	for _, f := range j.Filters {
		if f = strings.TrimSpace(f); f != "" && !strings.HasPrefix(f, "#") {
			r.Rules = append(r.Rules, f)
		}
	}
	var mps []string
	if e.MountPoints != nil {
		mps = e.MountPoints()
	}
	r.Rules = append(r.Rules, Rules(e.GOOS, b.ExcludeGroups, r.SourceFs, mps)...)

	if isLocal(r.SourceFs) && e.GOOS == "linux" {
		// Never wander into other filesystems (mounted drives, /proc
		// inside a chroot…); keep symlinks as links, as the Apple app does.
		r.Params["srcFs"] = ":local,one_file_system=true,links=true:" + r.SourceFs
	}

	if j.Kind == settings.KindSync && !b.AllowLargeDeletes {
		src, err := count(ctx, r.SourceFs, r.Rules)
		if err != nil {
			return r, ErrGuard{fmt.Sprintf("Couldn't read the source (%v), so the backup was skipped rather than risk deleting anything.", err)}
		}
		dst, err := count(ctx, r.DestFs, r.Rules)
		if err == nil { // unknown counts never trip the guard
			if src == 0 && dst > 0 {
				return r, ErrGuard{fmt.Sprintf("The source is empty but the backup holds %d files, so nothing was deleted. Is a drive disconnected or a folder moved?", dst)}
			}
			if dst >= 100 && float64(dst-src)/float64(dst) > 0.25 {
				return r, ErrGuard{fmt.Sprintf("This backup would delete %d of %d files (%d%%), so it was stopped to protect them. If that's what you want, turn on “Allow large deletions” for this backup and run it again.", dst-src, dst, 100*(dst-src)/dst)}
			}
			if dst >= 100 {
				md := dst / 4
				if md < 1 {
					md = 1
				}
				r.Params["max_delete"] = md
			}
		}
	}
	return r, nil
}

// Finish records the run's manifest (destination + local cache) and, after
// a success, applies retention. It never fails the run: problems are logged.
func (e *Engine) Finish(ctx context.Context, r *Run, outcome Outcome, reason string, st rcl.Stats) {
	if r == nil {
		return
	}
	b := r.Job.Backup
	m := &Manifest{FormatVersion: FormatVersion, RunID: r.RunID, SyncID: b.ID, SyncName: r.Job.Name,
		StartedAt: T(r.Started), FinishedAt: T(e.now()),
		SourceFs: PortableFs(r.SourceFs), DestinationFs: PortableFs(r.DestFs), Outcome: outcome,
		Stats: Stats{FilesTransferred: st.Transfers, FilesDeleted: st.Deletes, FilesChecked: st.Checks,
			BytesTransferred: st.Bytes, Errors: st.Errors},
		TreeCoverage: CoverageNone}
	if reason != "" {
		m.Stats.AbortReason = &reason
	}
	if outcome != Aborted {
		bd := PortableFs(r.BackupDirFs)
		m.BackupDirFs = &bd
		if v, _, err := listFiles(ctx, r.BackupDirFs, 0, versionedBudget); err == nil {
			m.VersionedEntries = v
		} else {
			e.logf("backup %q: listing this run's versions: %v", r.Job.Name, err)
		}
		e.recordLive(ctx, r, m, st)
	}
	data, err := Encode(m)
	if err != nil {
		e.logf("backup %q: encoding manifest: %v", r.Job.Name, err)
		return
	}
	e.cachePut(b.ID, r.RunID+ManifestExt, data)
	dir := e.join(r.DestFs, ManifestDir+"/"+b.ID)
	if err := writeFile(ctx, dir, r.RunID+ManifestExt, data); err != nil {
		e.logf("backup %q: saving the run record to the destination failed (kept locally): %v", r.Job.Name, err)
	}
	if outcome != Aborted {
		e.recoverDisplaced(ctx, r)
	}
	if (outcome == Completed || outcome == CompletedWithErrors) && b.RetentionDays > 0 {
		var live map[string]bool
		if m.TreeCoverage == CoverageFull {
			live = map[string]bool{}
			for _, en := range m.LiveEntries {
				live[en.Path] = true
			}
		}
		e.retain(ctx, r, live)
	}
}

// recoverDisplaced puts back run records that this run moved into its
// versions folder. The own-state rule keeps rclone off them, except with
// delete_excluded, which deletes excluded destination files regardless
// (rclone has no protect rule). Copies, never moves.
func (e *Engine) recoverDisplaced(ctx context.Context, r *Run) {
	rel := ManifestDir + "/" + r.Job.Backup.ID
	from := e.join(r.BackupDirFs, rel)
	names, err := listFileNames(ctx, from)
	if err != nil || len(names) == 0 {
		return
	}
	to := e.join(r.DestFs, rel)
	have, _ := listFileNames(ctx, to)
	present := map[string]bool{}
	for _, n := range have {
		present[n] = true
	}
	n := 0
	for _, name := range names {
		if present[name] || !strings.HasSuffix(name, ManifestExt) {
			continue
		}
		data, err := readFile(ctx, from, name)
		if err == nil {
			err = writeFile(ctx, to, name, data)
		}
		if err != nil {
			e.logf("backup %q: recovering run record %s: %v", r.Job.Name, name, err)
			continue
		}
		n++
	}
	if n > 0 {
		e.logf("backup %q: recovered %d run record(s) a run had moved into its versions folder", r.Job.Name, n)
	}
}

// recordLive lists the destination tree, or carries the previous tree
// forward when the run changed nothing and the last full look is recent.
func (e *Engine) recordLive(ctx context.Context, r *Run, m *Manifest, st rcl.Stats) {
	id := r.Job.Backup.ID
	prev := e.latestCached(id)
	unchanged := st.Transfers == 0 && st.Deletes == 0 && m.Outcome == Completed
	if prev != nil && unchanged && prev.TreeCoverage != CoverageNone && e.freshAge(id) < 24*time.Hour {
		m.LiveEntries, m.TreeCoverage = prev.LiveEntries, prev.TreeCoverage
		return
	}
	live, complete, err := listFiles(ctx, r.DestFs, maxLiveEntries, liveListBudget)
	if err != nil {
		e.logf("backup %q: listing the destination: %v", r.Job.Name, err)
		if prev != nil && prev.TreeCoverage != CoverageNone {
			m.LiveEntries, m.TreeCoverage = prev.LiveEntries, CoveragePartial
		}
		return
	}
	m.LiveEntries = live
	switch {
	case !complete:
		m.TreeCoverage = CoveragePartial
	case len(live) == 0 && prev != nil && len(prev.LiveEntries) > 0:
		m.TreeCoverage = CoveragePartial // an empty listing that used to have files proves nothing
	case m.Outcome != Completed && m.Outcome != CompletedWithErrors:
		m.TreeCoverage = CoveragePartial
	default:
		m.TreeCoverage = CoverageFull
	}
	e.markFresh(id)
}

func (e *Engine) retain(ctx context.Context, r *Run, live map[string]bool) {
	days := r.Job.Backup.RetentionDays
	names, err := listDirNames(ctx, r.VersionsBase)
	if err != nil {
		e.logf("backup %q: retention skipped: %v", r.Job.Name, err)
		return
	}
	cut := e.now().AddDate(0, 0, -days)
	var expired []Snapshot
	for _, n := range names {
		t, ok := ParseRunDir(n, time.Local)
		if !ok || !t.Before(cut) {
			continue
		}
		files, complete, err := listFiles(ctx, e.join(r.VersionsBase, n), 0, versionedBudget)
		if err != nil || !complete {
			continue // can't see it all: keep it
		}
		s := Snapshot{Name: n, At: t}
		for _, f := range files {
			s.Paths = append(s.Paths, f.Path)
		}
		expired = append(expired, s)
	}
	if len(expired) == 0 {
		return
	}
	if live == nil {
		entries, complete, err := listFiles(ctx, r.DestFs, 0, liveListBudget)
		if err != nil || !complete {
			e.logf("backup %q: retention skipped: couldn't list the whole backup", r.Job.Name)
			return
		}
		live = map[string]bool{}
		for _, en := range entries {
			live[en.Path] = true
		}
	}
	for _, n := range PlanRetention(expired, live) {
		if err := purge(ctx, e.join(r.VersionsBase, n)); err != nil {
			e.logf("backup %q: removing old versions %s: %v", r.Job.Name, n, err)
			continue
		}
		e.logf("backup %q: removed versions from %s (older than %d days)", r.Job.Name, n, days)
	}
}

// ---- local manifest cache ----

func (e *Engine) cacheDir(id string) string { return filepath.Join(e.CacheDir, strings.ToUpper(id)) }

func (e *Engine) cachePut(id, name string, data []byte) {
	if e.CacheDir == "" {
		return
	}
	d := e.cacheDir(id)
	if err := os.MkdirAll(d, 0o700); err == nil {
		_ = os.WriteFile(filepath.Join(d, name), data, 0o600)
	}
}

func (e *Engine) cached(id string) map[string][]byte {
	out := map[string][]byte{}
	if e.CacheDir == "" {
		return out
	}
	ents, _ := os.ReadDir(e.cacheDir(id))
	for _, en := range ents {
		if strings.HasSuffix(en.Name(), ManifestExt) {
			if b, err := os.ReadFile(filepath.Join(e.cacheDir(id), en.Name())); err == nil {
				out[en.Name()] = b
			}
		}
	}
	return out
}

func (e *Engine) latestCached(id string) *Manifest {
	var best *Manifest
	for _, b := range e.cached(id) {
		if m, err := Decode(b); err == nil && (best == nil || m.FinishedAt.After(best.FinishedAt.Time)) {
			best = m
		}
	}
	return best
}

func (e *Engine) freshFile(id string) string {
	return filepath.Join(e.cacheDir(id), "last-full-listing")
}

func (e *Engine) freshAge(id string) time.Duration {
	e.freshMu.Lock()
	defer e.freshMu.Unlock()
	b, err := os.ReadFile(e.freshFile(id))
	if err != nil {
		return 1 << 62
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(b)))
	if err != nil {
		return 1 << 62
	}
	return e.now().Sub(t)
}

func (e *Engine) markFresh(id string) {
	e.freshMu.Lock()
	defer e.freshMu.Unlock()
	if e.CacheDir == "" {
		return
	}
	_ = os.MkdirAll(e.cacheDir(id), 0o700)
	_ = os.WriteFile(e.freshFile(id), []byte(e.now().UTC().Format(time.RFC3339)+"\n"), 0o600)
}

// ---- Time Portal ----

// Ref is a backup found at a destination (ours or another device's).
type Ref struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Source   string    `json:"source"`
	Runs     int       `json:"runs"`
	LastRun  time.Time `json:"lastRun"`
	DestFs   string    `json:"destFs"`
	Offline  bool      `json:"offline,omitempty"` // from the local cache only
	Readable bool      `json:"readable"`
}

// manifests loads one backup's manifests: from the destination when it is
// reachable (caching new ones), else from the local cache.
func (e *Engine) manifests(ctx context.Context, destFs, id string) ([]*Manifest, bool, error) {
	dir := e.join(destFs, ManifestDir+"/"+strings.ToUpper(id))
	local := e.cached(id)
	offline := false
	names, err := listFileNames(ctx, dir)
	if err != nil {
		if len(local) == 0 {
			return nil, true, err
		}
		offline = true
	}
	for _, n := range names {
		if !strings.HasSuffix(n, ManifestExt) {
			continue
		}
		if _, ok := local[n]; ok {
			continue // run records are immutable
		}
		b, err := readFile(ctx, dir, n)
		if err != nil {
			e.logf("time portal: reading %s: %v", n, err)
			continue
		}
		e.cachePut(id, n, b)
		local[n] = b
	}
	var out []*Manifest
	for _, b := range local {
		m, err := Decode(b)
		if err != nil {
			continue
		}
		out = append(out, m)
	}
	return out, offline, nil
}

// Discover lists every backup recorded at destFs — including ones made by
// Enter Space on a Mac, iPhone or iPad.
func (e *Engine) Discover(ctx context.Context, destFs string) ([]Ref, error) {
	ids, err := listDirNames(ctx, e.join(destFs, ManifestDir))
	if err != nil {
		return nil, err
	}
	var out []Ref
	for _, id := range ids {
		if !ValidUUID(id) {
			continue
		}
		ms, offline, err := e.manifests(ctx, destFs, id)
		ref := Ref{ID: strings.ToUpper(id), DestFs: destFs, Offline: offline, Readable: err == nil && len(ms) > 0}
		for _, m := range ms {
			ref.Runs++
			if m.FinishedAt.After(ref.LastRun) {
				ref.LastRun, ref.Name, ref.Source = m.FinishedAt.Time, m.SyncName, m.SourceFs
			}
		}
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastRun.After(out[j].LastRun) })
	return out, nil
}

// Load builds the point-in-time index for one backup at destFs.
func (e *Engine) Load(ctx context.Context, destFs, id string) (*Index, bool, error) {
	ms, offline, err := e.manifests(ctx, destFs, id)
	if err != nil {
		return nil, offline, err
	}
	if len(ms) == 0 {
		return nil, offline, errors.New("no backups have been recorded here yet")
	}
	// Which dated version folders still exist (retention removes them).
	existing := map[string]map[string]bool{}
	if !offline {
		for _, m := range ms {
			if m.BackupDirFs == nil {
				continue
			}
			parent, _ := splitFile(*m.BackupDirFs)
			if _, done := existing[parent]; done {
				continue
			}
			names, err := listDirNames(ctx, NativeFs(parent, e.windows()))
			if err != nil {
				existing[parent] = nil // unknown: assume present
				continue
			}
			set := map[string]bool{}
			for _, n := range names {
				set[n] = true
			}
			existing[parent] = set
		}
	}
	gone := func(bd string) bool {
		parent, leaf := splitFile(bd)
		set := existing[parent]
		return set != nil && !set[leaf]
	}
	return NewIndex(ms, gone), offline, nil
}

// RestoreReport summarises a restore.
type RestoreReport struct {
	Total       int               `json:"total"`
	Done        int               `json:"done"`
	Bytes       int64             `json:"bytes"`
	Renamed     map[string]string `json:"renamed,omitempty"` // original → name used to keep both
	Failed      map[string]string `json:"failed,omitempty"`  // path → error
	Unavailable []Item            `json:"unavailable,omitempty"`
	Finished    bool              `json:"finished"`
	Target      string            `json:"target"`
}

// Restore copies paths as they were at asOf into root (a native folder or
// rclone path). With keepBoth, an existing different file is kept and the
// restored copy gets a "(restored <date>)" name.
func (e *Engine) Restore(ctx context.Context, ix *Index, asOf time.Time, paths []string, root string, keepBoth bool, progress func(RestoreReport)) RestoreReport {
	ok, unavailable := ix.Files(paths, asOf)
	rep := RestoreReport{Total: len(ok), Unavailable: unavailable, Renamed: map[string]string{}, Failed: map[string]string{}, Target: root}
	if progress != nil {
		progress(rep)
	}
	stamp := asOf.Local().Format("2006-01-02 15.04")
	for _, it := range ok {
		if ctx.Err() != nil {
			rep.Failed[it.Path] = "stopped"
			continue
		}
		rel := it.Path
		if keepBoth && exists(ctx, root, rel) {
			ext := path.Ext(rel)
			rel = strings.TrimSuffix(rel, ext) + " (restored " + stamp + ")" + ext
			rep.Renamed[it.Path] = rel
		}
		if err := copyFile(ctx, NativeFs(it.ContentFs, e.windows()), root, rel, it.Size); err != nil {
			rep.Failed[it.Path] = err.Error()
		} else {
			rep.Bytes += it.Size
		}
		rep.Done++
		if progress != nil {
			progress(rep)
		}
	}
	rep.Finished = true
	if progress != nil {
		progress(rep)
	}
	return rep
}

// LocalRuns are a backup's runs from the local manifest cache, newest
// first. Cheap enough to call on every status refresh.
func (e *Engine) LocalRuns(id string) []RunSummary {
	var out []RunSummary
	written := map[string]time.Time{} // run id → when the local record was written
	for name, b := range e.cached(id) {
		m, err := Decode(b)
		if err != nil {
			continue
		}
		if st, err := os.Stat(filepath.Join(e.cacheDir(id), name)); err == nil {
			written[m.RunID] = st.ModTime()
		}
		bd := ""
		if m.BackupDirFs != nil {
			bd = *m.BackupDirFs
		}
		out = append(out, RunSummary{RunID: m.RunID, StartedAt: m.StartedAt.Time, FinishedAt: m.FinishedAt.Time,
			Outcome: m.Outcome, Coverage: m.TreeCoverage, BackupDirFs: bd, Stats: m.Stats})
	}
	// Run records keep whole seconds (the Apple format requires it), so two
	// runs can finish in the same second; the local write time breaks the tie.
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].FinishedAt.Equal(out[j].FinishedAt) {
			return out[i].FinishedAt.After(out[j].FinishedAt)
		}
		return written[out[i].RunID].After(written[out[j].RunID])
	})
	return out
}

// Health evaluates a backup job against the watchdog rules.
func (e *Engine) Health(j settings.Job) *Alert {
	if j.Backup == nil || !j.Enabled {
		return nil
	}
	in := HealthInput{Runs: e.LocalRuns(j.Backup.ID), Created: j.Backup.Created, Now: e.now(),
		OnDrive: j.Triggers.OnVolume != nil}
	if d, err := time.ParseDuration(j.Triggers.Every); err == nil {
		in.Interval = d
	}
	if j.Triggers.Daily != "" && (in.Interval == 0 || in.Interval > 24*time.Hour) {
		in.Interval = 24 * time.Hour
	}
	return Evaluate(in)
}
