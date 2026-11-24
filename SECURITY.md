# Security

Please report vulnerabilities privately through GitHub's **Report a vulnerability** button on this repository (Security → Advisories), not in a public issue.

## Threat model, briefly

- The settings window and API listen on `127.0.0.1` on a random port. Clients need either:
  - the bearer token in `api.json`, which lives in the per-user state directory with user-only permissions; or
  - a session cookie obtained through a one-time, two-minute sign-in link.
- Every request must name our loopback host, which blocks DNS rebinding.
- State-changing requests also need an `X-ESL` header and a same-origin `Origin`.
- The UI runs under a strict Content-Security-Policy and never inserts server data as HTML.
- An encrypted `rclone.conf` is unlocked in memory only. The password goes to the OS keychain only if you choose that.
- Bugs in storage backends belong to [rclone](https://github.com/rclone/rclone/security). We update the embedded rclone when it ships security fixes.
