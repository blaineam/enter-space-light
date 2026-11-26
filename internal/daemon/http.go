package daemon

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/blaineam/enter-space-light/internal/backup"
	"github.com/blaineam/enter-space-light/internal/desktop"
	"github.com/blaineam/enter-space-light/internal/jobs"
	"github.com/blaineam/enter-space-light/internal/mounts"
	"github.com/blaineam/enter-space-light/internal/paths"
	"github.com/blaineam/enter-space-light/internal/rcl"
	"github.com/blaineam/enter-space-light/internal/secrets"
	"github.com/blaineam/enter-space-light/internal/settings"
	"github.com/blaineam/enter-space-light/internal/tune"
	"github.com/blaineam/enter-space-light/internal/ui"
	"github.com/blaineam/enter-space-light/internal/version"
	"github.com/blaineam/enter-space-light/internal/volumes"
	"github.com/blaineam/enter-space-light/internal/winfsp"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config"
	"github.com/rclone/rclone/fs/fspath"
)

const cookieName = "esl_session"

func (d *Daemon) routes() http.Handler {
	mux := http.NewServeMux()

	// UI
	mux.Handle("/", uiOnly(ui.Handler()))
	mux.HandleFunc("GET /login", d.login)

	// API (auth + CSRF enforced by d.guard)
	api := http.NewServeMux()
	api.HandleFunc("POST /api/login-code", d.loginCode)
	api.HandleFunc("GET /api/status", d.status)
	api.HandleFunc("GET /api/events", d.events)

	api.HandleFunc("POST /api/mounts", d.createMount)
	api.HandleFunc("PUT /api/mounts/{id}", d.updateMount)
	api.HandleFunc("DELETE /api/mounts/{id}", d.deleteMount)
	api.HandleFunc("POST /api/mounts/{id}/connect", d.mountAction(true))
	api.HandleFunc("POST /api/mounts/{id}/disconnect", d.mountAction(false))
	api.HandleFunc("POST /api/mounts/{id}/open", d.openMount)

	api.HandleFunc("POST /api/jobs", d.createJob)
	api.HandleFunc("PUT /api/jobs/{id}", d.updateJob)
	api.HandleFunc("DELETE /api/jobs/{id}", d.deleteJob)
	api.HandleFunc("POST /api/jobs/{id}/run", d.runJob)
	api.HandleFunc("POST /api/jobs/{id}/stop", d.stopJob)

	api.HandleFunc("GET /api/backup/setup", d.backupSetup)
	api.HandleFunc("GET /api/portal/discover", d.portalDiscover)
	api.HandleFunc("GET /api/portal/timeline", d.portalTimeline)
	api.HandleFunc("GET /api/portal/list", d.portalList)
	api.HandleFunc("POST /api/portal/restore", d.portalRestore)
	api.HandleFunc("GET /api/portal/restore", d.portalRestoreStatus)
	api.HandleFunc("POST /api/portal/restore/stop", d.portalRestoreStop)

	api.HandleFunc("PUT /api/prefs", d.putPrefs)
	api.HandleFunc("POST /api/autostart", d.setAutostart)

	api.HandleFunc("GET /api/providers", d.providers)
	api.HandleFunc("POST /api/remotes/config", d.configRemote)
	api.HandleFunc("DELETE /api/remotes/{name}", d.deleteRemote)
	api.HandleFunc("GET /api/rclone-conf", d.getConf)
	api.HandleFunc("PUT /api/rclone-conf", d.putConf)
	api.HandleFunc("POST /api/unlock", d.unlock)
	api.HandleFunc("POST /api/forget-password", d.forgetPassword)

	api.HandleFunc("GET /api/ls", d.ls)
	api.HandleFunc("GET /api/log", d.logTail)
	api.HandleFunc("POST /api/open-log", func(w http.ResponseWriter, _ *http.Request) {
		_ = desktop.OpenFolder(paths.LogDir())
		writeJSON(w, map[string]bool{"ok": true})
	})
	api.HandleFunc("POST /api/winfsp/install", d.installWinFsp)
	api.HandleFunc("POST /api/android/permission", d.androidPermission)
	api.HandleFunc("POST /api/quit", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]bool{"ok": true})
		go func() { time.Sleep(200 * time.Millisecond); d.Quit() }()
	})
	mux.Handle("/api/", d.guard(api))
	return d.hostCheck(securityHeaders(mux))
}

// hostCheck defeats DNS rebinding: only our own loopback address is served.
func (d *Daemon) hostCheck(next http.Handler) http.Handler {
	_, port, _ := strings.Cut(d.host, ":")
	ok := map[string]bool{d.host: true, "localhost:" + port: true}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ok[r.Host] {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func eq(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

// guard authenticates API calls and blocks cross-site requests.
//   - CLI/tray: "Authorization: Bearer <token from api.json>".
//   - UI window: the HttpOnly SameSite=Strict session cookie.
//   - Any state change also needs "X-ESL: 1" (a header no cross-origin page
//     can send without a CORS preflight we never answer) and, if the
//     browser sent an Origin, it must be ours.
func (d *Daemon) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		viaBearer := bearer != "" && eq(bearer, d.token)
		viaCookie := false
		if c, err := r.Cookie(cookieName); err == nil {
			viaCookie = eq(c.Value, d.session)
		}
		if r.URL.Path == "/api/login-code" && !viaBearer {
			httpErr(w, http.StatusUnauthorized, "bearer token required")
			return
		}
		if !viaBearer && !viaCookie {
			httpErr(w, http.StatusUnauthorized, "not signed in — open Enter Space Light from the tray icon")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if r.Header.Get("X-ESL") != "1" {
				httpErr(w, http.StatusForbidden, "missing X-ESL header")
				return
			}
			if o := r.Header.Get("Origin"); o != "" && o != "http://"+d.host && !strings.HasPrefix(o, "http://localhost:") {
				httpErr(w, http.StatusForbidden, "cross-origin request refused")
				return
			}
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
		next.ServeHTTP(w, r)
	})
}

func (d *Daemon) login(w http.ResponseWriter, r *http.Request) {
	if !d.useCode(r.URL.Query().Get("code")) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `<!doctype html><meta charset="utf-8"><title>Enter Space Light</title><body style="font:15px system-ui;padding:3em;max-width:34em">This link has expired. Open Enter Space Light again from its tray icon, or run <code>esl open</code>.`)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: d.session, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (d *Daemon) loginCode(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]string{"code": d.newCode(), "url": "http://" + d.host})
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func httpErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("bad request body: %w", err)
	}
	return nil
}

// ---- status ----

type remoteView struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Backend string `json:"backend"` // after following crypt/alias
}

type mountView struct {
	settings.Mount
	State   mounts.State `json:"state"`
	Backend string       `json:"backend"`
}

type jobView struct {
	settings.Job
	State  jobs.State    `json:"state"`
	Health *backup.Alert `json:"health,omitempty"`
	// Waiting is why a due run is being held (Android: "waiting for Wi-Fi").
	Waiting string `json:"waiting,omitempty"`
}

// Status is the whole UI model in one document.
type Status struct {
	Version   string `json:"version"`
	Rclone    string `json:"rclone"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	PID       int    `json:"pid"`
	Notice    string `json:"notice,omitempty"`
	Autostart bool   `json:"autostart"`
	MountRoot string `json:"mountRoot,omitempty"`
	LogFile   string `json:"logFile"`
	Config    struct {
		Path      string       `json:"path"`
		Encrypted bool         `json:"encrypted"`
		Locked    bool         `json:"locked"`
		Saved     bool         `json:"passwordSaved"`
		Remotes   []remoteView `json:"remotes"`
	} `json:"config"`
	WinFsp struct {
		Required   bool   `json:"required"`
		Installed  bool   `json:"installed"`
		Installing bool   `json:"installing"`
		Notice     string `json:"notice"`
		URL        string `json:"url"`
	} `json:"winfsp"`
	Prefs   settings.Prefs   `json:"prefs"`
	Mounts  []mountView      `json:"mounts"`
	Jobs    []jobView        `json:"jobs"`
	Volumes []volumes.Volume `json:"volumes"`
	Android *androidStatus   `json:"android,omitempty"`
}

func (d *Daemon) snapshot() Status {
	doc := d.store.Get()
	var s Status
	s.Version, s.Rclone, s.OS, s.Arch, s.PID = version.Version, rcl.Version(), runtime.GOOS, runtime.GOARCH, os.Getpid()
	s.Notice, _ = d.notice.Load().(string)
	s.Autostart = desktop.Autostart()
	s.MountRoot = mountRoot(doc.Prefs)
	s.LogFile = paths.LogFile()
	s.Config.Path = rcl.ConfigPath()
	s.Config.Encrypted = rcl.ConfigEncrypted()
	s.Config.Locked = rcl.ConfigLocked()
	if s.Config.Encrypted {
		s.Config.Saved = secrets.ConfigPassword() != ""
	}
	s.Config.Remotes = []remoteView{}
	if !s.Config.Locked {
		for _, name := range config.LoadedData().GetSectionList() {
			typ, _, _ := rcl.Backend(name)
			s.Config.Remotes = append(s.Config.Remotes, remoteView{Name: name, Type: typ, Backend: tune.BackendOf(name+":", rcl.Backend)})
		}
		sort.Slice(s.Config.Remotes, func(i, j int) bool {
			return strings.ToLower(s.Config.Remotes[i].Name) < strings.ToLower(s.Config.Remotes[j].Name)
		})
	}
	s.WinFsp.Required = runtime.GOOS == "windows"
	s.WinFsp.Installed = winfsp.Installed()
	s.WinFsp.Installing = d.winfspInstalling.Load()
	s.WinFsp.Notice, s.WinFsp.URL = winfsp.Notice, winfsp.URL
	s.Prefs = doc.Prefs

	ms := map[string]mounts.State{}
	for _, st := range d.mounts.Snapshot() {
		ms[st.ID] = st
	}
	s.Mounts = []mountView{}
	for _, m := range doc.Mounts {
		s.Mounts = append(s.Mounts, mountView{Mount: m, State: ms[m.ID], Backend: tune.BackendOf(m.Remote, rcl.Backend)})
	}
	js := map[string]jobs.State{}
	for _, st := range d.runner.Snapshot() {
		js[st.ID] = st
	}
	s.Jobs = []jobView{}
	health := d.backupHealth(doc)
	for _, j := range doc.Jobs {
		s.Jobs = append(s.Jobs, jobView{Job: j, State: js[j.ID], Health: health[j.ID], Waiting: d.sched.Waiting(j)})
	}
	s.Volumes = d.vols.Current()
	if s.Volumes == nil {
		s.Volumes = []volumes.Volume{}
	}
	s.Android = androidSnapshot()
	return s
}

func (d *Daemon) status(w http.ResponseWriter, _ *http.Request) { writeJSON(w, d.snapshot()) }

// events is a Server-Sent Events stream of "changed" pings; the UI re-reads
// /api/status on each one.
func (d *Daemon) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		httpErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	ch, cancel := d.hub.subscribe()
	defer cancel()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	_, _ = io.WriteString(w, "event: changed\ndata: {}\n\n")
	fl.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-d.quitCh:
			_, _ = io.WriteString(w, "event: quit\ndata: {}\n\n")
			fl.Flush()
			return
		case <-ch:
			_, _ = io.WriteString(w, "event: changed\ndata: {}\n\n")
		case <-ping.C:
			_, _ = io.WriteString(w, ": ping\n\n")
		}
		fl.Flush()
	}
}

// ---- mounts ----

func (d *Daemon) update(w http.ResponseWriter, fn func(*settings.Settings) error) bool {
	if _, err := d.store.Update(fn); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return false
	}
	d.afterSettings()
	return true
}

func (d *Daemon) createMount(w http.ResponseWriter, r *http.Request) {
	var m settings.Mount
	if err := readJSON(r, &m); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	m.ID = settings.NewID()
	if !d.update(w, func(s *settings.Settings) error { s.Mounts = append(s.Mounts, m); return nil }) {
		return
	}
	if m.AutoMount {
		_ = d.mounts.SetWant(m.ID, true)
	}
	writeJSON(w, m)
}

func (d *Daemon) updateMount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var m settings.Mount
	if err := readJSON(r, &m); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	m.ID = id
	if !d.update(w, func(s *settings.Settings) error {
		for i := range s.Mounts {
			if s.Mounts[i].ID == id {
				s.Mounts[i] = m
				return nil
			}
		}
		return fmt.Errorf("no mount %q", id)
	}) {
		return
	}
	writeJSON(w, m)
}

func (d *Daemon) deleteMount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !d.update(w, func(s *settings.Settings) error {
		out := s.Mounts[:0]
		for _, m := range s.Mounts {
			if m.ID != id {
				out = append(out, m)
			}
		}
		s.Mounts = out
		return nil
	}) {
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (d *Daemon) mountAction(want bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := d.mounts.SetWant(r.PathValue("id"), want); err != nil {
			httpErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	}
}

func (d *Daemon) openMount(w http.ResponseWriter, r *http.Request) {
	st, ok := d.mounts.Get(r.PathValue("id"))
	if !ok || st.Status != mounts.StatusMounted || st.MountPoint == "" {
		httpErr(w, http.StatusConflict, "not connected")
		return
	}
	if err := desktop.OpenFolder(st.MountPoint); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// ---- jobs ----

func (d *Daemon) createJob(w http.ResponseWriter, r *http.Request) {
	var j settings.Job
	if err := readJSON(r, &j); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	j.ID = settings.NewID()
	d.fillBackup(&j, nil)
	if !d.update(w, func(s *settings.Settings) error { s.Jobs = append(s.Jobs, j); return nil }) {
		return
	}
	writeJSON(w, j)
}

func (d *Daemon) updateJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var j settings.Job
	if err := readJSON(r, &j); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	j.ID = id
	if !d.update(w, func(s *settings.Settings) error {
		for i := range s.Jobs {
			if s.Jobs[i].ID == id {
				d.fillBackup(&j, &s.Jobs[i])
				s.Jobs[i] = j
				return nil
			}
		}
		return fmt.Errorf("no sync job %q", id)
	}) {
		return
	}
	writeJSON(w, j)
}

func (d *Daemon) deleteJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d.runner.Stop(id)
	if !d.update(w, func(s *settings.Settings) error {
		out := s.Jobs[:0]
		for _, j := range s.Jobs {
			if j.ID != id {
				out = append(out, j)
			}
		}
		s.Jobs = out
		return nil
	}) {
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (d *Daemon) runJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	doc := d.store.Get()
	j, ok := doc.FindJob(id)
	if !ok {
		httpErr(w, http.StatusNotFound, "no such sync job")
		return
	}
	root := ""
	if v := j.Triggers.OnVolume; v != nil {
		for _, vol := range d.vols.Current() {
			if vol.Matches(v.ID, v.Label) {
				root = vol.Root
			}
		}
		if root == "" && strings.Contains(j.Source+j.Dest, "{volume}") {
			httpErr(w, http.StatusConflict, "connect the drive first")
			return
		}
	}
	if err := d.runner.Start(id, "manual", root); err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, jobs.ErrAlreadyRunning) {
			code = http.StatusConflict
		}
		httpErr(w, code, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (d *Daemon) stopJob(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]bool{"stopped": d.runner.Stop(r.PathValue("id"))})
}

// ---- prefs ----

func (d *Daemon) putPrefs(w http.ResponseWriter, r *http.Request) {
	var p settings.Prefs
	if err := readJSON(r, &p); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if p.BWLimit != "" {
		var bw fs.BwTimetable
		if err := bw.Set(p.BWLimit); err != nil {
			httpErr(w, http.StatusBadRequest, "bandwidth limit: "+err.Error())
			return
		}
	}
	if p.CacheMaxSize != "" {
		var sz fs.SizeSuffix
		if err := sz.Set(p.CacheMaxSize); err != nil {
			httpErr(w, http.StatusBadRequest, "cache size: "+err.Error())
			return
		}
	}
	if p.LogLevel != "" {
		var lvl fs.LogLevel
		if err := lvl.Set(p.LogLevel); err != nil {
			httpErr(w, http.StatusBadRequest, "log level: "+err.Error())
			return
		}
	}
	if !d.update(w, func(s *settings.Settings) error { s.Prefs = p; return nil }) {
		return
	}
	writeJSON(w, p)
}

func (d *Daemon) setAutostart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		On       bool `json:"on"`
		Headless bool `json:"headless"`
	}
	if err := readJSON(r, &req); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := desktop.SetAutostart(req.On, req.Headless); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	d.hub.notify()
	writeJSON(w, map[string]bool{"autostart": desktop.Autostart()})
}

// ---- remotes / rclone.conf ----

type optionView struct {
	Name       string   `json:"name"`
	Help       string   `json:"help"`
	Provider   string   `json:"provider,omitempty"`
	Default    string   `json:"default,omitempty"`
	Required   bool     `json:"required,omitempty"`
	IsPassword bool     `json:"isPassword,omitempty"`
	Sensitive  bool     `json:"sensitive,omitempty"`
	Advanced   bool     `json:"advanced,omitempty"`
	Exclusive  bool     `json:"exclusive,omitempty"`
	Type       string   `json:"type"`
	Examples   []choice `json:"examples,omitempty"`
}

type choice struct {
	Value    string `json:"value"`
	Help     string `json:"help"`
	Provider string `json:"provider,omitempty"`
}

type providerView struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Options     []optionView `json:"options"`
}

// providers lists every backend and its options, trimmed to what a form
// needs. Advanced options are included but flagged; the UI hides them
// behind a disclosure.
func (d *Daemon) providers(w http.ResponseWriter, _ *http.Request) {
	out := []providerView{}
	for _, ri := range fs.Registry {
		if ri.Hide {
			continue
		}
		pv := providerView{Name: ri.Name, Description: ri.Description}
		for _, o := range ri.Options {
			if o.Hide&fs.OptionHideConfigurator != 0 {
				continue
			}
			ov := optionView{Name: o.Name, Help: o.Help, Provider: o.Provider, Required: o.Required, IsPassword: o.IsPassword,
				Sensitive: o.Sensitive, Advanced: o.Advanced, Exclusive: o.Exclusive, Type: o.Type()}
			if o.Default != nil {
				ov.Default = fmt.Sprint(o.Default)
				if ov.Type == "bool" {
					ov.Default = strconv.FormatBool(o.Default == true)
				}
			}
			for _, e := range o.Examples {
				ov.Examples = append(ov.Examples, choice{Value: e.Value, Help: e.Help, Provider: e.Provider})
			}
			pv.Options = append(pv.Options, ov)
		}
		out = append(out, pv)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Description < out[j].Description })
	writeJSON(w, out)
}

// configRemote drives rclone's own configuration state machine: create or
// update a remote, then answer follow-up questions (OAuth sign-in, team
// drive choice…) one at a time until rclone says it is done.
func (d *Daemon) configRemote(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name       string            `json:"name"`
		Type       string            `json:"type"`
		Parameters map[string]string `json:"parameters"`
		Update     bool              `json:"update"`
		State      string            `json:"state"`
		Result     string            `json:"result"`
		Continue   bool              `json:"continue"`
	}
	if err := readJSON(r, &req); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if rcl.ConfigLocked() {
		httpErr(w, http.StatusConflict, "unlock rclone.conf first")
		return
	}
	if err := fspath.CheckConfigName(req.Name); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	params := rcl.Params{}
	for k, v := range req.Parameters {
		if v != "" {
			params[k] = v
		}
	}
	method := "config/create"
	in := rcl.Params{"name": req.Name, "parameters": params,
		"opt": rcl.Params{"nonInteractive": true, "obscure": true, "state": req.State, "result": req.Result, "continue": req.Continue}}
	if req.Update || req.Continue {
		method = "config/update"
	} else {
		if config.LoadedData().HasSection(req.Name) {
			httpErr(w, http.StatusConflict, fmt.Sprintf("a remote called %q already exists", req.Name))
			return
		}
		in["type"] = req.Type
	}
	// OAuth questions block here while the user signs in in their browser.
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	out, err := d.rc.Call(ctx, method, in)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	d.hub.notify()
	writeJSON(w, out)
}

func (d *Daemon) deleteRemote(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	doc := d.store.Get()
	var users []string
	for _, m := range doc.Mounts {
		if tune.RemoteName(m.Remote) == name {
			users = append(users, "drive "+m.Name)
		}
	}
	for _, j := range doc.Jobs {
		if tune.RemoteName(j.Source) == name || tune.RemoteName(j.Dest) == name {
			users = append(users, "sync "+j.Name)
		}
	}
	if len(users) > 0 && r.URL.Query().Get("force") != "1" {
		httpErr(w, http.StatusConflict, "still used by "+strings.Join(users, ", "))
		return
	}
	if _, err := d.rc.Call(r.Context(), "config/delete", rcl.Params{"name": name}); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	d.hub.notify()
	writeJSON(w, map[string]bool{"ok": true})
}

func (d *Daemon) getConf(w http.ResponseWriter, _ *http.Request) {
	if rcl.ConfigEncrypted() {
		httpErr(w, http.StatusConflict, "rclone.conf is encrypted; edit it with `esl rclone config`")
		return
	}
	b, err := os.ReadFile(rcl.ConfigPath())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]string{"path": rcl.ConfigPath(), "text": string(b)})
}

// putConf replaces rclone.conf with the user's text, keeping a backup and
// rolling back if rclone can't load the result.
func (d *Daemon) putConf(w http.ResponseWriter, r *http.Request) {
	if rcl.ConfigEncrypted() {
		httpErr(w, http.StatusConflict, "rclone.conf is encrypted; edit it with `esl rclone config`")
		return
	}
	var req struct {
		Text string `json:"text"`
	}
	if err := readJSON(r, &req); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	path := rcl.ConfigPath()
	old, readErr := os.ReadFile(path)
	if readErr == nil {
		if err := os.WriteFile(path+".bak", old, 0o600); err != nil {
			httpErr(w, http.StatusInternalServerError, "backup failed: "+err.Error())
			return
		}
	}
	text := req.Text
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := rcl.Reload(); err != nil {
		if readErr == nil {
			_ = os.WriteFile(path, old, 0o600)
			_ = rcl.Reload()
		}
		httpErr(w, http.StatusBadRequest, "rclone could not read that, so nothing was changed: "+err.Error())
		return
	}
	d.mounts.Kick()
	d.hub.notify()
	writeJSON(w, map[string]string{"backup": path + ".bak"})
}

func (d *Daemon) unlock(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
		Remember bool   `json:"remember"`
	}
	if err := readJSON(r, &req); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := rcl.Unlock(req.Password); err != nil {
		httpErr(w, http.StatusUnauthorized, err.Error())
		return
	}
	saved := false
	if req.Remember {
		if err := secrets.SaveConfigPassword(req.Password); err != nil {
			logf("saving password to the keychain: %v", err)
		} else {
			saved = true
		}
	}
	d.mounts.Kick()
	d.hub.notify()
	writeJSON(w, map[string]bool{"ok": true, "saved": saved})
}

func (d *Daemon) forgetPassword(w http.ResponseWriter, _ *http.Request) {
	if err := secrets.ForgetConfigPassword(); err != nil {
		httpErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	d.hub.notify()
	writeJSON(w, map[string]bool{"ok": true})
}

// ---- browsing / logs / misc ----

// ls lists folders for the path pickers.
func (d *Daemon) ls(w http.ResponseWriter, r *http.Request) {
	fsName := r.URL.Query().Get("fs")
	dir := r.URL.Query().Get("path")
	if fsName == "" {
		httpErr(w, http.StatusBadRequest, "fs is required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	out, err := d.rc.Call(ctx, "operations/list", rcl.Params{"fs": fsName, "remote": dir, "opt": rcl.Params{"dirsOnly": true}})
	if err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	raw, _ := json.Marshal(out["list"])
	var items []struct {
		Name string `json:"Name"`
		Path string `json:"Path"`
	}
	_ = json.Unmarshal(raw, &items)
	sort.Slice(items, func(i, j int) bool { return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name) })
	writeJSON(w, map[string]any{"fs": fsName, "path": dir, "dirs": items})
}

func (d *Daemon) logTail(w http.ResponseWriter, r *http.Request) {
	kb, _ := strconv.Atoi(r.URL.Query().Get("kb"))
	if kb <= 0 || kb > 1024 {
		kb = 64
	}
	f, err := os.Open(paths.LogFile())
	if err != nil {
		writeJSON(w, map[string]string{"text": ""})
		return
	}
	defer func() { _ = f.Close() }()
	st, _ := f.Stat()
	off := st.Size() - int64(kb)*1024
	if off < 0 {
		off = 0
	}
	_, _ = f.Seek(off, io.SeekStart)
	b, _ := io.ReadAll(f)
	text := string(b)
	if off > 0 {
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = text[i+1:]
		}
	}
	writeJSON(w, map[string]string{"text": text, "path": paths.LogFile()})
}

func (d *Daemon) installWinFsp(w http.ResponseWriter, _ *http.Request) {
	if runtime.GOOS != "windows" {
		httpErr(w, http.StatusBadRequest, "WinFsp is only needed on Windows")
		return
	}
	if !d.winfspInstalling.CompareAndSwap(false, true) {
		writeJSON(w, map[string]bool{"installing": true})
		return
	}
	d.hub.notify()
	go func() {
		defer func() {
			d.winfspInstalling.Store(false)
			d.mounts.Kick()
			d.hub.notify()
		}()
		if err := winfsp.Install(); err != nil {
			logf("WinFsp install: %v", err)
		}
	}()
	writeJSON(w, map[string]bool{"installing": true})
}

// uiOnly serves the static UI to GET/HEAD and refuses everything else.
func uiOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		next.ServeHTTP(w, r)
	})
}
