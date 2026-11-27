# Changelog

All notable changes to Enter Space Light. Versions follow [Semantic Versioning](https://semver.org).

## [Unreleased]

## [0.2.1] - 2026-11-27

- Fixed (Linux): Enter Space Light could freeze for good if a sync or backup used one of its own drive folders as the source or destination. Once frozen, it couldn't be closed, not even with `kill -9`. This could happen in every earlier version. It comes from how Go and the FUSE library work together, so the Linux build now uses a small patched copy of `bazil.org/fuse` (see `third_party/bazil-fuse/ENTER-SPACE-LIGHT.md`).
- The Windows and Android apps work the same as in 0.2.0. Linux users should update.

## [0.2.0] - 2026-11-26

- New: **Android** (8.0 and later, arm64), as an APK to install directly rather than from Google Play.
  - Each drive appears in the Files app and in other apps' open and save screens. Apps read, seek, edit in place and append through real file descriptors backed by rclone's cache; changes upload in the background.
  - A background service keeps drives available and runs schedules, starts with the phone, and keeps the processor awake only while something is transferring.
  - Scheduled syncs wait for Wi-Fi unless you allow mobile data, can wait for the charger, and run once when allowed. Syncs you start yourself run immediately.
  - The backup wizard and Time Portal work on the phone's shared storage and USB drives, with Android skip lists. Back up to a cloud remote, a USB drive or a folder.
- Fixed (all platforms): skip rules written as `**/name` never matched at the top of the source, so backups included things they should have skipped there, such as `$RECYCLE.BIN` at the root of a drive, or a `node_modules` folder directly inside the backed-up folder. No files were lost. Files copied that way earlier stay in the backup; delete them there if you don't want them.

## [0.1.1] - 2026-11-25

- Fixed (Windows): drives set to an automatic letter never connected on a PC with a disconnected network drive ("failed to mount FUSE fs: mount stopped before calling Init"). The disconnected drive's letter, usually Z:, looked free, so the app tried it again and again. Enter Space Light now picks the letter itself, skips every letter Windows already has (including disconnected and remembered network drives), and tries another letter if one fails.

## [0.1.0] - 2026-11-24

First version, for Windows (x64, Arm) and Linux (x86_64, arm64).

- Backups: a five-step wizard (what, where, what to skip, when and how long, review) for your user folder, chosen folders or a whole drive, to a cloud remote, a USB drive or a folder. It can set up a second copy.
  - Old versions of changed and deleted files are kept in dated folders.
  - Retention never removes the last copy of a file.
  - A delete guard stops runs that would empty or gut the backup.
  - Health alerts cover backups that never finished, keep failing, missed their schedule, or have copied nothing for weeks.
  - Windows and Linux skip lists cover caches, temp files, system files, browser caches, other cloud apps' folders, and optional developer and game folders. Mounted drives inside a backed-up folder are never followed.
- Time Portal: browse any backup as it was at any earlier run, with deleted, changed and unchanged files marked, and restore files or folders to where they were (keeping newer copies) or into another folder.
- Every sync and backup keeps `.enterspace/` folders (backup run records, app caches) out of the transfer, as the first rule, so a mirror never deletes a backup's own records. With `delete_excluded` set, records rclone moves aside are copied back after the run.
- Backup run records use the same on-disk format as Enter Space for Apple platforms, so a backup made by a Mac, iPhone or iPad opens in Time Portal here. Compatibility is checked in both directions against the Apple app's own code.

- Mount any rclone remote as a Windows drive letter (WinFsp, network-drive mode) or a Linux folder (FUSE 3), usable from every app and shell.
- Drives connect at sign-in, are health-checked every 30 seconds, re-checked after sleep, and remounted with backoff when they fail. Stale FUSE mounts left by a crash are cleaned up.
- Automatic per-provider tuning: full VFS caching sized from free disk space, a 5-second write-back with retries, rate limits for Drive-class providers, parallelism for object stores, and tuning that follows crypt and alias remotes to the real backend.
- Sync jobs (copy, mirror, move, two-way) that run every N minutes or hours, daily at a set time (catching up after sleep), at sign-in, or when a particular USB drive is connected. Filters, per-job rclone options, live progress, history, and automatic retries after 1, 5 and 15 minutes.
- Settings window for drives, syncs, remotes (rclone's own setup flow, including browser sign-in), `rclone.conf` editing with backup and validation, bandwidth limits and cache size.
- Encrypted `rclone.conf` support, with the password optionally kept in Windows Credential Manager or the Linux keyring.
- Tray icon with drive and sync status, quick connect/disconnect, run-now, and an alert badge.
- `esl` command line: status, connect/disconnect, run/stop syncs, autostart, quit, and the full rclone CLI as `esl rclone`.
- Windows installer (per-user; installs WinFsp only if needed), portable zips, `.deb`/`.rpm` packages and a no-root Linux tarball.
