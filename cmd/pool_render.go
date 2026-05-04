package cmd

import (
	"io"
	"os"
	"sync"
)

// poolRenderer is the abstraction over the live multi-row status table
// that runPool drives. Implementations may use ANSI cursor arithmetic
// (legacyPoolRenderer), a TUI library (mpbPoolRenderer, follow-up),
// or anything else; runPool only sees the interface.
//
// The contract:
//
//   - Render is idempotent on rows. The first call performs whatever
//     initial setup the implementation needs (printing the header,
//     subscribing to SIGWINCH, …). Subsequent calls update the live
//     region in place. runPool calls it on every progress event,
//     completion, and resize.
//   - Writer returns an io.Writer for stray output that should be
//     interleaved above the live region. Currently unused; PR 2 will
//     route runner warnings through it. Implementations that have no
//     interleaving guarantees may return os.Stderr.
//   - Trimmed reports whether the most recent Render had to drop rows
//     to fit the viewport. runPool uses this to decide whether to emit
//     a trailing plain dump so the user sees every row's final state.
//   - Close releases resources (signal subscriptions, render
//     goroutines). Safe to call multiple times.
type poolRenderer interface {
	Render(rows []poolStatusRow)
	Writer() io.Writer
	Trimmed() bool
	Close()
}

// newPoolRenderer constructs the renderer used by runPool when not in
// quiet mode. Today only legacyPoolRenderer is available; the mpb
// backend lands in a follow-up commit and this factory will dispatch
// based on ATEAM_RENDERER (default = legacy).
func newPoolRenderer(w io.Writer) poolRenderer {
	if w == nil {
		w = os.Stdout
	}
	return newLegacyPoolRenderer(w)
}

// legacyPoolRenderer wraps the existing cursor-up + clear-to-end
// rendering. Behavior is identical to what runPool used to do inline;
// extracting it lets us swap in alternative backends without touching
// runPool.
type legacyPoolRenderer struct {
	w io.Writer

	mu           sync.Mutex
	rows         []poolStatusRow // last snapshot, used by the resize goroutine
	renderedRows int             // visual rows occupied by the most recent draw
	trimmed      bool            // sticky: true if any draw had to trim
	started      bool            // first Render has happened (cursor anchor exists)

	resizeStop func()
	resizeDone sync.WaitGroup
}

func newLegacyPoolRenderer(w io.Writer) *legacyPoolRenderer {
	return &legacyPoolRenderer{w: w}
}

func (r *legacyPoolRenderer) Render(rows []poolStatusRow) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Snapshot for the resize goroutine. clonePoolStatusRows decouples
	// us from the caller's slice so a later mutation can't tear a redraw.
	r.rows = clonePoolStatusRows(rows)
	if !r.started {
		r.renderedRows, r.trimmed = printPoolStatusesTo(r.w, r.rows)
		r.started = true
		r.subscribeResizeLocked()
		return
	}
	rendered, trimmed := reprintPoolStatusesTo(r.w, r.rows, r.renderedRows)
	r.renderedRows = rendered
	if trimmed {
		r.trimmed = true
	}
}

func (r *legacyPoolRenderer) Writer() io.Writer { return r.w }
func (r *legacyPoolRenderer) Trimmed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.trimmed
}

func (r *legacyPoolRenderer) Close() {
	r.mu.Lock()
	stop := r.resizeStop
	r.resizeStop = nil
	r.mu.Unlock()
	if stop != nil {
		stop()
	}
	r.resizeDone.Wait()
}

func (r *legacyPoolRenderer) subscribeResizeLocked() {
	ch, stop := subscribeWindowResize()
	if ch == nil {
		return
	}
	r.resizeStop = stop
	r.resizeDone.Add(1)
	go func() {
		defer r.resizeDone.Done()
		for range ch {
			r.mu.Lock()
			rendered, trimmed := reprintPoolStatusesTo(r.w, r.rows, r.renderedRows)
			r.renderedRows = rendered
			if trimmed {
				r.trimmed = true
			}
			r.mu.Unlock()
		}
	}()
}
