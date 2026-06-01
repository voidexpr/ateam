package flow

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ateam/internal/display"
	"github.com/ateam/internal/runner"
)

// StdoutReporter is the default Reporter for single-bundle cmds (exec,
// verify, review, auto_setup, code). It replaces the per-cmd "Done (dur,
// cost)" / "Skipped:" / "Failed:" lines those cmds used to emit inline.
//
// BundleStart is a no-op: each cmd prints its own informative starting
// line (e.g. "Supervisor verifying recent code changes (5m timeout)...")
// which carries richer context than a generic "Starting <name>..." would.
// StageStart/End are also quiet.
//
// AgentEvent is gated by Stream: when true (auto_setup, code default
// mode, exec without --no-stream), the reporter emits "[role] tool: X"
// style structured progress lines to ErrOut, replacing the per-cmd
// printProgress chan-drainer goroutine. When false (verify, review),
// AgentEvent is a no-op and the user relies on the runner's stderr
// passthrough for live feedback.
//
// Thread-safety: not synchronized. Intended for single-bundle execution
// where every callback fires from one goroutine. Using StdoutReporter
// from a Parallel produces interleaved (but not corrupted) output —
// os.Stdout / os.Stderr writes are atomic. Use TableReporter for Parallel.
type StdoutReporter struct {
	BaseReporter
	Out               io.Writer // stdout for BundleEnd lines (defaults to os.Stdout)
	ErrOut            io.Writer // stderr for streamed AgentEvent lines (defaults to os.Stderr)
	Stream            bool      // emit per-event progress lines on AgentEvent
	SuppressBundleEnd bool      // when true, BundleEnd is silent — cmds that print their own richer end-of-run output (e.g. exec's printExecSummary) set this to skip the generic "Done (...)" line
}

func (r *StdoutReporter) writer() io.Writer {
	if r.Out != nil {
		return r.Out
	}
	return os.Stdout
}

func (r *StdoutReporter) errWriter() io.Writer {
	if r.ErrOut != nil {
		return r.ErrOut
	}
	return os.Stderr
}

// BundleStart is a no-op; see the type comment for the rationale.

// BundleEnd prints one of:
//   - "Done (dur, cost)"          on success
//   - "Skipped <name>: <reason>"  on Pre-skip
//   - "Failed <name>: <reason>"   on Pre/Render/Execute/Post error
//
// Replaces the old internal/stage/actions::PrintDone action — successful
// bundles automatically get the "Done" line via the Reporter.
func (r *StdoutReporter) BundleEnd(b BundleInfo, res Result) {
	if r.SuppressBundleEnd {
		return
	}
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

// AgentEvent emits a structured progress line per RunProgress event when
// Stream is true. Delegates to PrintProgressLine so cmd-layer drainers
// (e.g. cmd/exec.go::printProgress for the non-flow paths that still
// exist in auto_roles / inspect) can share the exact format.
func (r *StdoutReporter) AgentEvent(_ BundleInfo, p runner.RunProgress) {
	if !r.Stream {
		return
	}
	PrintProgressLine(r.errWriter(), p)
}

// PrintProgressLine writes one structured "[role] ..." progress line to
// w for the given RunProgress event. Single source of truth for the
// stream-mode progress format; the cmd-layer printProgress drainer still
// in use by `ateam auto_roles` and `ateam inspect` calls into this
// helper so the two paths cannot drift.
func PrintProgressLine(w io.Writer, p runner.RunProgress) {
	ts := display.FormatDuration(p.Elapsed)
	switch p.Phase {
	case runner.PhaseInit:
		fmt.Fprintf(w, "[%s] %s\n", p.RoleID, FormatInitLine(p))
	case runner.PhaseThinking:
		if p.Content != "" {
			fmt.Fprintf(w, "[%s] %s (%s)\n", p.RoleID, runner.SingleLineText(p.Content), ts)
		} else {
			fmt.Fprintf(w, "[%s] thinking... (%s)\n", p.RoleID, ts)
		}
	case runner.PhaseTool:
		ctxInfo := FormatContextProgress(p.ContextTokens, p.ContextWindow)
		if p.ToolInput != "" {
			fmt.Fprintf(w, "[%s] tool: %s %s (%d total, %s%s)\n", p.RoleID, p.ToolName, runner.SingleLineText(p.ToolInput), p.ToolCount, ts, ctxInfo)
		} else {
			fmt.Fprintf(w, "[%s] tool: %s (%d total, %s%s)\n", p.RoleID, p.ToolName, p.ToolCount, ts, ctxInfo)
		}
	case runner.PhaseToolResult:
		if p.Content != "" {
			fmt.Fprintf(w, "[%s] result: %s (%s)\n", p.RoleID, runner.SingleLineText(p.Content), ts)
		}
	case runner.PhaseDone:
		fmt.Fprintf(w, "[%s] done (%s)\n", p.RoleID, ts)
	case runner.PhaseError:
		fmt.Fprintf(w, "[%s] error (%s)\n", p.RoleID, ts)
	case runner.PhaseStall:
		fmt.Fprintf(w, "[%s] stall: %s (%s)\n", p.RoleID, p.Content, ts)
	}
}

// FormatInitLine renders the body of an "[role] ..." line for a
// PhaseInit event. Exported so cmd-layer drainers (auto_roles, inspect)
// share the format.
func FormatInitLine(p runner.RunProgress) string {
	switch p.Subtype {
	case "compact_boundary":
		return "context compacted"
	case "", "init":
		parts := []string{}
		if p.Model != "" {
			parts = append(parts, "model="+p.Model)
		}
		if p.SessionID != "" {
			parts = append(parts, "session="+p.SessionID)
		}
		if len(parts) == 0 {
			return "initializing..."
		}
		return "init: " + strings.Join(parts, " ")
	default:
		return "init: " + p.Subtype
	}
}

// FormatContextProgress renders the ", ctx: X/Y%" tail for PhaseTool
// lines. Empty when contextTokens is unknown.
func FormatContextProgress(contextTokens, contextWindow int) string {
	if contextTokens <= 0 {
		return ""
	}
	ctxStr := display.FmtTokens(int64(contextTokens))
	if contextWindow > 0 {
		pct := contextTokens * 100 / contextWindow
		return fmt.Sprintf(", ctx: %s/%d%%", ctxStr, pct)
	}
	return fmt.Sprintf(", ctx: %s", ctxStr)
}
