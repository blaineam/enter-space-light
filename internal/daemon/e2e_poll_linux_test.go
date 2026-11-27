//go:build e2e && linux

package daemon_test

import (
	"testing"

	"golang.org/x/sys/unix"
)

// primePoll makes it safe for this process to open files on a drive it is
// serving itself, which only a test does.
//
// Go registers every file it opens with epoll, which on FUSE asks the
// filesystem server a POLL question and holds a Go processor while it waits.
// If the garbage collector stops the world just then, the server (this same
// process) can't answer, and both wait for ever (CI froze this way). The
// server answers "not supported" once and the kernel never asks again, so
// ask that first question with plain system calls, which release the
// processor.
func primePoll(t *testing.T, file string) {
	t.Helper()
	fd, err := unix.Open(file, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("opening %s through the mount: %v", file, err)
	}
	defer func() { _ = unix.Close(fd) }()
	if _, err := unix.Poll([]unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}, 5000); err != nil && err != unix.EINTR {
		t.Fatalf("polling %s: %v", file, err)
	}
}
