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
// Two properties matter relative to legacyPoolRenderer:
//
//  1. mpb owns its own render goroutine and serializes all writes.
//     Anything written through Writer() (i.e. *mpb.Progress.Write) is
//     interleaved above the live region instead of corrupting it.
//  2. mpb tracks its own line count, so SIGWINCH and stray writes can't
//     desynchronize the cursor accounting that bit the legacy renderer.
//
// Single-line constraint: mpb bars are one line each. The legacy view
// renders the report path as a 2nd line under each "done" row; here we
// inline it after the detail field so the column width stays predictable.
type mpbPoolRenderer struct {
	w        io.Writer
	progress *mpb.Progress

	mu       sync.Mutex
	rows     []poolStatusRow
	bars     []*mpb.Bar // by row index; nil until first Render
	complete []bool     // by row index; true once SetTotal-true has been issued
	closed   bool
}

func newMpbPoolRenderer(w io.Writer) *mpbPoolRenderer {
	// Print the column header once, above the live region. mpb's render
	// loop only walks the cursor up over the bars it owns, so anything
	// written before progress.New starts stays pinned in scrollback
	// above the table — the same place the legacy renderer puts it on
	// the first draw.
	fmt.Fprintln(w, poolStatusHeader)
	return &mpbPoolRenderer{
		w:        w,
		progress: mpb.New(mpb.WithOutput(w)),
	}
}

func (r *mpbPoolRenderer) Render(rows []poolStatusRow) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.rows = clonePoolStatusRows(rows)
	if r.bars == nil {
		r.bars = make([]*mpb.Bar, len(r.rows))
		r.complete = make([]bool, len(r.rows))
		for i := range r.rows {
			r.bars[i] = r.makeBar(i)
		}
	}
	// Mark terminal rows complete so mpb stops re-rendering them and
	// (*Progress).Wait can return when everything finishes.
	for i, row := range r.rows {
		if r.complete[i] || r.bars[i] == nil {
			continue
		}
		if row.State == poolStateDone || row.State == poolStateError {
			r.bars[i].SetTotal(-1, true)
			r.complete[i] = true
		}
	}
}

func (r *mpbPoolRenderer) Writer() io.Writer { return r.progress }
func (r *mpbPoolRenderer) Interleaves() bool { return true }
func (r *mpbPoolRenderer) Trimmed() bool     { return false }

func (r *mpbPoolRenderer) Close() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	// Force any still-incomplete bars to terminal so Wait can return.
	for i, bar := range r.bars {
		if bar != nil && !r.complete[i] {
			bar.SetTotal(-1, true)
			r.complete[i] = true
		}
	}
	r.mu.Unlock()
	r.progress.Wait()
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

// formatPoolRowSingleLine renders a poolStatusRow as one line of text,
// matching the legacy poolStatusRowFmt columns. When the row has a Path
// (terminal "done" rows), the path is appended after the detail with an
// arrow separator instead of placed on a 2nd line — mpb bars are
// single-line.
func formatPoolRowSingleLine(row poolStatusRow) string {
	execID := "-"
	if row.ExecID > 0 {
		execID = strconv.FormatInt(row.ExecID, 10)
	}
	calls := "-"
	if row.State != poolStateQueued || row.Calls > 0 {
		calls = strconv.Itoa(row.Calls)
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
	return strings.TrimRight(fmt.Sprintf(poolStatusRowFmt, execID, row.Label, row.State, est, calls, detail), " ")
}
