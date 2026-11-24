// Package jobs runs sync jobs: one at a time per job, with automatic
// retries and backoff, live progress, and a short persisted history.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/blaineam/enter-space-light/internal/rcl"
	"github.com/blaineam/enter-space-light/internal/settings"
	"github.com/blaineam/enter-space-light/internal/tune"
)

// Result is one finished run.
type Result struct {
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished"`
	Reason   string    `json:"reason"` // manual, schedule, daily, start, drive "Label"
	OK       bool      `json:"ok"`
	Error    string    `json:"error,omitempty"`
	Attempts int       `json:"attempts"`
	Bytes    int64     `json:"bytes"`
	Files    int64     `json:"files"`
}

// Live is a running job's progress.
type Live struct {
	Reason  string     `json:"reason"`
	Started time.Time  `json:"started"`
	Attempt int        `json:"attempt"`
	Stats   rcl.Stats  `json:"stats"`
	Waiting *time.Time `json:"waitingUntil,omitempty"` // between retries
}

// State is what the UI shows per job.
type State struct {
	ID      string   `json:"id"`
	Running *Live    `json:"running,omitempty"`
	Last    *Result  `json:"last,omitempty"`
	History []Result `json:"history"`
}

// Book is the persisted part: history plus scheduler bookkeeping.
type Book struct {
	History   map[string][]Result  `json:"history"`
	LastStart map[string]time.Time `json:"lastStart"` // any trigger
	LastDaily map[string]time.Time `json:"lastDaily"` // the daily slot last fired for
	BisyncOK  map[string]string    `json:"bisyncOk"`  // job id → "src|dst" that has completed a resync
}

const keepHistory = 20

// Config wires the runner.
type Config struct {
	RC        rcl.Caller
	Store     *settings.Store
	Resolve   tune.Resolver
	BookPath  string
	Workdir   string // bisync work dir
	Logf      func(format string, args ...any)
	OnChange  func()
	OnFailure func(job settings.Job, err string)
	Locked    func() bool
	Retry     []time.Duration // outer retries after rclone's own retries give up
	Poll      time.Duration
	Now       func() time.Time
}

type running struct {
	live   Live
	cancel context.CancelFunc
}

// Runner executes jobs.
type Runner struct {
	c       Config
	mu      sync.Mutex
	book    Book
	running map[string]*running
	wg      sync.WaitGroup
}

// New loads the history book and returns a runner.
func New(c Config) *Runner {
	if c.Logf == nil {
		c.Logf = func(string, ...any) {}
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Poll == 0 {
		c.Poll = 2 * time.Second
	}
	if c.Retry == nil {
		c.Retry = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute}
	}
	if c.Locked == nil {
		c.Locked = func() bool { return false }
	}
	r := &Runner{c: c, running: map[string]*running{}}
	r.book = loadBook(c.BookPath)
	return r
}

// ErrAlreadyRunning means the job was not started because it is running.
var ErrAlreadyRunning = errors.New("already running")

// Start runs job id in the background. volumeRoot expands {volume}.
func (r *Runner) Start(id, reason, volumeRoot string) error {
	doc := r.c.Store.Get()
	j, ok := doc.FindJob(id)
	if !ok {
		return fmt.Errorf("no sync job %q", id)
	}
	if r.c.Locked() {
		return errors.New("rclone.conf is encrypted — unlock it first")
	}
	if strings.Contains(j.Source+j.Dest, "{volume}") && volumeRoot == "" {
		return errors.New("this job runs when its drive is connected")
	}
	r.mu.Lock()
	if _, busy := r.running[id]; busy {
		r.mu.Unlock()
		return ErrAlreadyRunning
	}
	ctx, cancel := context.WithCancel(context.Background())
	run := &running{live: Live{Reason: reason, Started: r.c.Now(), Attempt: 1}, cancel: cancel}
	r.running[id] = run
	if r.book.LastStart == nil {
		r.book.LastStart = map[string]time.Time{}
	}
	r.book.LastStart[id] = run.live.Started
	r.saveLocked()
	r.mu.Unlock()
	r.changed()

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		res := r.execute(ctx, j, run, volumeRoot)
		cancel()
		r.mu.Lock()
		delete(r.running, id)
		if r.book.History == nil {
			r.book.History = map[string][]Result{}
		}
		h := append([]Result{res}, r.book.History[id]...)
		if len(h) > keepHistory {
			h = h[:keepHistory]
		}
		r.book.History[id] = h
		r.saveLocked()
		r.mu.Unlock()
		if res.OK {
			r.c.Logf("sync %q finished: %d files, %d bytes", j.Name, res.Files, res.Bytes)
		} else {
			r.c.Logf("sync %q failed after %d attempt(s): %s", j.Name, res.Attempts, res.Error)
			if r.c.OnFailure != nil && res.Error != "stopped" {
				r.c.OnFailure(j, res.Error)
			}
		}
		r.changed()
	}()
	return nil
}

func (r *Runner) execute(ctx context.Context, j settings.Job, run *running, volumeRoot string) Result {
	res := Result{Started: run.live.Started, Reason: run.live.Reason}
	srcB := tune.BackendOf(tune.Expand(j.Source, volumeRoot), r.c.Resolve)
	dstB := tune.BackendOf(tune.Expand(j.Dest, volumeRoot), r.c.Resolve)
	pairKey := tune.Expand(j.Source, volumeRoot) + "|" + tune.Expand(j.Dest, volumeRoot)

	for attempt := 1; ; attempt++ {
		r.mu.Lock()
		first := j.Kind == settings.KindBisync && r.book.BisyncOK[j.ID] != pairKey
		run.live.Attempt = attempt
		run.live.Waiting = nil
		r.mu.Unlock()
		r.changed()

		call := tune.JobCall(j, srcB, dstB, volumeRoot, r.c.Workdir, first)
		group := "esl/" + j.ID
		_, _ = r.c.RC.Call(context.Background(), "core/stats-reset", rcl.Params{"group": group})
		var err error
		id, err := rcl.StartAsync(ctx, r.c.RC, call.Method, rcl.Params(call.Params), group)
		if err == nil {
			var st rcl.JobStatus
			st, err = rcl.Wait(ctx, r.c.RC, id, r.c.Poll, func() {
				if s, e := rcl.GroupStats(context.Background(), r.c.RC, group); e == nil {
					r.mu.Lock()
					run.live.Stats = s
					r.mu.Unlock()
					r.changed()
				}
			})
			if err == nil && !st.Success {
				err = errors.New(firstLine(st.Error))
			}
		}
		if s, e := rcl.GroupStats(context.Background(), r.c.RC, group); e == nil {
			res.Bytes, res.Files = s.Bytes, s.Transfers
		}
		res.Attempts = attempt
		if err == nil {
			res.OK = true
			if j.Kind == settings.KindBisync {
				r.mu.Lock()
				if r.book.BisyncOK == nil {
					r.book.BisyncOK = map[string]string{}
				}
				r.book.BisyncOK[j.ID] = pairKey
				r.mu.Unlock()
			}
			break
		}
		if ctx.Err() != nil {
			res.Error = "stopped"
			break
		}
		res.Error = err.Error()
		if attempt > len(r.c.Retry) || !retryable(err) {
			break
		}
		wait := r.c.Retry[attempt-1]
		until := r.c.Now().Add(wait)
		r.mu.Lock()
		run.live.Waiting = &until
		r.mu.Unlock()
		r.changed()
		r.c.Logf("sync %q attempt %d failed (%v); retrying in %s", j.Name, attempt, err, wait)
		select {
		case <-ctx.Done():
			res.Error = "stopped"
			res.Finished = r.c.Now()
			return res
		case <-time.After(wait):
		}
	}
	res.Finished = r.c.Now()
	return res
}

// retryable: configuration mistakes won't fix themselves; everything else
// (network, throttling, a server hiccup) might.
func retryable(err error) bool {
	s := strings.ToLower(err.Error())
	for _, permanent := range []string{
		"didn't find section in config file", "not found in config", "unknown parameters",
		"directory not found", "is a file not a directory", "can't sync or move files on overlapping remotes",
		"bisync aborted", "must run --resync", "permission denied", "access is denied",
	} {
		if strings.Contains(s, permanent) {
			return false
		}
	}
	return true
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return "failed"
	}
	return s
}

// Stop cancels a running job.
func (r *Runner) Stop(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, ok := r.running[id]
	if ok {
		run.cancel()
	}
	return ok
}

// StopAll cancels every job and waits up to d.
func (r *Runner) StopAll(d time.Duration) {
	r.mu.Lock()
	for _, run := range r.running {
		run.cancel()
	}
	r.mu.Unlock()
	done := make(chan struct{})
	go func() { r.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
	}
}

// Wait blocks until every running job ends (tests).
func (r *Runner) Wait() { r.wg.Wait() }

// Running reports whether id is running.
func (r *Runner) Running(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.running[id]
	return ok
}

// Snapshot returns every job's state, in settings order.
func (r *Runner) Snapshot() []State {
	doc := r.c.Store.Get()
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]State, 0, len(doc.Jobs))
	for _, j := range doc.Jobs {
		st := State{ID: j.ID, History: append([]Result{}, r.book.History[j.ID]...)}
		if len(st.History) > 0 {
			last := st.History[0]
			st.Last = &last
		}
		if run, ok := r.running[j.ID]; ok {
			l := run.live
			st.Running = &l
		}
		out = append(out, st)
	}
	return out
}

// Forget drops bookkeeping for deleted jobs.
func (r *Runner) Forget(keep map[string]bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, m := range []map[string]time.Time{r.book.LastStart, r.book.LastDaily} {
		for id := range m {
			if !keep[id] {
				delete(m, id)
			}
		}
	}
	for id := range r.book.History {
		if !keep[id] {
			delete(r.book.History, id)
		}
	}
	for id := range r.book.BisyncOK {
		if !keep[id] {
			delete(r.book.BisyncOK, id)
		}
	}
	r.saveLocked()
}

// Bookkeeping accessors for the scheduler.

// LastStart is when id last started for any reason.
func (r *Runner) LastStart(id string) time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.book.LastStart[id]
}

// LastDaily is the daily slot id last fired for.
func (r *Runner) LastDaily(id string) time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.book.LastDaily[id]
}

// MarkDaily records that the daily slot fired.
func (r *Runner) MarkDaily(id string, slot time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.book.LastDaily == nil {
		r.book.LastDaily = map[string]time.Time{}
	}
	r.book.LastDaily[id] = slot
	r.saveLocked()
}

func (r *Runner) changed() {
	if r.c.OnChange != nil {
		r.c.OnChange()
	}
}

func (r *Runner) saveLocked() {
	if r.c.BookPath == "" {
		return
	}
	if err := settings.WriteJSONAtomic(r.c.BookPath, r.book); err != nil {
		r.c.Logf("saving history: %v", err)
	}
}

func loadBook(path string) Book {
	b := Book{History: map[string][]Result{}, LastStart: map[string]time.Time{}, LastDaily: map[string]time.Time{}, BisyncOK: map[string]string{}}
	if path == "" {
		return b
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return b
	}
	if err := jsonUnmarshal(data, &b); err != nil {
		return Book{History: map[string][]Result{}, LastStart: map[string]time.Time{}, LastDaily: map[string]time.Time{}, BisyncOK: map[string]string{}}
	}
	return b
}
