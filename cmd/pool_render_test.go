package cmd

import (
	"bytes"
	"strings"
	"testing"
)

// TestNewPoolRendererReturnsMpb confirms the factory hands back the
// mpb renderer. There's only one implementation today; the test exists
// to flag if a future refactor accidentally swaps the wiring.
func TestNewPoolRendererReturnsMpb(t *testing.T) {
	var buf bytes.Buffer
	r := newPoolRenderer(&buf)
	if _, ok := r.(*mpbPoolRenderer); !ok {
		t.Errorf("expected *mpbPoolRenderer, got %T", r)
	}
	r.Close()
}

// TestMpbPoolRendererSmoke exercises the mpb renderer's lifecycle via a
// bytes.Buffer. mpb auto-disables ANSI rendering on non-TTY writers, so
// the bar output check is loose — we just verify the API contract
// holds, the column header is printed, and Close completes without
// deadlocking.
func TestMpbPoolRendererSmoke(t *testing.T) {
	var buf bytes.Buffer
	r := newMpbPoolRenderer(&buf)

	rows := []poolStatusRow{
		{Label: "alpha", State: poolStateQueued},
		{Label: "beta", State: poolStateQueued},
	}
	r.Render(rows)

	rows[0].State = poolStateRunning
	rows[0].Detail = "1m"
	r.Render(rows)

	rows[0].State = poolStateDone
	rows[0].Detail = "2m  $0.10"
	rows[0].Path = ".ateam/roles/alpha/report.md"
	rows[1].State = poolStateError
	rows[1].Detail = "0s"
	r.Render(rows)

	r.Close()
	r.Close() // idempotent

	if !strings.Contains(buf.String(), "LABEL") || !strings.Contains(buf.String(), "STATUS") {
		t.Errorf("mpb renderer must print the column header above the live region:\n%s", buf.String())
	}
}

// TestFormatPoolRowSingleLineInlinesPath verifies that the mpb formatter
// embeds the report path in the detail column instead of putting it on
// a 2nd line — bars are single-line and we don't want the column widths
// to drift just because the row is "done".
func TestFormatPoolRowSingleLineInlinesPath(t *testing.T) {
	row := poolStatusRow{
		ExecID: 42,
		Label:  "alpha",
		State:  poolStateDone,
		Turns:  3,
		Detail: "2m",
		Path:   ".ateam/roles/alpha/report.md",
	}
	got := formatPoolRowSingleLine(row)
	if !strings.Contains(got, "→ .ateam/roles/alpha/report.md") {
		t.Errorf("expected path inlined with arrow, got %q", got)
	}
	if strings.Contains(got, "\n") {
		t.Errorf("single-line formatter must not emit newlines, got %q", got)
	}
}
