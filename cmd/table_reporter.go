package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ateam/internal/display"
	"github.com/ateam/internal/flow"
	"github.com/ateam/internal/runner"
)

// tableReporter implements flow.Reporter using the existing
// poolStatusRow / poolRenderer (mpb) infrastructure. It owns the live
// status table for Parallel-shaped runs — report today, parallel after
// step 2g migrates.
//
// Concurrency: AgentEvent / BundleStart / BundleEnd fire concurrently
// from N Parallel worker goroutines. All state mutation happens under
// `mu`. Render itself is throttled to 500ms wall time via lastRedraw.
//
// Lifetime: the cmd builds the reporter with bundle labels up front
// (mpb needs the row count ahead of time), passes it to flow.Run, then
// calls Close after to tear down the live region and print the summary
// + failure tails.
type tableReporter struct {
	flow.BaseReporter

	out         io.Writer
	labels      []string
	agentName   string
	itemLabel   string
	onDone      func(summary runner.RunSummary, cwd string) string
	startedAt   time.Time
	cwd         string
	useLive     bool
	renderer    poolRenderer
	restoreStd  func()
	tornDown    bool
	mu          sync.Mutex
	rows        []poolStatusRow
	labelIndex  map[string]int
	results     []runner.RunSummary
	lastRedraw  time.Time
	succeeded   int
	failed      int
	skipped     int
	doneSummary bool
}

type tableReporterOpts struct {
	out       io.Writer
	labels    []string
	agentName string
	itemLabel string // "role(s)" or "task(s)" — used in the failure error
	onDone    func(runner.RunSummary, string) string
	quiet     bool
}

// newTableReporter prepares the live region. If quiet or stdout is not a
// TTY, the reporter falls back to the printProgress-shape stderr stream
// (matching today's runPool behavior).
func newTableReporter(opts tableReporterOpts) *tableReporter {
	out := opts.out
	if out == nil {
		out = os.Stdout
	}
	cwd, _ := os.Getwd()
	useLive := !opts.quiet && isTerminal()
	rows, idx := newPoolStatusRows(opts.labels)

	r := &tableReporter{
		out:        out,
		labels:     opts.labels,
		agentName:  opts.agentName,
		itemLabel:  opts.itemLabel,
		onDone:     opts.onDone,
		startedAt:  time.Now(),
		cwd:        cwd,
		useLive:    useLive,
		rows:       rows,
		labelIndex: idx,
	}
	if useLive {
		r.renderer = newPoolRenderer(os.Stdout)
		r.renderer.Render(rows)
		// Redirect process-wide os.Stdout / os.Stderr through the renderer
		// so any stray Go-side write lands above the live region instead
		// of corrupting the cursor accounting. Subprocess output is
		// captured separately and unaffected.
		r.restoreStd = redirectStdStreams(r.renderer.Writer())
	}
	return r
}

// StageStart is a no-op — the table was prepared at construction with the
// fixed row set. The reporter does not branch on Pipeline vs Parallel: in
// today's report shape there's exactly one Parallel under the Pipeline.

func (r *tableReporter) BundleStart(flow.BundleInfo) {}

func (r *tableReporter) AgentEvent(b flow.BundleInfo, p runner.RunProgress) {
	r.mu.Lock()
	defer r.mu.Unlock()
	idx, ok := r.labelIndex[b.Name]
	if !ok {
		return
	}
	r.rows[idx] = nextPoolStatusRow(r.rows[idx], p)
	r.renderIfDue()
}

func (r *tableReporter) BundleEnd(b flow.BundleInfo, res flow.Result) {
	r.mu.Lock()
	defer r.mu.Unlock()
	idx, ok := r.labelIndex[b.Name]
	if !ok {
		return
	}
	var summary runner.RunSummary
	if res.Summary != nil {
		summary = *res.Summary
	}
	switch res.Flow.State {
	case flow.StateSkip:
		summary.ErrorCause = res.Flow.Reason
		r.rows[idx] = skippedPoolStatusRow(r.rows[idx], summary)
		r.skipped++
	case flow.StateError:
		if summary.Err == nil && res.Flow.Err != nil {
			summary.Err = res.Flow.Err
			summary.IsError = true
		}
		r.rows[idx] = errorPoolStatusRow(r.rows[idx], summary, r.cwd)
		r.failed++
	default:
		displayPath := ""
		if r.onDone != nil {
			displayPath = r.onDone(summary, r.cwd)
		}
		r.rows[idx] = donePoolStatusRow(r.rows[idx], summary, displayPath)
		r.succeeded++
	}
	r.results = append(r.results, summary)
	r.render()
}

// StageEnd marks any queued-but-never-started rows as skipped. PreDispatch
// failures emit a StateSkip Result without firing BundleStart/End for the
// skipped bundle, so these rows would otherwise stay in "queued" forever.
// The framework's StageOutcome provides the aggregate skipped count for
// the cmd-layer summary.
func (r *tableReporter) StageEnd(_ flow.StageInfo, _ flow.StageOutcome) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.rows {
		if r.rows[i].State == poolStateQueued {
			r.rows[i].State = poolStateSkipped
			r.rows[i].Detail = "not dispatched"
		}
	}
	r.render()
}

func (r *tableReporter) render() {
	if !r.useLive || r.renderer == nil {
		return
	}
	r.renderer.Render(r.rows)
	r.lastRedraw = time.Now()
}

func (r *tableReporter) renderIfDue() {
	if !r.useLive || r.renderer == nil {
		return
	}
	if time.Since(r.lastRedraw) >= 500*time.Millisecond {
		r.renderer.Render(r.rows)
		r.lastRedraw = time.Now()
	}
}

// Close tears down the live region (restoring os.Stdout/Stderr) and
// prints the "N succeeded, M failed, K skipped" summary plus per-task
// failure tails. Returns a non-nil error if any task failed or was
// skipped — same shape today's runPool returns. Safe to call multiple
// times.
func (r *tableReporter) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.tornDown {
		// One final paint so the table reflects the latest state on the
		// real terminal, then tear down the live region.
		if r.useLive && r.renderer != nil {
			r.renderer.Render(r.rows)
		}
		if r.restoreStd != nil {
			r.restoreStd()
		}
		if r.renderer != nil {
			r.renderer.Close()
		}
		r.tornDown = true
	}

	if r.doneSummary {
		return r.errorFromCounts()
	}
	r.doneSummary = true

	if r.skipped > 0 {
		fmt.Fprintf(r.out, "\n%d succeeded, %d failed, %d skipped (%s)\n",
			r.succeeded, r.failed, r.skipped, display.FormatDuration(time.Since(r.startedAt)))
	} else {
		fmt.Fprintf(r.out, "\n%d succeeded, %d failed (%s)\n",
			r.succeeded, r.failed, display.FormatDuration(time.Since(r.startedAt)))
	}

	if r.failed > 0 {
		for _, summary := range r.results {
			if summary.Err == nil && !summary.IsError {
				continue
			}
			tail := runner.StreamTailError(summary.StreamFilePath, r.agentName, 5)
			if tail != "" {
				fmt.Fprintf(r.out, "\n  %s:\n", summary.RoleID)
				for _, line := range strings.Split(tail, "\n") {
					fmt.Fprintf(r.out, "        %s\n", line)
				}
			} else if summary.Err != nil {
				fmt.Fprintf(r.out, "\n  %s: %v\n", summary.RoleID, summary.Err)
			}
		}
	}

	return r.errorFromCounts()
}

func (r *tableReporter) errorFromCounts() error {
	switch {
	case r.failed > 0 && r.skipped > 0:
		return fmt.Errorf("%d %s failed, %d skipped", r.failed, r.itemLabel, r.skipped)
	case r.failed > 0:
		return fmt.Errorf("%d %s failed", r.failed, r.itemLabel)
	case r.skipped > 0:
		return fmt.Errorf("%d %s skipped", r.skipped, r.itemLabel)
	}
	return nil
}

// Counts returns the aggregate state after Close. Useful for cmd-layer
// reductions (conditional --review chain, "Run 'ateam review'..." hint).
func (r *tableReporter) Counts() (succeeded, failed, skipped int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.succeeded, r.failed, r.skipped
}

// Results returns a copy of the per-bundle summaries collected during the
// run, in completion order. Used by --print loops that iterate per-role
// results.
func (r *tableReporter) Results() []runner.RunSummary {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]runner.RunSummary, len(r.results))
	copy(out, r.results)
	return out
}
