# Patched copy of bazil.org/fuse

Upstream: `bazil.org/fuse v0.0.0-20230120002735-62a210ff1fd5` (BSD licence, see
`LICENSE`). Only the packages the Linux build uses are here, without tests.
`go.mod` in the repo root points at this folder with a `replace` line.

## Why

A Go program that opens a file on a FUSE drive it serves itself can freeze
for good, and `kill -9` can't end it. Go adds each file it opens to epoll. On
FUSE that can send the server a POLL request while the thread keeps its Go
processor. If the server needs that processor (or the garbage collector is
stopping the world), neither side moves. Syncs and backups that use one of the
app's own drives do exactly this.

## The two changes (both in `fs/`)

1. `serve.go`: an unimplemented POLL is answered `ENOSYS` instead of "always
   ready". The kernel then never sends POLL again for that mount.
2. `serve.go` and `pollprobe.go`: the root answers a lookup of
   `.esl-poll-probe`. `internal/linuxmount` opens and polls it with plain
   system calls right after mounting, so the one POLL the kernel does send
   arrives safely. `go-fuse` uses the same technique.

`internal/linuxmount/mount_test.go` freezes without these changes. When
updating rclone, keep this copy at the version rclone's `go.mod` requires.
