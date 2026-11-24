# Contributing

Thanks for helping. Bug reports with logs (Settings → Log, or **Open log folder**) are the most useful thing you can send.

## Code contributions

Enter Space Light is GPL-3.0. To keep the option of using contributed code in other editions of Enter Space, every contribution is also licensed to the maintainer under the [Contributor License Agreement](CLA.md). By opening a pull request you agree to it. If you can't agree to it, please open an issue describing the change instead.

Before opening a pull request:

```bash
gofmt -l .                     # must print nothing
go test -race ./...
go test -tags e2e -run TestE2E ./internal/daemon
```

- Keep it light. This is deliberately the "mapped network drive" edition: mounting, syncing, and getting out of the way. Media libraries, backups, photo timelines and the like belong in the full Enter Space apps and won't be accepted here.
- Leave rclone to do the storage work. Add tuning in `internal/tune` (with a test that the option names exist in rclone), not by patching rclone.
- Anything that touches the local API must keep the protections in `internal/daemon/http.go`: the host check, the bearer or session check, `X-ESL` on writes, and CSP.
- Never write outside `ESL_HOME` when it is set. The tests rely on that sandbox.
