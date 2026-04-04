package cmd

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ateam/internal/runner"
)

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

	if !strings.HasPrefix(got, "\0338\033[4A\033[J") {
		t.Fatalf("expected redraw to restore saved cursor before moving up, got %q", got)
	}
	if !strings.Contains(got, "\r\033[2Kheader\n\r\033[2Krow\n") {
		t.Fatalf("expected redraw lines to clear before rewriting, got %q", got)
	}
	if !strings.HasSuffix(got, "\r\033[2K\0337") {
		t.Fatalf("expected redraw to clear and save the anchor line below the table, got %q", got)
	}
}

func TestTotalVisualRowsCountsWrappedLines(t *testing.T) {
	got := totalVisualRows([]string{"12345", "123456"}, 5)
	if got != 3 {
		t.Fatalf("expected 3 visual rows, got %d", got)
	}
}
