# Changelog

All notable changes to Enter Space Light. Versions follow [Semantic Versioning](https://semver.org).

## [Unreleased]

## [0.1.0] - unreleased

First version, for Windows (x64, Arm) and Linux (x86_64, arm64).

- Mount any rclone remote as a Windows drive letter (WinFsp, network-drive mode) or a Linux folder (FUSE 3), usable from every app and shell.
- Drives connect at sign-in, are health-checked every 30 seconds, re-checked after sleep, and remounted with backoff when they fail. Stale FUSE mounts left by a crash are cleaned up.
- Automatic per-provider tuning: full VFS caching sized from free disk space, a 5-second write-back with retries, rate limits for Drive-class providers, parallelism for object stores, and tuning that follows crypt and alias remotes to the real backend.
- Sync jobs (copy, mirror, move, two-way) that run every N minutes or hours, daily at a set time (catching up after sleep), at sign-in, or when a particular USB drive is connected. Filters, per-job rclone options, live progress, history, and automatic retries after 1, 5 and 15 minutes.
- Settings window for drives, syncs, remotes (rclone's own setup flow, including browser sign-in), `rclone.conf` editing with backup and validation, bandwidth limits and cache size.
- Encrypted `rclone.conf` support, with the password optionally kept in Windows Credential Manager or the Linux keyring.
- Tray icon with drive and sync status, quick connect/disconnect, run-now, and an alert badge.
- `esl` command line: status, connect/disconnect, run/stop syncs, autostart, quit, and the full rclone CLI as `esl rclone`.
- Windows installer (per-user; installs WinFsp only if needed), portable zips, `.deb`/`.rpm` packages and a no-root Linux tarball.
