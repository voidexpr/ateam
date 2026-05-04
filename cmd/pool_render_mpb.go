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

	resizeStop func()
	resizeDone sync.WaitGroup
}

func newMpbPoolRenderer(w io.Writer) *mpbPoolRenderer {
	// Print the column header once, above the live region. Pass the
	// underlying writer to mpb directly (no wrapping): mpb's terminal
	// detection is a type assertion to *os.File — wrapping os.Stdout
	// hides it, mpb then thinks output isn't a TTY and disables auto
	// refresh, which means no bars ever render. Resize is handled via
	// the Progress.Write interleave path in onResize below instead of
	// by intercepting the output.
	fmt.Fprintln(w, poolStatusHeader)
	r := &mpbPoolRenderer{
		w:        w,
		progress: mpb.New(mpb.WithOutput(w)),
	}
	r.subscribeResize()
	return r
}

func (r *mpbPoolRenderer) subscribeResize() {
	ch, stop := subscribeWindowResize()
	if ch == nil {
		return
	}
	r.resizeStop = stop
	r.resizeDone.Add(1)
	go func() {
		defer r.resizeDone.Done()
		for range ch {
			r.onResize()
		}
	}()
}

// onResize injects a clear-screen + clear-scrollback + re-emitted
// header through mpb's interleave channel. mpb queues the bytes and
// emits them at the start of the next refresh tick, ahead of the bar
// frame, so the next frame lands in a fresh viewport with the header
// pinned at row 1.
//
//	\x1b[H  – cursor home
//	\x1b[2J – erase viewport
//	\x1b[3J – erase scrollback (xterm extension; supported by Apple
//	          Terminal, iTerm2, modern xterm, alacritty, kitty, …)
//
// Tradeoff: any interleaved log lines that were queued ahead of this
// resize escape get clobbered by the clear. They remain available in
// stderr capture files, so this is acceptable for the "stop the
// corruption" goal.
func (r *mpbPoolRenderer) onResize() {
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return
	}
	_, _ = r.progress.Write([]byte("\x1b[H\x1b[2J\x1b[3J" + poolStatusHeader + "\n"))
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
	stop := r.resizeStop
	r.resizeStop = nil
	r.mu.Unlock()
	if stop != nil {
		stop()
	}
	r.resizeDone.Wait()
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
