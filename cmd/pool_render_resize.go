package cmd

import (
	"io"
	"sync"
)

// resizeAwareWriter wraps an io.Writer (typically the renderer's
// underlying os.Stdout) and, on a SIGWINCH, prepends an ANSI sequence
// to the next Write that wipes the viewport + scrollback and re-emits
// the live table's column header.
//
// Why this is necessary: mpb's frame redraw is "write N lines, cursor
// up N". When the terminal shrinks between frames, the previous
// frame's top rows have already scrolled into scrollback and a
// cursor-up-N walk can't reach them. The result is the same "stacked
// rows" symptom the legacy renderer suffered from — frames pile up
// instead of overwriting cleanly.
//
// By emitting `\033[H\033[2J\033[3J` (cursor home, clear viewport,
// clear scrollback — the latter is the xterm extension Apple Terminal,
// iTerm2, and most modern terminals support) plus the column header on
// the first Write after a resize, the next mpb frame lands in a fresh
// viewport with the cursor at row 2. mpb's existing cursor-up math
// then works correctly because there is nothing left in scrollback
// for it to fail to reach.
//
// Tradeoff: any interleaved log lines (e.g. agent.Warnf output routed
// through mpb.Progress.Write) that arrived between the previous frame
// and the resize get wiped. They remain available in stderr capture
// files, so this is acceptable for the "stop the corruption" goal.
type resizeAwareWriter struct {
	inner  io.Writer
	header []byte // <header>\n, ready to write

	mu      sync.Mutex
	pending bool
}

func newResizeAwareWriter(inner io.Writer, header string) *resizeAwareWriter {
	return &resizeAwareWriter{
		inner:  inner,
		header: append([]byte(header), '\n'),
	}
}

// markResized signals that the next Write should be preceded by a
// clear+header sequence. Safe to call concurrently with Write.
func (r *resizeAwareWriter) markResized() {
	r.mu.Lock()
	r.pending = true
	r.mu.Unlock()
}

func (r *resizeAwareWriter) Write(p []byte) (n int, err error) {
	r.mu.Lock()
	pending := r.pending
	r.pending = false
	r.mu.Unlock()
	if pending {
		// \x1b[H  – cursor home
		// \x1b[2J – erase viewport
		// \x1b[3J – erase scrollback (xterm extension)
		if _, err := r.inner.Write([]byte("\x1b[H\x1b[2J\x1b[3J")); err != nil {
			return 0, err
		}
		if _, err := r.inner.Write(r.header); err != nil {
			return 0, err
		}
	}
	return r.inner.Write(p)
}
