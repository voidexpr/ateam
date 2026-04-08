package cmd

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ateam/internal/display"
	"github.com/ateam/internal/runner"
)

const (
	poolStateQueued  = "queued"
	poolStateRunning = "running"
	poolStateDone    = "done"
	poolStateError   = "ERROR"
	poolStatusHeader = "  ID      LABEL                     STATUS   CALLS  DETAILS"
)

type poolStatusRow struct {
	ExecID int64
	Label  string
	State  string
	Calls  int
	Detail string
	Path   string
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

// streamFilePrefix returns the log file prefix (without _stream.jsonl suffix)
// relative to cwd, with a trailing "*" glob hint.
func streamFilePrefix(streamPath, cwd string) string {
	prefix := strings.TrimSuffix(streamPath, "_stream.jsonl")
	rel := relPath(cwd, prefix)
	return rel + "*"
}

func poolStatusLinesForWidth(rows []poolStatusRow, width int) []string {
	lines := make([]string, 0, len(rows)+1)
	lines = append(lines, fitPoolStatusLine(poolStatusHeader, width))
	for _, row := range rows {
		lines = append(lines, poolStatusRowLines(row, width)...)
	}
	return lines
}

func poolStatusRowLines(row poolStatusRow, width int) []string {
	execID := "-"
	if row.ExecID > 0 {
		execID = strconv.FormatInt(row.ExecID, 10)
	}
	calls := "-"
	if row.State != poolStateQueued || row.Calls > 0 {
		calls = strconv.Itoa(row.Calls)
	}
	line := strings.TrimRight(fmt.Sprintf("  %-7s %-25s %-8s %-6s %s", execID, row.Label, row.State, calls, row.Detail), " ")
	if row.State != poolStateDone || row.Path == "" {
		return []string{fitPoolStatusLine(line, width)}
	}
	return []string{
		fitPoolStatusLine(line, width),
		row.Path,
	}
}

func fitPoolStatusLine(line string, width int) string {
	line = strings.TrimRight(line, " ")
	if width <= 1 {
		return line
	}
	limit := width - 1
	n := utf8.RuneCountInString(line)
	if n <= limit {
		return line
	}
	if limit == 1 {
		return "…"
	}
	runes := []rune(line)
	return string(runes[:limit-1]) + "…"
}

func formatRunningToolDetail(elapsed, toolName string, toolCount int) string {
	label := "tool calls"
	if toolCount == 1 {
		label = "tool call"
	}
	return strings.TrimSpace(fmt.Sprintf("%s  %s (%d %s)", elapsed, toolName, toolCount, label))
}

func poolStatusTerminal(state string) bool {
	return state == poolStateDone || state == poolStateError
}

func nextPoolStatusRow(row poolStatusRow, p runner.RunProgress) poolStatusRow {
	if poolStatusTerminal(row.State) {
		return row
	}
	elapsed := runner.FormatDuration(p.Elapsed)
	next := row
	next.ExecID = p.ExecID
	next.Calls = p.ToolCount
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
	return next
}

func errorPoolStatusRow(row poolStatusRow, summary runner.RunSummary, cwd string) poolStatusRow {
	return finalizedPoolStatusRow(row, summary, poolStateError, strings.TrimSpace(fmt.Sprintf("%s  %s  %s  %s",
		summary.EndedAt.Format("15:04:05"),
		runner.FormatDuration(summary.Duration),
		poolStatusTokens(summary),
		streamFilePrefix(summary.StreamFilePath, cwd),
	)), "")
}

func donePoolStatusRow(row poolStatusRow, summary runner.RunSummary, path string) poolStatusRow {
	return finalizedPoolStatusRow(row, summary, poolStateDone, strings.TrimSpace(fmt.Sprintf("%s  %s  %s  %s",
		summary.EndedAt.Format("15:04:05"),
		runner.FormatDuration(summary.Duration),
		poolStatusCost(summary),
		poolStatusTokens(summary),
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
	return display.FmtTokens(int64(summary.InputTokens + summary.OutputTokens + summary.CacheReadTokens + summary.CacheWriteTokens))
}

func writePoolStatusLines(w io.Writer, lines []string, clear bool) {
	for _, line := range lines {
		if clear {
			fmt.Fprintf(w, "\r\033[2K%s\n", line)
			continue
		}
		fmt.Fprintln(w, line)
	}
}

func savePoolStatusAnchor(w io.Writer) {
	fmt.Fprint(w, "\r\033[2K\0337")
}

func redrawPoolStatusLines(w io.Writer, lines []string, previousRows int, width int) int {
	currentRows := totalVisualRows(lines, width)
	fmt.Fprint(w, "\0338")
	if previousRows > 0 {
		fmt.Fprintf(w, "\033[%dA", previousRows)
	}
	fmt.Fprint(w, "\033[J")
	writePoolStatusLines(w, lines, true)
	savePoolStatusAnchor(w)
	return currentRows
}

func visualRowsForLine(line string, width int) int {
	if width <= 0 {
		return 1
	}
	n := utf8.RuneCountInString(line)
	if n == 0 {
		return 1
	}
	rows := n / width
	if n%width != 0 {
		rows++
	}
	if rows < 1 {
		return 1
	}
	return rows
}

func totalVisualRows(lines []string, width int) int {
	total := 0
	for _, line := range lines {
		total += visualRowsForLine(line, width)
	}
	return total
}

func currentPoolStatusLines(rows []poolStatusRow) ([]string, int) {
	width := stdoutWidth()
	return poolStatusLinesForWidth(rows, width), width
}

func printPoolStatuses(rows []poolStatusRow) int {
	lines, width := currentPoolStatusLines(rows)
	writePoolStatusLines(os.Stdout, lines, false)
	savePoolStatusAnchor(os.Stdout)
	return totalVisualRows(lines, width)
}

func printPlainPoolStatuses(rows []poolStatusRow) {
	writePoolStatusLines(os.Stdout, poolStatusLinesForWidth(rows, 0), false)
}

func reprintPoolStatuses(rows []poolStatusRow, previousRows int) int {
	lines, width := currentPoolStatusLines(rows)
	return redrawPoolStatusLines(os.Stdout, lines, previousRows, width)
}
