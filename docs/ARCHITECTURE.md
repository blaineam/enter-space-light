# Architecture

One Go binary, `esl`, with no cgo on any platform. It embeds rclone as a library and calls rclone's own rc functions in-process (`internal/rcl`), with no subprocess and no HTTP hop. That's the same code path `rclone rcd` uses, so mounts, syncs, config and stats behave exactly as rclone documents them.

```
cmd/esl            CLI entry: run | open | status | connect | sync | … | rclone <args>
internal/daemon    lifecycle, local HTTP API + UI server, event hub, tray model
internal/mounts    reconcile loop: desired state → mounted, watchdog, backoff, resume
internal/jobs      sync runner (retries, progress, history) + scheduler
internal/tune      rclone parameters per backend; option names tested against rclone
internal/volumes   drive arrival detection (Windows drive letters, Linux mountinfo)
internal/settings  settings.json model, validation, atomic writes
internal/desktop   open folders / app window, autostart, bookmarks, notifications
internal/tray      tray icon and menu (fyne.io/systray, pure Go on Windows and Linux)
internal/ui/web    the settings window (vanilla JS, no build step), embedded
internal/rclonecli the full rclone CLI as `esl rclone`, minus selfupdate
```

## Mounting

- **Windows:** rclone `cmount` → cgofuse → WinFsp, with the DLL loaded at runtime (needs `-tags cmount`). Drives use network mode and the volume name `\\enterspace\<name>`. Automatic letters use rclone's `*`.
- **Linux:** rclone `mount` (pure-Go FUSE) through `fusermount3`. The default mount folder is `~/Cloud/<name>`, added to GTK file-manager bookmarks.
- `mounts.Manager` keeps desired state (wanted or not wanted). A reconcile pass every 5 seconds mounts what is wanted, runs health checks (rclone's mount list plus a timed directory read), and remounts after two failures with backoff. A wall-clock gap much longer than the tick means the machine slept, and triggers immediate health checks.

## Syncs

`jobs.Runner` starts rclone `sync/*` calls with `_async`, polls `job/status` and `core/stats` (per-job stats group), and retries after rclone's own retries give up, unless the error is permanent. Bisync remembers per source and destination pair whether it has completed the required first `--resync`.

`jobs.Scheduler` is a pure function of the clock, the settings and the runner's bookkeeping, evaluated every 15 seconds. That's why intervals and daily slots catch up correctly after sleep. Drive triggers match by volume serial or filesystem UUID, never by letter or path. `{volume}` in a path expands to wherever the drive appeared.

## Local API

Served on `127.0.0.1:<random>`. The CLI reads the address and bearer token from `api.json`. The app window signs in with a one-time code and gets a session cookie. See `SECURITY.md`. `/api/events` is a Server-Sent Events stream of "changed" pings, coalesced to at most one every 400 ms, and the UI re-reads `/api/status`.

## Sandbox

`ESL_HOME=<dir>` moves settings, state, cache, logs, `rclone.conf` and default mount folders inside `<dir>`. It also disables autostart and bookmarks and uses a separate keychain entry. It wins over `RCLONE_CONFIG`. The end-to-end test runs this way and checks that a decoy `RCLONE_CONFIG` is never touched.

## Tests

- `go test -race ./...`: settings validation, tuning (including that every option name exists in rclone), mount reconcile and watchdog, runner retries, scheduler catch-up, volume diffing.
- `go test -tags e2e -run TestE2E ./internal/daemon`: the real daemon in a sandbox, covering the API security checks, a filtered copy, a two-way sync and validation. With `ESL_E2E_MOUNT=1` (CI installs FUSE or WinFsp) it also mounts and does reads, seeks, writes and renames through the drive.
