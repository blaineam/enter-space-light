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
internal/backup    versioned backups: wizard skip lists, manifests, delete guard,
                   retention, health, point-in-time resolver (Time Portal), restore
```

## Backups and Time Portal

A backup is an ordinary sync or copy job with `backup` options. The runner hands it to `backup.Engine`:

- **Before the run:**
  - The dated versions folder becomes `--backup-dir`. It sits at the top of the remote (or inside the bucket, or at the drive root) as `.previous-versions/<namespace>/<yyyy-MM-dd_HH-mm-ss>`.
  - `- /.enterspace/**` is always the first filter rule, so run records are never touched.
  - The wizard's skip-list rules are added, plus our own mount points inside the source. On Linux, local sources also get `one_file_system` and `links`.
  - The **delete guard** counts source and destination. It refuses to run when the source is empty, or when more than 25% of 100+ files would go. Otherwise it sets `max_delete`.
- **After the run:** the engine writes a run record (a **manifest**) to `<dest>/.enterspace/manifests/<SYNC-UUID>/<RUN-UUID>.json.deflate`, and keeps a local copy for offline browsing. The record holds:
  - this run's displaced files (a listing of the versions folder);
  - the live tree, listed fresh when the run changed anything or the last full look is over 24 hours old, otherwise carried forward. It is capped at 400k entries or 15 minutes, past which it counts as `partial`.

  Retention then removes expired version folders newest-first, but only those whose files all still exist elsewhere (`PlanRetention`).

The format is byte-compatible with Enter Space for Apple platforms (`BackupManifest.swift`): raw DEFLATE JSON, ISO-8601 dates with whole seconds, uppercase UUIDs. Windows local paths are written `/C:/…` so the Apple app reads them as local. `scripts/check-apple-compat.sh` compiles the Apple app's real codec and checks both directions.

`backup.Index` is a rule-for-rule port of the Apple app's `PointInTimeResolver`:

- The earliest version displaced **after** T is the content at T.
- Otherwise the live copy is, if it existed at T.
- Otherwise the item is reported as unknown or unavailable, never dropped.
- Partial listings never count as evidence of deletion.

The index is always rebuilt from the destination's manifests. Which version folders retention removed is learned by listing them, so pruned versions show as "no longer kept" rather than failing on restore.

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
