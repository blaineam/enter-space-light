// soren.config.mjs — QA suites for Enter Space Light (Windows + Linux).
//
// Run:  node ../../_shared/soren/soren.mjs run "Enter Space Light"
//       node ../../_shared/soren/soren.mjs run "Enter Space Light" e2e
//
// Everything here is hermetic: ESL_HOME sandboxes settings, cache, rclone.conf
// and mount folders, and the e2e test fails if a decoy RCLONE_CONFIG is ever
// touched. Real drive mounts need WinFsp/FUSE, which this Mac doesn't have, so
// `real-mounts` gates on the GitHub Actions run for the same commit, where
// both platforms mount for real.
export default {
  name: 'Enter Space Light',
  suites: {
    // Settings validation, tuning (every option name checked against rclone's
    // registry), mount reconcile/watchdog/backoff/sleep, sync runner retries,
    // scheduler catch-up, volume detection, backup manifests, point-in-time
    // resolver, retention, health rules.
    unit: { type: 'cmd', cmd: 'go', args: ['test', '-race', '-count=1', './...'],
            description: 'Go unit tests (race detector)', tags: ['regression'] },
    // The real daemon + embedded rclone in a sandbox: API security, one-time
    // login, filtered copy, two-way sync, backups with versions and run
    // records, Time Portal browse + restore, delete guard, delete_excluded
    // record recovery, health alerts.
    e2e: { type: 'cmd', cmd: 'go', args: ['test', '-tags', 'e2e', '-count=1', '-run', 'TestE2E', './internal/daemon'],
           description: 'End-to-end daemon in a sandbox (no mount on macOS)', tags: ['regression'] },
    // go vet for windows/linux amd64+arm64 (+ darwin dev build), cgo off.
    'cross-vet': { type: 'cmd', cmd: './scripts/vet-all.sh', description: 'go vet on every release target' },
    // Backup run records stay interchangeable with Enter Space for Apple
    // platforms: compiles the Apple app's own BackupManifest.swift and checks
    // both directions. Needs the Enter Space repo checked out beside this one.
    'apple-compat': { type: 'cmd', cmd: './scripts/check-apple-compat.sh',
                      description: 'Manifest format vs the Apple app codec, both directions' },
    ui: { type: 'node-check', files: ['internal/ui/web/app.js'], description: 'Settings window script parses' },
    // Real WinFsp drives (Windows) and FUSE mounts (Linux), from CI.
    'real-mounts': { type: 'cmd', cmd: './scripts/ci-real-mounts.sh',
                     description: 'GitHub Actions: real-mount e2e on Windows + Linux for HEAD' },
  },
  release: { requireGreen: ['unit', 'e2e', 'cross-vet', 'apple-compat', 'ui', 'real-mounts'] },
};
