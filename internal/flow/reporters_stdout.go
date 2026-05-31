package flow

import (
	"fmt"
	"io"
	"os"

	"github.com/ateam/internal/display"
	"github.com/ateam/internal/runner"
)

// StdoutReporter streams bundle lifecycle to stdout. Single-bundle cmds
// (exec, verify, review, auto_setup, code) use this as their default
// Reporter; it replaces the per-cmd "Starting…" / "Done (dur, cost)" prints
// the migrated cmds used to emit inline.
//
// Stage callbacks are intentionally quiet — a single bundle's start/end is
// already the section delimiter the user reads. Parallel runs use the
// TableReporter instead.
//
// Thread-safety: not synchronized. Intended for single-bundle execution
// where every callback fires from one goroutine. Using StdoutReporter
// from a Parallel produces interleaved (but not corrupted) output —
// os.Stdout writes are atomic. Use TableReporter for Parallel instead.
type StdoutReporter struct {
	BaseReporter
	Out io.Writer
}

func (r *StdoutReporter) writer() io.Writer {
	if r.Out != nil {
		return r.Out
	}
	return os.Stdout
}

// BundleStart prints "Starting <name>..." with role/action context when
// they're meaningful (different from the bundle name).
func (r *StdoutReporter) BundleStart(b BundleInfo) {
	switch {
	case b.Role != "" && b.Role != b.Name:
		fmt.Fprintf(r.writer(), "Starting %s (role %s)...\n", b.Name, b.Role)
	default:
		fmt.Fprintf(r.writer(), "Starting %s...\n", b.Name)
	}
}

// BundleEnd prints one of:
//   - "Done (dur, cost)"          on success
//   - "Skipped <name>: <reason>"  on Pre-skip
//   - "Failed <name>: <reason>"   on Pre/Render/Execute/Post error
//
// Replaces the old internal/stage/actions::PrintDone action — successful
// bundles automatically get the "Done" line via the Reporter.
func (r *StdoutReporter) BundleEnd(b BundleInfo, res Result) {
	w := r.writer()
	switch res.Flow.State {
	case StateSkip:
		fmt.Fprintf(w, "Skipped %s: %s\n", b.Name, res.Flow.Reason)
	case StateError:
		reason := res.Flow.Reason
		if reason == "" && res.Flow.Err != nil {
			reason = res.Flow.Err.Error()
		}
		fmt.Fprintf(w, "Failed %s: %s\n", b.Name, reason)
	default:
		if res.Summary != nil {
			cost := ""
			if c := display.FmtCost(res.Summary.Cost); c != "" {
				cost = ", " + c
			}
			fmt.Fprintf(w, "Done (%s%s)\n\n", display.FormatDuration(res.Summary.Duration), cost)
		} else {
			fmt.Fprintf(w, "Done %s\n\n", b.Name)
		}
	}
}

// AgentEvent is intentionally a no-op for StdoutReporter — the runner
// already streams subprocess output (and the stream.jsonl path) to the
// user. AgentEvent is the structured channel for renderers that want to
// build their own UI (TableReporter, JSONReporter).
func (r *StdoutReporter) AgentEvent(BundleInfo, runner.RunProgress) {}
