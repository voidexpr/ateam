package cmd

import (
	"io"
	"os"
)

// poolRenderer is the abstraction over the live multi-row status table
// that runPool drives. There is currently one implementation
// (mpbPoolRenderer); the interface is kept because the seam helps tests
// and leaves room for an alt-screen variant without rewriting call
// sites.
//
// The contract:
//
//   - Render is idempotent on rows. The first call performs whatever
//     initial setup the implementation needs (printing the header).
//     Subsequent calls update the live region in place. runPool calls
//     it on every progress event and on each completion.
//   - Writer returns an io.Writer for stray output that should be
//     interleaved above the live region. runPool plugs this into
//     redirectStdStreams so every Go-side write to os.Stdout / os.Stderr
//     is captured and routed through the renderer instead of corrupting
//     the live region.
//   - Close releases resources (mpb's render goroutine, etc.). Safe to
//     call multiple times.
type poolRenderer interface {
	Render(rows []poolStatusRow)
	Writer() io.Writer
	Close()
}

// newPoolRenderer constructs the renderer runPool drives when stdout
// is a terminal and !opts.quiet. Non-TTY callers must skip this and
// fall through to the plain printProgress path; mpb auto-disables ANSI
// rendering on non-*os.File writers and would silently buffer
// everything otherwise.
func newPoolRenderer(w io.Writer) poolRenderer {
	if w == nil {
		w = os.Stdout
	}
	return newMpbPoolRenderer(w)
}
