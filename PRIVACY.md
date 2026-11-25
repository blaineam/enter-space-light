# Privacy policy

This program will not transfer any information to other networked systems unless specifically requested by the user or the person installing or operating it.

- No accounts, no analytics, no telemetry, no crash reporting and no automatic update checks. The project runs no servers.
- Enter Space Light connects to the storage you configure (your cloud accounts and servers) to mount, sync, back up and restore. When you sign in to a provider through your browser, rclone's sign-in flow talks to that provider. What those services do with your data is covered by their own privacy policies.
- If you ask the app to install the WinFsp driver on Windows, it uses the Windows Package Manager (`winget`), which downloads it from the sources Windows is set up to use.
- Settings, logs, caches and `rclone.conf` stay on your computer. A saved configuration password is kept in Windows Credential Manager or your Linux keyring.
- Backups write a record of each run (file names, sizes, dates and checksums) into the backup destination you chose.

Questions: [open an issue](https://github.com/blaineam/enter-space-light/issues), without personal data.
