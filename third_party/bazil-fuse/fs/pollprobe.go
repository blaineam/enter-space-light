// Added by Enter Space Light; not part of upstream bazil.org/fuse.

package fs

import (
	"context"

	"bazil.org/fuse"
)

// PollProbeName is a file the server itself answers for in the root of every
// mount. It is never listed and the FS being served never sees it. Opening
// and polling it right after mounting makes the kernel ask its one POLL
// question at a moment when nothing can deadlock.
const PollProbeName = ".esl-poll-probe"

type pollProbeNode struct{}

var pollProbe Node = &pollProbeNode{}

func (*pollProbeNode) Attr(_ context.Context, a *fuse.Attr) error {
	a.Valid = 0
	a.Mode = 0o444
	return nil
}
