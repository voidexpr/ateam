package cmd

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ateam/internal/runner"
)

func TestPoolStatusHeaderIncludesEstTokens(t *testing.T) {
	lines := poolStatusLinesForWidth([]poolStatusRow{
		{Label: "a", State: "queued"},
	}, 120)
	if !strings.Contains(lines[0], "EstTOKENS") {
		t.Fatalf("expected header to include EstTOKENS, got %q", lines[0])
	}
}

func TestPoolStatusRowShowsEstTokens(t *testing.T) {
	lines := poolStatusLinesForWidth([]poolStatusRow{
		{ExecID: 7, Label: "live", State: "running", EstTokens: 12345, Calls: 2, Detail: "1m2s"},
	}, 120)
	if len(lines) < 2 {
		t.Fatalf("expected header plus row, got %d lines", len(lines))
	}
	// 12345 tokens → FmtTokens renders as "12.3K" (see internal/display).
	if !strings.Contains(lines[1], "12.3K") {
		t.Errorf("expected row to show formatted EstTOKENS; got %q", lines[1])
	}
}

func TestNextPoolStatusRowTracksEstTokensMonotonically(t *testing.T) {
	row := poolStatusRow{Label: "l", State: "running", EstTokens: 500}
	// Subsequent progress reports larger cumulative totals — row takes them.
	next := nextPoolStatusRow(row, runner.RunProgress{
		Phase:                  runner.PhaseTool,
		ToolName:               "Bash",
		CumulativeInputTokens:  400,
		CumulativeOutputTokens: 300,
	})
	if next.EstTokens != 700 {
		t.Errorf("EstTokens = %d, want 700 (400+300)", next.EstTokens)
	}
	// A later event with smaller totals must not regress the column.
	back := nextPoolStatusRow(next, runner.RunProgress{
		Phase:                  runner.PhaseTool,
		ToolName:               "Bash",
		CumulativeInputTokens:  0,
		CumulativeOutputTokens: 0,
	})
	if back.EstTokens != 700 {
		t.Errorf("EstTokens regressed to %d, want 700", back.EstTokens)
	}
}

func TestPoolStatusLinesIncludeIDColumn(t *testing.T) {
	lines := poolStatusLinesForWidth([]poolStatusRow{
		{Label: "security", State: "queued"},
		{ExecID: 42, Label: "testing_basic", State: "running", Calls: 3, Detail: "12s  bash"},
	}, 120)

	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "ID") {
		t.Fatalf("expected header to include ID, got %q", lines[0])
	}
	if !strings.Contains(lines[0], "CALLS") {
		t.Fatalf("expected header to include CALLS, got %q", lines[0])
	}
	if !strings.Contains(lines[1], "-") {
		t.Fatalf("expected queued row to show empty exec id placeholder, got %q", lines[1])
	}
	if !strings.Contains(lines[2], "42") {
		t.Fatalf("expected running row to show exec id, got %q", lines[2])
	}
	if !strings.Contains(lines[2], " 3 ") {
		t.Fatalf("expected running row to show tool call count, got %q", lines[2])
	}
}

func TestFitPoolStatusLineAvoidsTerminalWrap(t *testing.T) {
	line := fitPoolStatusLine("  1234567 security running a very long detail string", 20)
	if got := utf8.RuneCountInString(line); got > 19 {
		t.Fatalf("expected at most 19 runes, got %d in %q", got, line)
	}
	if !strings.HasSuffix(line, "…") {
		t.Fatalf("expected truncated line to end with ellipsis, got %q", line)
	}
}

func TestDoneStatusPathIsNeverTruncated(t *testing.T) {
	path := "very/long/path/to/report.md"
	lines := poolStatusLinesForWidth([]poolStatusRow{
		{ExecID: 42, Label: "testing_basic", State: "done", Calls: 3, Detail: "12:34:56  12s  $0.25  1.2K", Path: path},
	}, 12)

	if len(lines) != 3 {
		t.Fatalf("expected header plus 2 done-row lines, got %d", len(lines))
	}
	if !strings.Contains(lines[2], path) {
		t.Fatalf("expected full path to remain visible, got %q", lines[2])
	}
}

func TestFormatRunningToolDetail(t *testing.T) {
	got := formatRunningToolDetail("12s", "Bash", 12)
	want := "12s  Bash (12 tool calls)"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestNextPoolStatusRowDoesNotOverwriteDoneRow(t *testing.T) {
	row := poolStatusRow{
		ExecID: 42,
		Label:  "testing_basic",
		State:  "done",
		Calls:  3,
		Detail: "12:34:56  12s  $0.25  1.2K",
		Path:   "reports/testing_basic/report.md",
	}

	next := nextPoolStatusRow(row, runner.RunProgress{
		ExecID:    42,
		Phase:     runner.PhaseTool,
		ToolName:  "Bash",
		ToolCount: 99,
	})

	if next.State != poolStateDone {
		t.Fatalf("expected done state to remain final, got %q", next.State)
	}
	if next.Path != "reports/testing_basic/report.md" {
		t.Fatalf("expected path to remain intact, got %q", next.Path)
	}
	if next.Detail != "12:34:56  12s  $0.25  1.2K" {
		t.Fatalf("expected detail to remain intact, got %q", next.Detail)
	}
}

func TestWritePoolStatusBlockRedrawClearsFromColumnZero(t *testing.T) {
	var buf bytes.Buffer
	redrawPoolStatusLines(&buf, []string{"header", "row"}, 4, 10)
	got := buf.String()

	if !strings.HasPrefix(got, "\r\033[4A\033[J") {
		t.Fatalf("expected redraw to walk back to the table's first row from current cursor, got %q", got)
	}
	if !strings.Contains(got, "\r\033[2Kheader\n\r\033[2Krow\n") {
		t.Fatalf("expected redraw lines to clear before rewriting, got %q", got)
	}
	if !strings.HasSuffix(got, "\r\033[2K") {
		t.Fatalf("expected redraw to leave cursor parked on a cleared anchor line below the table, got %q", got)
	}
	if strings.Contains(got, "\0337") || strings.Contains(got, "\0338") {
		t.Fatalf("redraw should not depend on DECSC/DECRC (cursor save/restore), got %q", got)
	}
}

func TestWritePoolStatusBlockRedrawHandlesZeroPrevious(t *testing.T) {
	var buf bytes.Buffer
	redrawPoolStatusLines(&buf, []string{"header"}, 0, 10)
	got := buf.String()

	if !strings.HasPrefix(got, "\r\033[J") {
		t.Fatalf("expected zero-previous redraw to skip the cursor-up, got %q", got)
	}
}

func TestTotalVisualRowsCountsWrappedLines(t *testing.T) {
	got := totalVisualRows([]string{"12345", "123456"}, 5)
	if got != 3 {
		t.Fatalf("expected 3 visual rows, got %d", got)
	}
}
