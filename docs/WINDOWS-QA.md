# Windows real-machine test

CI already mounts a real WinFsp drive and runs backup, Time Portal and the
delete guard on a Windows Server runner. This checklist covers what CI can't:
the installer, Explorer, the tray, sign-in, notifications, USB drives and
sleep, on a real Windows 10 or 11 PC.

You can give this whole file to Claude Code running on the PC: "Follow
docs/WINDOWS-QA.md from github.com/blaineam/enter-space-light and report the
results."

## Rules for the tester (human or AI)

- Test the **published installer** from the latest GitHub release, not a local
  build.
- Use only throwaway data in `C:\esl-test`. Never back up, sync or mirror a
  real folder, and never run a mirror whose destination holds real files.
- If `%APPDATA%\rclone\rclone.conf` already exists, copy it to
  `rclone.conf.before-esl-test` first. Add only test remotes, and remove them
  at the end. Never print the file: it can hold passwords and tokens.
- Don't put usernames, tokens, remote names of real accounts or other personal
  data in the report or in issues.
- Ask the person before anything that restarts, sleeps or signs out the PC,
  and before approving a Windows permission (UAC) prompt: they approve it.
- Mark a step **PASS** only if you observed the result. Otherwise mark it
  **FAIL** (with what happened and the log) or **NOT RUN** (with why).

Logs: `%LOCALAPPDATA%\EnterSpaceLight\logs\esl.log`. The command line is
`%LOCALAPPDATA%\Programs\Enter Space Light\esl.exe`.

## 0. Machine

Record the Windows edition and build (`winver`), the CPU architecture, and
whether WinFsp or rclone were already installed.

## 1. Install

1. Download `EnterSpaceLight-<v>-windows-<arch>-setup.exe` and `SHA256SUMS`
   from the latest release. Check the hash with `Get-FileHash`.
2. Run the installer. SmartScreen will warn, because the build isn't
   code-signed yet. Note the wording, then **More info → Run anyway**.
3. The app should install without administrator rights. If WinFsp is
   missing, only the WinFsp step should ask for permission.
4. The window opens (an Edge app window, no tabs or address bar), a tray icon
   appears, and **no console window** appears or flashes.

## 2. A drive in Explorer

1. Create `C:\esl-test\remote\docs\hello.txt` with known text.
2. In the app: Remotes → Add remote → "alias", name `esltest`, remote
   `C:\esl-test\remote`.
3. Add a drive for `esltest:` with automatic letter and auto-connect.
4. Check: a new drive letter under **Network locations** in Explorer, labelled
   with the drive name, with plausible free space.
5. In Explorer: open `hello.txt` in Notepad, edit and save; create a folder;
   rename and delete files; drag a 500 MB file in (make one with
   `fsutil file createnew`). After about 10 seconds the same changes must
   exist under `C:\esl-test\remote`, and the large file's SHA-256 must match.
6. From **both** cmd and PowerShell: `dir`, `type`, `copy`, `robocopy`.
7. Open the drive from an app that isn't Explorer (an editor, a media player
   or an image viewer) and save a file from it.
8. Disconnect and reconnect from the tray menu, then from `esl disconnect` /
   `esl connect`.
9. A second drive with a chosen letter, and one with a **folder** mount point.
10. Choose a letter that is already in use: expect a clear error, not a hang.

## 3. Resilience

1. With a drive connected, end `EnterSpaceLight.exe` in Task Manager. Explorer
   must not hang. Start the app again: the drive returns on the same letter.
2. Rename `C:\esl-test\remote` away, wait 90 seconds and rename it back. Record
   what the drive and the app report, and whether it recovers without help.
3. (Ask first) Sleep the PC for 2 minutes and wake it: the drive works within
   a minute.
4. (Ask first) Restart the PC and sign in: the app starts by itself with no
   console window, and the auto-connect drive appears.
5. Launch the app while it's already running: the window opens, with no
   second tray icon.

## 4. Backup and Time Portal

1. Fill `C:\esl-test\source` with at least 120 small files in a few folders,
   including names with spaces and non-ASCII characters.
2. **Set up a backup** → Only some folders → Another folder
   `C:\esl-test\source` → A folder or network share →
   `C:\esl-test\backups\PC`. Run it. The files and `.enterspace\manifests`
   must appear in the destination.
3. Change one file, delete one folder, and run again. The old versions must be
   under `C:\esl-test\backups\.previous-versions\…`, and **both** run records
   must still be in `.enterspace\manifests`.
4. Time Portal → step back to the first run: the deleted folder shows
   **Deleted since** and the changed file **Changed since**.
5. Restore the deleted folder **to where it was**, and the changed file into a
   **separate folder**. Check the contents. Restoring over a newer file must
   keep both.
6. Delete most of the source (more than a quarter) and run: the backup must be
   **stopped** with a clear message, nothing deleted from the destination, and
   a Windows notification shown.
7. Back up to the connected `esltest:` drive's letter or to `esltest:backup/x`
   and confirm Time Portal works there too.

## 5. Schedules and USB

1. A copy sync with a 1-minute interval runs on its own.
2. A daily sync set two minutes ahead fires at that time.
3. (Needs the person) With a spare USB stick: create a sync that runs when it
   is connected, with `{volume}` as the destination. Unplug it and plug it back
   in. The sync starts within about 15 seconds, once. Plug it into another
   port, or let it get another letter: it still matches.

## 6. Settings and encrypted config

1. Remotes → Edit rclone.conf: make a harmless edit and save. A `.bak` exists.
2. Only if there was no rclone.conf before this test: set a configuration
   password with `esl rclone config`, restart the app, unlock it with
   "Remember" on, restart again (it unlocks by itself), and check the entry in
   Windows Credential Manager. Then remove the password.
3. Turn "Start when I sign in" off and on, and check
   `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`.

## 7. Uninstall and clean up

1. Uninstall from Windows Settings. The app quits, drives disconnect, and the
   Run entry is gone. WinFsp stays installed.
2. Remove the test remotes (or restore `rclone.conf.before-esl-test`) and
   `C:\esl-test`.

## Report

Reply with a table of every numbered step (PASS / FAIL / NOT RUN and a short
note), then the failures in detail with the relevant log lines (personal data
removed). If the person asks, open one GitHub issue per failure.
