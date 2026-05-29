package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/display"
	"github.com/ateam/internal/runner"
)

// poolDisplayOpts controls how runPool renders progress and formats output.
type poolDisplayOpts struct {
	quiet       bool                                   // suppress ANSI table; fall back to plain text progress
	out         io.Writer                              // output for summary/error tails (nil = os.Stdout)
	onDone      func(runner.RunSummary, string) string // result, cwd → display path for status row; nil → ""
	agentName   string
	itemLabel   string       // used in "N failed" error, e.g. "role(s)" or "agent(s)"
	preDispatch func() error // optional precheck before each task is dispatched (e.g. batch budget cap)
}

// runPool drives a runner.Pool to completion, rendering progress and printing
// the summary count + error tails. It returns all results and a non-nil error
// if any tasks failed.
func runPool(ctx context.Context, r *runner.Runner, tasks []runner.PoolExec, maxParallel int, opts poolDisplayOpts) ([]runner.RunSummary, error) {
	start := time.Now()
	out := opts.out
	if out == nil {
		out = os.Stdout
	}

	labels := make([]string, len(tasks))
	for i, t := range tasks {
		labels[i] = t.RoleID
	}
	cwd, _ := os.Getwd()

	// The live renderer requires a TTY: mpb auto-disables ANSI rendering
	// on non-*os.File writers and silently buffers everything, so a
	// non-TTY run (piped, redirected, CI) would see the column header
	// and then nothing until exit. Fall through to the plain
	// printProgress path in that case (same path --quiet uses).
	useLiveRenderer := !opts.quiet && isTerminal()

	var statusRows []poolStatusRow
	var labelIndex map[string]int
	var renderer poolRenderer
	var restoreStd func()
	if useLiveRenderer {
		statusRows, labelIndex = newPoolStatusRows(labels)
		renderer = newPoolRenderer(os.Stdout)
		renderer.Render(statusRows)
		// Redirect process-wide os.Stdout / os.Stderr through the
		// renderer's interleave channel for the duration of the run.
		// This catches every Go-side stray write — fmt.Fprintf,
		// log.Printf, panic output, future call sites we haven't
		// audited — and routes them above the live region instead of
		// letting them corrupt the cursor accounting. Subprocess
		// output is captured separately by the agent and not affected.
		restoreStd = redirectStdStreams(renderer.Writer())
	}
	// Defer-only cleanup as a panic safety net. Normal flow tears
	// down the live region explicitly below so the post-run summary
	// lands on the real terminal, not interleaved through mpb.
	liveRegionTornDown := false
	tearDownLiveRegion := func() {
		if liveRegionTornDown {
			return
		}
		liveRegionTornDown = true
		// Order matters: restore real stdout/stderr first so any
		// final bytes drain into the renderer, then close the
		// renderer to flush its bars.
		if restoreStd != nil {
			restoreStd()
		}
		if renderer != nil {
			renderer.Close()
		}
	}
	defer tearDownLiveRegion()

	completedCh := make(chan runner.RunSummary, len(tasks))
	progressCh := make(chan runner.RunProgress, 64)
	var statusMu sync.Mutex

	go func() {
		runner.RunPoolWithOpts(ctx, r, tasks, maxParallel, progressCh, completedCh, runner.PoolOpts{
			PreDispatch: opts.preDispatch,
		})
		close(progressCh)
	}()

	var progressDone sync.WaitGroup
	progressDone.Add(1)
	go func() {
		defer progressDone.Done()
		if useLiveRenderer {
			var lastRedraw time.Time
			for p := range progressCh {
				idx, ok := labelIndex[p.RoleID]
				if !ok {
					continue
				}
				statusMu.Lock()
				statusRows[idx] = nextPoolStatusRow(statusRows[idx], p)
				if time.Since(lastRedraw) >= 500*time.Millisecond {
					renderer.Render(statusRows)
					lastRedraw = time.Now()
				}
				statusMu.Unlock()
			}
		} else {
			printProgress(progressCh)
		}
	}()

	var succeeded, failed, skipped int
	var results []runner.RunSummary
	for result := range completedCh {
		isSkipped := result.ErrorSource == agent.ErrorSourceSkipped
		if useLiveRenderer {
			statusMu.Lock()
			idx := labelIndex[result.RoleID]
			switch {
			case isSkipped:
				statusRows[idx] = skippedPoolStatusRow(statusRows[idx], result)
				skipped++
			case result.Err != nil:
				statusRows[idx] = errorPoolStatusRow(statusRows[idx], result, cwd)
				failed++
			default:
				displayPath := ""
				if opts.onDone != nil {
					displayPath = opts.onDone(result, cwd)
				}
				statusRows[idx] = donePoolStatusRow(statusRows[idx], result, displayPath)
				succeeded++
			}
			renderer.Render(statusRows)
			statusMu.Unlock()
		} else {
			switch {
			case isSkipped:
				skipped++
			case result.Err != nil:
				failed++
			default:
				if opts.onDone != nil {
					opts.onDone(result, cwd)
				}
				succeeded++
			}
		}
		results = append(results, result)
	}
	progressDone.Wait()

	if useLiveRenderer {
		statusMu.Lock()
		if ctx.Err() == nil {
			renderer.Render(statusRows)
		}
		statusMu.Unlock()
	}

	// Tear down the live region BEFORE printing the summary and any
	// failure tails. With mpb still active those writes either fight
	// with the bar redraws (when written via opts.out / fmt.Fprintf
	// which bypasses the redirect) or get interleaved above the bars
	// alongside a now-redundant copy of the table.
	tearDownLiveRegion()

	if skipped > 0 {
		fmt.Fprintf(out, "\n%d succeeded, %d failed, %d skipped (%s)\n", succeeded, failed, skipped, display.FormatDuration(time.Since(start)))
	} else {
		fmt.Fprintf(out, "\n%d succeeded, %d failed (%s)\n", succeeded, failed, display.FormatDuration(time.Since(start)))
	}

	if failed > 0 {
		for _, result := range results {
			if result.Err == nil {
				continue
			}
			tail := runner.StreamTailError(result.StreamFilePath, opts.agentName, 5)
			if tail != "" {
				fmt.Fprintf(out, "\n  %s:\n", result.RoleID)
				for _, line := range strings.Split(tail, "\n") {
					fmt.Fprintf(out, "        %s\n", line)
				}
			} else {
				fmt.Fprintf(out, "\n  %s: %v\n", result.RoleID, result.Err)
			}
		}
	}

	switch {
	case failed > 0 && skipped > 0:
		return results, fmt.Errorf("%d %s failed, %d skipped", failed, opts.itemLabel, skipped)
	case failed > 0:
		return results, fmt.Errorf("%d %s failed", failed, opts.itemLabel)
	case skipped > 0:
		return results, fmt.Errorf("%d %s skipped", skipped, opts.itemLabel)
	}
	return results, nil
}
