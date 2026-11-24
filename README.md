<p align="center"><img src="assets/icon-128.png" width="96" alt=""></p>

# Enter Space Light

**Your cloud storage as ordinary drives on Windows and Linux. Free and open source.**

Enter Space Light mounts any [rclone](https://rclone.org) remote (Google Drive, OneDrive, Dropbox, S3, Backblaze B2, SFTP, SMB, WebDAV and about 70 more) as a normal drive. On Windows it is a drive letter in File Explorer, like a mapped network drive. On Linux it is a folder. Every application, shell and script can use it.

It also runs **backups and syncs**: every few hours, daily at a set time, at sign-in, or **whenever a particular USB drive is plugged in**. Backups keep earlier versions of changed and deleted files, and **Time Portal** lets you browse a backup as it was at any earlier moment and bring files back.

It starts at sign-in, reconnects drives on its own, retries failed uploads and syncs, and tunes rclone for each provider so you don't have to. There is a tray icon and a small settings window. Everything else happens in the file manager you already use.

> Looking for photo, video and music libraries, backups, and the Files app on iPhone, iPad, Mac and Apple TV? That's [Enter Space](https://wemiller.com/apps/enter-space/). Enter Space Light is the light, free sibling for Windows and Linux.

## Install

### Windows 10 and 11 (x64 and Arm)

Download `EnterSpaceLight-<version>-windows-<arch>-setup.exe` from [Releases](https://github.com/blaineam/enter-space-light/releases) and run it. It installs for your user account and needs no administrator rights, except for one step. If [WinFsp](https://winfsp.dev) (the free filesystem driver that makes drive letters possible) isn't installed yet, the installer adds it, and Windows asks for permission for that step only.

A portable `.zip` is also available. Without the installer, install WinFsp yourself (`winget install WinFsp.WinFsp`), or let the app do it from its settings window.

### Linux (x86_64 and arm64)

- **Debian, Ubuntu, Mint, Pop!_OS:** `sudo apt install ./enter-space-light_<version>_amd64.deb`
- **Fedora, RHEL, openSUSE:** `sudo dnf install ./enter-space-light-<version>.x86_64.rpm`
- **Anything else:** unpack the `.tar.gz` and run `./install.sh` (installs to `~/.local`, no root). You also need FUSE 3 (`fuse3` on most distros).

Then start **Enter Space Light** from your app menu.

## First run

1. **Add a remote.** This is the cloud account or server. Providers that sign in through a browser (Google Drive, OneDrive, Dropbox…) open one. Already use rclone? Your existing `rclone.conf` is picked up automatically: it's the same file.
2. **Add a drive** for that remote. It connects immediately and again every time you sign in.
3. Optionally **add a sync**: for example, copy `Pictures` to B2 every night, or mirror a cloud folder to a USB stick whenever it's plugged in.

Start-at-sign-in is turned on the first time you run it. You can turn it off in Settings.

## Backups and Time Portal

**Set up a backup** walks you through five steps:

1. **What:** your whole user folder (recommended), chosen folders, or a whole drive.
2. **Where:** a cloud remote (encrypted ones are listed first), a USB drive, or a folder or network share.
3. **What to skip:** temporary files and caches, browser caches, system files, folders that OneDrive, Dropbox or iCloud already keep online, and optionally developer and game folders.
4. **When and for how long:** the schedule (daily, several times a day, or when the drive is plugged in) and how long to keep old versions.
5. **Review.** It can then set up a second copy somewhere else (one at home, one in the cloud).

How it protects you:

- **Old versions are kept.** A file that is changed or deleted is moved into a dated folder under `.previous-versions` next to the backup, instead of being lost.
- **Retention never removes the last copy.** Expired versions are cleaned up, but never the only remaining copy of a deleted file.
- **A delete guard.** If the source is suddenly empty, or a run would delete more than a quarter of the backup (an unplugged drive, a moved folder), the run stops before deleting anything and tells you.
- **A health check.** You're warned when a backup has never finished, keeps failing, has missed its schedule, or has copied nothing for weeks.

**Time Portal** shows a backup as it was at any earlier backup. Each item is marked **Deleted since**, **Changed since** or **Unchanged**. Select files or folders and put them back where they were (a newer copy is kept beside the restored one), or restore them into a separate folder.

**Works with Enter Space on Apple devices.** Every run writes a small record to the backup (`.enterspace/manifests`) in the same format Enter Space uses on Mac, iPhone and iPad. A backup made by a Mac can be browsed and restored here (**Open another backup…**). The format is shared, so the Apple apps can read these records too, though opening a Windows or Linux backup there needs a matching option in those apps.

## What "tuned automatically" means

You never have to know these flags exist. Anything you set in a drive's or sync's *Advanced* box wins.

- **Full VFS caching** (`--vfs-cache-mode full`), so every program gets normal file behaviour: random writes, seeking, renaming over files, memory-mapped reads. The cache size comes from your free disk space (a fifth of it, split across drives, between 1 GB and 50 GB) and never fills the disk.
- **Write-back of 5 seconds, never 0.** A write-back of 0 makes rclone upload inside `close()` with no queue and no retry. At 5 seconds a failed upload is retried in the background.
- **Per-provider limits.** Google Drive, OneDrive, Dropbox and similar get request-rate limits so they don't throttle you. Object stores (S3, B2, GCS, Azure) get more parallel transfers and fast listing. Mega gets gentle settings. Encrypted (`crypt`) and `alias` remotes are tuned for the storage underneath them.
- **Directory caching that matches the provider.** It is long where the provider reports changes, short where files change behind rclone's back (SFTP, SMB, S3).
- **Windows drives are network drives.** Explorer doesn't write thumbnail caches into them, reports free space correctly and has no recycle bin there.
- **Retries everywhere.** Connection-level retries, then rclone's own retries, then Enter Space Light retries failed syncs after 1, 5 and 15 minutes. Configuration mistakes (a remote that no longer exists) are reported instead of retried.

## Reliability

- A watchdog checks each drive every 30 seconds with a timeout, so a dead server can never hang it. After two failures in a row the drive is remounted, with backoff (5 s → 5 min).
- After the computer wakes from sleep, every drive is checked straight away.
- On Linux, a mount left behind by a crash ("Transport endpoint is not connected") is cleaned up automatically.
- Missed schedules catch up: a nightly sync the laptop slept through runs once on wake, not seven times.
- Only one copy runs per user. Launching it again just opens the window.

## Command line

`esl` controls the running app and is handy in scripts:

```
esl                      open the window (starting the app if needed)
esl status               drives and syncs at a glance
esl connect "Google Drive"      esl disconnect "Google Drive"
esl sync "Pictures backup"      esl stop "Pictures backup"
esl autostart on|off [--headless]
esl quit
esl rclone <anything>    full rclone, same rclone.conf (e.g. esl rclone config)
```

`esl run --no-tray` runs without a tray icon, for servers. `esl autostart on --headless` installs it as a systemd user service.

## Your rclone.conf

Enter Space Light uses rclone's standard `rclone.conf`, so it works side by side with the `rclone` command:

- Edit it in the window (**Remotes → Edit rclone.conf**). A backup is kept, and changes rclone can't read are refused.
- Or run `esl rclone config` for rclone's full interactive editor.
- Encrypted configs are supported. You're asked for the password when the app starts, and you can save it in Windows Credential Manager or your Linux keyring so it unlocks by itself at sign-in.

App settings (drives, syncs, preferences) live separately in `settings.json`: `%APPDATA%\EnterSpaceLight` on Windows, `~/.config/enter-space-light` on Linux.

## Privacy and security

- No accounts, no telemetry, no servers of ours. It talks only to the storage you configure.
- The settings window is served on `127.0.0.1` only and is protected by:
  - a random token that changes every run;
  - a one-time sign-in link;
  - an `HttpOnly`, `SameSite=Strict` session cookie;
  - host checks that block DNS rebinding;
  - a cross-site request guard;
  - a strict Content-Security-Policy.

  Other users on the same machine can't use it.
- See [SECURITY.md](SECURITY.md) to report a vulnerability.

## Building

Go 1.27 or newer, no C compiler:

```bash
./scripts/build.sh 0.1.0                 # all targets into dist/
go test -race ./...                      # unit tests
go test -tags e2e -run TestE2E ./internal/daemon   # end-to-end in a sandbox
```

Windows builds need `-tags cmount`; `build.sh` handles it.

The QA gate is `soren.config.mjs`: unit, end-to-end, cross-target vet, Apple manifest compatibility, the UI script, and `real-mounts`, which requires the GitHub Actions run for the same commit, where Windows (WinFsp) and Linux (FUSE) mount real drives, to be green. `ESL_HOME=/some/dir` sandboxes everything (settings, cache, `rclone.conf`, mount folders) for development. See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## License

GPL-3.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE). "Enter Space" and its icon are trademarks; forks must use their own name and icon ([TRADEMARKS.md](TRADEMARKS.md)). Contributions are welcome under the [contributor agreement](CONTRIBUTING.md).

WinFsp - Windows File System Proxy, Copyright (C) Bill Zissimopoulos — <https://github.com/winfsp/winfsp>
