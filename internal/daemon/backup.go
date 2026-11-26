package daemon

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blaineam/enter-space-light/internal/backup"
	"github.com/blaineam/enter-space-light/internal/desktop"
	"github.com/blaineam/enter-space-light/internal/mounts"
	"github.com/blaineam/enter-space-light/internal/paths"
	"github.com/blaineam/enter-space-light/internal/rcl"
	"github.com/blaineam/enter-space-light/internal/settings"
	"github.com/blaineam/enter-space-light/internal/tune"
	"github.com/blaineam/enter-space-light/internal/volumes"
)

// portal caches point-in-time indexes and tracks the one running restore.
type portal struct {
	mu      sync.Mutex
	indexes map[string]portalIndex
	restore *backup.RestoreReport
	cancel  context.CancelFunc
	alerted map[string]string // job id → alert kind last notified
}

type portalIndex struct {
	ix      *backup.Index
	offline bool
	at      time.Time
}

func (d *Daemon) newEngine() *backup.Engine {
	return &backup.Engine{
		GOOS: runtime.GOOS, CacheDir: filepath.Join(paths.StateDir(), "manifests"), Resolve: rcl.Backend, Logf: logf,
		MountPoints: func() []string {
			var out []string
			for _, s := range d.mounts.Snapshot() {
				if s.Status == mounts.StatusMounted && s.MountPoint != "" {
					out = append(out, s.MountPoint)
				}
			}
			return out
		},
	}
}

// fillBackup assigns a new backup its identity, and keeps an existing
// backup's identity across edits (it names its history on the destination).
func (d *Daemon) fillBackup(j *settings.Job, existing *settings.Job) {
	if j.Backup == nil {
		return
	}
	if existing != nil && existing.Backup != nil {
		j.Backup.ID, j.Backup.Namespace, j.Backup.Created = existing.Backup.ID, existing.Backup.Namespace, existing.Backup.Created
		return
	}
	j.Backup.ID = backup.NewUUID()
	j.Backup.Created = time.Now()
	_, p := backup.FsSplit(strings.ReplaceAll(j.Dest, "{volume}", ""))
	if tune.RemoteName(j.Dest) == "" {
		p = filepath.Base(strings.TrimRight(strings.ReplaceAll(j.Dest, "{volume}", ""), `/\`))
	}
	ns := backup.Namespace(p, j.Name)
	// Two backups to the same place must not share a versions folder.
	for _, other := range d.store.Get().Jobs {
		if other.Backup != nil && other.Backup.Namespace == ns && tune.RemoteName(other.Dest) == tune.RemoteName(j.Dest) {
			ns += "-" + j.Backup.ID[:8]
			break
		}
	}
	j.Backup.Namespace = ns
}

// backupHealth checks every backup and notifies once per new problem.
func (d *Daemon) backupHealth(doc settings.Settings) map[string]*backup.Alert {
	out := map[string]*backup.Alert{}
	d.portal.mu.Lock()
	defer d.portal.mu.Unlock()
	for _, j := range doc.Jobs {
		a := d.engine.Health(j)
		out[j.ID] = a
		kind := ""
		if a != nil {
			kind = a.Kind
		}
		if prev, seen := d.portal.alerted[j.ID]; kind != "" && (!seen || prev != kind) {
			desktop.Notify("Backup needs attention: "+j.Name, a.Message)
		}
		d.portal.alerted[j.ID] = kind
	}
	return out
}

// ---- API ----

type setupInfo struct {
	OS       string                `json:"os"`
	Home     string                `json:"home"`
	Computer string                `json:"computer"`
	Groups   []backup.ExcludeGroup `json:"groups"`
	Remotes  []remoteView          `json:"remotes"`
	Volumes  []volumes.Volume      `json:"volumes"`
	Folders  []string              `json:"folders"` // common folders that exist under home
}

func (d *Daemon) backupSetup(w http.ResponseWriter, _ *http.Request) {
	home, _ := os.UserHomeDir()
	if paths.Sandboxed() {
		home = filepath.Join(paths.Home(), "home") // never offer the real home folder from a sandbox
		_ = os.MkdirAll(home, 0o700)
	}
	host, _ := os.Hostname()
	host = strings.SplitN(host, ".", 2)[0] // "desk.lan" → "desk"
	folders := []string{"Documents", "Desktop", "Pictures", "Music", "Videos", "Downloads"}
	if runtime.GOOS == "android" {
		home, host = androidStorage(), androidDeviceName()
		folders = []string{"DCIM", "Pictures", "Documents", "Download", "Movies", "Music", "Recordings"}
	}
	info := setupInfo{OS: runtime.GOOS, Home: home, Computer: host, Groups: backup.Groups(runtime.GOOS), Volumes: d.vols.Current(), Remotes: d.snapshot().Config.Remotes}
	if info.Volumes == nil {
		info.Volumes = []volumes.Volume{}
	}
	for _, f := range folders {
		if st, err := os.Stat(filepath.Join(home, f)); err == nil && st.IsDir() {
			info.Folders = append(info.Folders, filepath.Join(home, f))
		}
	}
	writeJSON(w, info)
}

// portalTarget resolves ?job=<id> (one of ours) or ?fs=<dest>&id=<uuid>
// (any backup at a destination, e.g. one made by a Mac).
func (d *Daemon) portalTarget(r *http.Request) (destFs, id string, job *settings.Job, err error) {
	q := r.URL.Query()
	if jid := q.Get("job"); jid != "" {
		doc := d.store.Get()
		j, ok := doc.FindJob(jid)
		if !ok || j.Backup == nil {
			return "", "", nil, errors.New("no such backup")
		}
		dest := j.Dest
		if strings.Contains(dest, "{volume}") {
			root := d.volumeRoot(j)
			if root == "" {
				return "", "", nil, errors.New("connect this backup's drive to browse it")
			}
			dest = tune.Expand(dest, root)
		}
		return dest, j.Backup.ID, &j, nil
	}
	destFs, id = q.Get("fs"), strings.ToUpper(q.Get("id"))
	if destFs == "" || !backup.ValidUUID(id) {
		return "", "", nil, errors.New("choose a backup")
	}
	return destFs, id, nil, nil
}

func (d *Daemon) volumeRoot(j settings.Job) string {
	if v := j.Triggers.OnVolume; v != nil {
		for _, vol := range d.vols.Current() {
			if vol.Matches(v.ID, v.Label) {
				return vol.Root
			}
		}
	}
	return ""
}

func (d *Daemon) index(ctx context.Context, destFs, id string, fresh bool) (*backup.Index, bool, error) {
	key := destFs + "|" + id
	d.portal.mu.Lock()
	c, ok := d.portal.indexes[key]
	d.portal.mu.Unlock()
	if ok && !fresh && time.Since(c.at) < 2*time.Minute {
		return c.ix, c.offline, nil
	}
	ix, offline, err := d.engine.Load(ctx, destFs, id)
	if err != nil {
		return nil, offline, err
	}
	d.portal.mu.Lock()
	d.portal.indexes[key] = portalIndex{ix: ix, offline: offline, at: time.Now()}
	d.portal.mu.Unlock()
	return ix, offline, nil
}

// portalDiscover lists the backups found at ?fs=.
func (d *Daemon) portalDiscover(w http.ResponseWriter, r *http.Request) {
	fsName := r.URL.Query().Get("fs")
	if fsName == "" {
		httpErr(w, http.StatusBadRequest, "fs is required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	refs, err := d.engine.Discover(ctx, fsName)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if refs == nil {
		refs = []backup.Ref{}
	}
	writeJSON(w, refs)
}

func (d *Daemon) portalTimeline(w http.ResponseWriter, r *http.Request) {
	destFs, id, _, err := d.portalTarget(r)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	ix, offline, err := d.index(ctx, destFs, id, r.URL.Query().Get("refresh") == "1")
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]any{"id": id, "name": ix.Name, "destFs": destFs, "offline": offline, "runs": ix.Runs(), "timeline": ix.Timeline()})
}

func (d *Daemon) portalList(w http.ResponseWriter, r *http.Request) {
	destFs, id, _, err := d.portalTarget(r)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	at, err := strconv.ParseInt(r.URL.Query().Get("at"), 10, 64)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "at must be a unix time")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	ix, _, err := d.index(ctx, destFs, id, false)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, ix.List(r.URL.Query().Get("path"), time.Unix(at, 0)))
}

func (d *Daemon) portalRestore(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Paths    []string `json:"paths"`
		At       int64    `json:"at"`
		Target   string   `json:"target"` // "original" or "folder"
		Folder   string   `json:"folder"`
		KeepBoth bool     `json:"keepBoth"`
	}
	if err := readJSON(r, &req); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	destFs, id, job, err := d.portalTarget(r)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.Paths) == 0 {
		httpErr(w, http.StatusBadRequest, "choose files or folders to restore")
		return
	}
	var root string
	switch req.Target {
	case "original":
		if job == nil {
			httpErr(w, http.StatusBadRequest, "this backup was made elsewhere; restore it into a folder")
			return
		}
		root = job.Source
		if strings.Contains(root, "{volume}") {
			vr := d.volumeRoot(*job)
			if vr == "" {
				httpErr(w, http.StatusConflict, "connect the drive first")
				return
			}
			root = tune.Expand(root, vr)
		}
	case "folder":
		root = expandHome(strings.TrimSpace(req.Folder))
		if root == "" {
			httpErr(w, http.StatusBadRequest, "choose a folder to restore into")
			return
		}
	default:
		httpErr(w, http.StatusBadRequest, "target must be original or folder")
		return
	}
	ix, _, err := d.index(r.Context(), destFs, id, false)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	d.portal.mu.Lock()
	if d.portal.restore != nil && !d.portal.restore.Finished {
		d.portal.mu.Unlock()
		httpErr(w, http.StatusConflict, "a restore is already running")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.portal.cancel = cancel
	d.portal.restore = &backup.RestoreReport{Target: root}
	d.portal.mu.Unlock()
	go func() {
		defer cancel()
		rep := d.engine.Restore(ctx, ix, time.Unix(req.At, 0), req.Paths, root, req.KeepBoth, func(p backup.RestoreReport) {
			d.portal.mu.Lock()
			cp := p
			d.portal.restore = &cp
			d.portal.mu.Unlock()
			d.hub.notify()
		})
		logf("restore into %s: %d of %d files, %d failed, %d unavailable", root, rep.Done-len(rep.Failed), rep.Total, len(rep.Failed), len(rep.Unavailable))
		if len(rep.Failed) > 0 {
			desktop.Notify("Restore finished with problems", strconv.Itoa(len(rep.Failed))+" file(s) couldn't be restored.")
		} else {
			desktop.Notify("Restore finished", strconv.Itoa(rep.Done)+" file(s) restored.")
		}
	}()
	writeJSON(w, map[string]any{"started": true, "target": root})
}

func (d *Daemon) portalRestoreStatus(w http.ResponseWriter, _ *http.Request) {
	d.portal.mu.Lock()
	defer d.portal.mu.Unlock()
	writeJSON(w, map[string]any{"restore": d.portal.restore})
}

func (d *Daemon) portalRestoreStop(w http.ResponseWriter, _ *http.Request) {
	d.portal.mu.Lock()
	if d.portal.cancel != nil {
		d.portal.cancel()
	}
	d.portal.mu.Unlock()
	writeJSON(w, map[string]bool{"ok": true})
}

// expandHome turns a leading "~" into the user's home folder.
func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		home, err := userFolder()
		if paths.Sandboxed() {
			home, err = filepath.Join(paths.Home(), "home"), nil
		}
		if err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}

// userFolder is where "~" points in the app: the home folder, or shared
// storage on Android (where HOME is the app's private directory).
func userFolder() (string, error) {
	if runtime.GOOS == "android" {
		return androidStorage(), nil
	}
	return os.UserHomeDir()
}
