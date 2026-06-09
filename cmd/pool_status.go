package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ateam/internal/display"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
)

const (
	poolStateQueued   = "queued"
	poolStateRunning  = "running"
	poolStateDone     = "done"
	poolStateError    = "ERROR"
	poolStateSkipped  = "SKIPPED"
	poolStateCanceled = "canceled" // process died without writing an end row (ateam top)

	// poolStatusRowFmt is the shared template used for both the header
	// and each data row, so column widths can't drift between them.
	poolStatusRowFmt = "  %-7s %-25s %-8s %-9s %-6s %s"
)

var poolStatusHeader = fmt.Sprintf(poolStatusRowFmt, "ID", "LABEL", "STATUS", "EstTOKENS", "TURNS", "DETAILS")

type poolStatusRow struct {
	ExecID    int64
	Label     string
	State     string
	EstTokens int // running total of input+output tokens; estimated from partial stream while the run is live
	Turns     int
	Detail    string
	Path      string
}

func newPoolStatusRows(labels []string) ([]poolStatusRow, map[string]int) {
	rows := make([]poolStatusRow, len(labels))
	index := make(map[string]int, len(labels))
	for i, label := range labels {
		index[label] = i
		rows[i] = poolStatusRow{
			Label: label,
			State: poolStateQueued,
		}
	}
	return rows, index
}

func clonePoolStatusRows(rows []poolStatusRow) []poolStatusRow {
	return append([]poolStatusRow(nil), rows...)
}

// progressColumnsHelp returns a paragraph describing the live progress
// table for inclusion in command --help text. unit is the noun for each
// row ("task", "role", etc).
func progressColumnsHelp(unit string) string {
	return fmt.Sprintf(`Progress table columns:
  ID, LABEL, STATUS, EstTOKENS, TURNS, DETAILS

  EstTOKENS is the running input+output token count for each %[1]s. While a
  %[1]s is live it is an *estimate* built from the per-turn usage reported in
  the stream (the final total only arrives on the agent's terminal result
  event); once the %[1]s finishes it reflects the authoritative total from
  that event. The column exists so a crash or timeout before the terminal
  event still gives visibility into how much the %[1]s consumed.`, unit)
}

// agentFilePrefix returns a friendly identifier for the log location of a
// run, relative to cwd. New layout: the per-exec_id dir. Legacy layout: the
// timestamp+action prefix (with a "*" glob hint, since multiple files share it).
func agentFilePrefix(streamPath, cwd string) string {
	if root.IsLegacyStreamFile(streamPath) {
		return relPath(cwd, strings.TrimSuffix(streamPath, "_stream.jsonl")) + "*"
	}
	return relPath(cwd, filepath.Dir(streamPath))
}

func formatRunningToolDetail(elapsed, toolName string, toolCount int) string {
	label := "tool calls"
	if toolCount == 1 {
		label = "tool call"
	}
	return strings.TrimSpace(fmt.Sprintf("%s  %s (%d %s)", elapsed, toolName, toolCount, label))
}

func poolStatusTerminal(state string) bool {
	return state == poolStateDone || state == poolStateError || state == poolStateCanceled
}

func nextPoolStatusRow(row poolStatusRow, p runner.RunProgress) poolStatusRow {
	if poolStatusTerminal(row.State) {
		return row
	}
	elapsed := display.FormatDuration(p.Elapsed)
	next := row
	next.ExecID = p.ExecID
	// Keep both counters monotonically increasing so a terminal event that
	// briefly reports 0 doesn't blank the column.
	if p.TurnCount > next.Turns {
		next.Turns = p.TurnCount
	}
	if t := p.CumulativeInputTokens + p.CumulativeOutputTokens; t > next.EstTokens {
		next.EstTokens = t
	}
	switch p.Phase {
	case runner.PhaseInit:
		next.State = poolStateRunning
		next.Detail = elapsed
	case runner.PhaseTool:
		next.State = poolStateRunning
		detail := formatRunningToolDetail(elapsed, p.ToolName, p.ToolCount)
		if p.ContextTokens > 0 {
			detail += " ctx: " + display.FmtTokens(int64(p.ContextTokens))
			if p.ContextWindow > 0 {
				detail += fmt.Sprintf("/%d%%", p.ContextTokens*100/p.ContextWindow)
			}
		}
		next.Detail = detail
	case runner.PhaseDone:
		next.State = poolStateDone
		next.Detail = elapsed
	case runner.PhaseError:
		next.State = poolStateError
		next.Detail = elapsed
	case runner.PhaseStall:
		next.State = poolStateRunning
		if p.Content != "" {
			next.Detail = elapsed + " stall: " + p.Content
		} else {
			next.Detail = elapsed + " stall"
		}
	default:
		next.State = poolStateRunning
		next.Detail = elapsed
	}
	return next
}

func finalizedPoolStatusRow(row poolStatusRow, summary runner.RunSummary, state, detail, path string) poolStatusRow {
	next := row
	next.ExecID = summary.ExecID
	next.State = state
	next.Detail = detail
	next.Path = path
	if t := summary.InputTokens + summary.OutputTokens; t > next.EstTokens {
		next.EstTokens = t
	}
	if summary.Turns > next.Turns {
		next.Turns = summary.Turns
	}
	return next
}

func errorPoolStatusRow(row poolStatusRow, summary runner.RunSummary, cwd string) poolStatusRow {
	return finalizedPoolStatusRow(row, summary, poolStateError, strings.TrimSpace(fmt.Sprintf("%s  %s  %s  %s  %s",
		summary.EndedAt.Format("15:04:05"),
		display.FormatDuration(summary.Duration),
		poolStatusTokens(summary),
		poolStatusContext(summary),
		agentFilePrefix(summary.AgentFilePath, cwd),
	)), "")
}

func skippedPoolStatusRow(row poolStatusRow, summary runner.RunSummary) poolStatusRow {
	detail := summary.ErrorCause
	if detail == "" {
		detail = "skipped"
	}
	return finalizedPoolStatusRow(row, summary, poolStateSkipped, detail, "")
}

func donePoolStatusRow(row poolStatusRow, summary runner.RunSummary, path string) poolStatusRow {
	return finalizedPoolStatusRow(row, summary, poolStateDone, strings.TrimSpace(fmt.Sprintf("%s  %s  %s  %s  %s",
		summary.EndedAt.Format("15:04:05"),
		display.FormatDuration(summary.Duration),
		poolStatusCost(summary),
		poolStatusTokens(summary),
		poolStatusContext(summary),
	)), path)
}

func poolStatusCost(summary runner.RunSummary) string {
	cost := display.FmtCost(summary.Cost)
	if cost == "" {
		return "$0.00"
	}
	return cost
}

func poolStatusTokens(summary runner.RunSummary) string {
	t := display.FmtTokens(int64(summary.InputTokens + summary.OutputTokens + summary.CacheReadTokens + summary.CacheWriteTokens))
	if t == "" {
		return ""
	}
	return "tokens: " + t
}

func poolStatusContext(summary runner.RunSummary) string {
	if summary.PeakContextTokens <= 0 {
		return ""
	}
	s := "ctx: " + display.FmtTokens(int64(summary.PeakContextTokens))
	if summary.ContextWindow > 0 {
		s += fmt.Sprintf("/%d%%", summary.PeakContextTokens*100/summary.ContextWindow)
	}
	return s
}
