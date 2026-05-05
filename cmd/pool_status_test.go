package cmd

import (
	"strings"
	"testing"

	"github.com/ateam/internal/runner"
)

func TestPoolStatusHeaderIncludesEstTokens(t *testing.T) {
	if !strings.Contains(poolStatusHeader, "EstTOKENS") {
		t.Fatalf("expected header to include EstTOKENS, got %q", poolStatusHeader)
	}
}

func TestPoolStatusHeaderIncludesIDColumn(t *testing.T) {
	if !strings.Contains(poolStatusHeader, "ID") {
		t.Fatalf("expected header to include ID, got %q", poolStatusHeader)
	}
	if !strings.Contains(poolStatusHeader, "CALLS") {
		t.Fatalf("expected header to include CALLS, got %q", poolStatusHeader)
	}
}

func TestPoolStatusRowFormatsExecID(t *testing.T) {
	queued := formatPoolRowSingleLine(poolStatusRow{Label: "security", State: poolStateQueued})
	if !strings.Contains(queued, "-") {
		t.Errorf("queued row should show empty exec id placeholder, got %q", queued)
	}
	running := formatPoolRowSingleLine(poolStatusRow{
		ExecID: 42, Label: "testing_basic", State: poolStateRunning, Calls: 3, Detail: "12s  bash",
	})
	if !strings.Contains(running, "42") {
		t.Errorf("running row should show exec id, got %q", running)
	}
	if !strings.Contains(running, " 3 ") {
		t.Errorf("running row should show tool call count, got %q", running)
	}
}

func TestPoolStatusRowShowsEstTokens(t *testing.T) {
	line := formatPoolRowSingleLine(poolStatusRow{
		ExecID: 7, Label: "live", State: poolStateRunning, EstTokens: 12345, Calls: 2, Detail: "1m2s",
	})
	// 12345 tokens → FmtTokens renders as "12.3K" (see internal/display).
	if !strings.Contains(line, "12.3K") {
		t.Errorf("expected row to show formatted EstTOKENS; got %q", line)
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
