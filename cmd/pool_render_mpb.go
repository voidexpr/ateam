package cmd

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/ateam/internal/display"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
)

// mpbPoolRenderer drives the live status table via mpb (vbauerster/mpb).
// One bar per role; each bar's content is supplied by a single decor.Any
// decorator that pulls the row's formatted line from a shared snapshot.
//
// Two properties this gives us:
//
//  1. mpb owns its own render goroutine and serializes all writes.
//     Anything written through Writer() (i.e. *mpb.Progress.Write) is
//     interleaved above the live region — runPool plugs the
//     std-stream redirect into this so every Go-side write to
//     os.Stdout / os.Stderr lands above the bars instead of corrupting
//     the cursor accounting.
//  2. mpb tracks its own line count and requeries terminal size on
//     each refresh tick, so the live viewport self-corrects after a
//     SIGWINCH without explicit handling on our side.
//
// Single-line constraint: mpb bars are one line each. Where a row has
// a report path (terminal "done" rows), the path is inlined after the
// detail field with an arrow separator so the column widths stay
// predictable.
type mpbPoolRenderer struct {
	w        io.Writer
	progress *mpb.Progress

	mu     sync.Mutex
	rows   []poolStatusRow
	bars   []*mpb.Bar // by row index; nil until first Render
	closed bool
}

// newMpbPoolRenderer constructs the renderer.
//
// Resize handling: deliberately none. mpb requeries terminal size on
// every refresh tick (cwriter handles it), so the live viewport
// self-corrects after a SIGWINCH without any extra work from us. A
// previous version emitted \x1b[3J (erase-scrollback) on SIGWINCH to
// also wipe stale rows pushed into scrollback by a viewport shrink —
// that fixed the cosmetic "stale rows above the viewport" issue but
// destroyed the operator's pre-run scrollback, which is far more
// valuable. We accept that scrollback may contain stale frame rows
// from before a resize-shrink; this is expected for cursor-arithmetic
// rendering and the operator only sees them if they scroll up. If
// scrollback hygiene becomes important, switch to alt-screen mode
// (\x1b[?1049h / \x1b[?1049l) — that's a larger change because the
// post-run summary in runPool would have to move out of the
// alt-screen region.
func newMpbPoolRenderer(w io.Writer) *mpbPoolRenderer {
	// Print the column header once, above the live region. Pass the
	// underlying writer to mpb directly (no wrapping): mpb's terminal
	// detection is a type assertion to *os.File — wrapping os.Stdout
	// hides it and mpb disables auto refresh, which means no bars
	// ever render.
	fmt.Fprintln(w, poolStatusHeader)
	return &mpbPoolRenderer{
		w:        w,
		progress: mpb.New(mpb.WithOutput(w)),
	}
}

func (r *mpbPoolRenderer) Render(rows []poolStatusRow) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.rows = clonePoolStatusRows(rows)
	if r.bars == nil {
		r.bars = make([]*mpb.Bar, len(r.rows))
		for i := range r.rows {
			r.bars[i] = r.makeBar(i)
		}
	}
	// Collect terminal bars under the lock; call SetTotal AFTER unlocking.
	// SetTotal is a blocking send to the bar's serve goroutine, which can
	// be mid-draw and waiting on r.mu via formatRow — holding r.mu across
	// the call deadlocks. SetTotal is idempotent so re-issuing on each
	// Render is harmless.
	var done []*mpb.Bar
	for i, row := range r.rows {
		if r.bars[i] == nil {
			continue
		}
		if row.State == poolStateDone || row.State == poolStateError {
			done = append(done, r.bars[i])
		}
	}
	r.mu.Unlock()

	for _, bar := range done {
		bar.SetTotal(-1, true)
	}
}

func (r *mpbPoolRenderer) Writer() io.Writer { return r.progress }

func (r *mpbPoolRenderer) Close() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	bars := append([]*mpb.Bar(nil), r.bars...)
	r.mu.Unlock()

	// SetTotal is a blocking send to each bar's serve loop, which can be
	// mid-draw and waiting on r.mu via formatRow. Call it OUTSIDE the
	// lock so the bar can drain its operateState channel. SetTotal is
	// idempotent for already-completed bars.
	for _, bar := range bars {
		if bar != nil {
			bar.SetTotal(-1, true)
		}
	}
	r.progress.Wait()

	// Note on cursor position after Wait: mpb's cwriter buffers a
	// `cursor-up <barCount>` escape at the END of each Flush — but it
	// only sends that escape at the START of the *next* Flush. On
	// shutdown there is no next Flush, so the buffered cursor-up is
	// never emitted. The cursor is therefore left at the end of the
	// last bar frame (one line below the last bar), which is exactly
	// where we want it: subsequent writes by runPool — the post-run
	// summary, failure tails — land cleanly below the persisted bars
	// without us needing to position the cursor explicitly.
}

// makeBar constructs the bar for row i. The decorator closes over r
// and reads the current row state under the renderer's mutex.
func (r *mpbPoolRenderer) makeBar(i int) *mpb.Bar {
	return r.progress.New(0, mpb.NopStyle(),
		mpb.BarFillerTrim(),
		mpb.PrependDecorators(
			decor.Any(func(_ decor.Statistics) string {
				return r.formatRow(i)
			}),
		),
	)
}

func (r *mpbPoolRenderer) formatRow(i int) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i >= len(r.rows) {
		return ""
	}
	return formatPoolRowSingleLine(r.rows[i])
}

// formatPoolRowSingleLine renders a poolStatusRow as one line of text
// using the shared poolStatusRowFmt columns. When the row has a Path
// (terminal "done" rows), the path is appended after the detail with
// an arrow separator: mpb bars are single-line, so we can't place the
// path on a second row.
func formatPoolRowSingleLine(row poolStatusRow) string {
	execID := "-"
	if row.ExecID > 0 {
		execID = strconv.FormatInt(row.ExecID, 10)
	}
	turns := "-"
	if row.State != poolStateQueued || row.Turns > 0 {
		turns = strconv.Itoa(row.Turns)
	}
	est := ""
	if row.EstTokens > 0 {
		est = display.FmtTokens(int64(row.EstTokens))
	}
	detail := row.Detail
	if row.Path != "" {
		if detail != "" {
			detail += "  → " + row.Path
		} else {
			detail = row.Path
		}
	}
	return strings.TrimRight(fmt.Sprintf(poolStatusRowFmt, execID, row.Label, row.State, est, turns, detail), " ")
}
