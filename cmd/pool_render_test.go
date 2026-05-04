package cmd

import (
	"bytes"
	"strings"
	"testing"
)

// TestLegacyPoolRendererSmoke exercises the renderer's Render → Render
// → Close lifecycle through a buffer (no TTY). The output isn't visually
// inspected — it's enough that the contract holds: header is written,
// updates produce more bytes, Close is idempotent.
func TestLegacyPoolRendererSmoke(t *testing.T) {
	var buf bytes.Buffer
	r := newLegacyPoolRenderer(&buf)

	rows := []poolStatusRow{
		{Label: "alpha", State: poolStateQueued},
		{Label: "beta", State: poolStateQueued},
	}

	r.Render(rows)
	afterFirst := buf.Len()
	if afterFirst == 0 {
		t.Fatalf("first Render wrote nothing")
	}
	if !strings.Contains(buf.String(), "alpha") || !strings.Contains(buf.String(), "beta") {
		t.Errorf("first Render missing labels:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "LABEL") {
		t.Errorf("first Render missing header:\n%s", buf.String())
	}

	rows[0].State = poolStateRunning
	rows[0].Detail = "1m"
	r.Render(rows)
	if buf.Len() == afterFirst {
		t.Fatalf("second Render produced no additional output")
	}

	r.Close()
	r.Close() // must be idempotent
}

// TestLegacyPoolRendererTrimmedSticky verifies Trimmed() sticks once
// any draw has been forced to trim. Without a real terminal this is
// just a guard against regressions in the sticky-bit logic.
func TestLegacyPoolRendererTrimmedSticky(t *testing.T) {
	var buf bytes.Buffer
	r := newLegacyPoolRenderer(&buf)
	r.Render([]poolStatusRow{{Label: "a", State: poolStateQueued}})
	// stdoutSize() returns 0,0 in tests (not a TTY), so trimming never
	// fires; Trimmed() should report false.
	if r.Trimmed() {
		t.Errorf("expected Trimmed=false on non-TTY")
	}
	r.Close()
}

// TestNewPoolRendererDefault confirms the factory hands back a legacy
// renderer when ATEAM_RENDERER is unset.
func TestNewPoolRendererDefault(t *testing.T) {
	t.Setenv("ATEAM_RENDERER", "")
	var buf bytes.Buffer
	r := newPoolRenderer(&buf)
	if _, ok := r.(*legacyPoolRenderer); !ok {
		t.Errorf("expected *legacyPoolRenderer, got %T", r)
	}
	r.Close()
}

// TestNewPoolRendererMpb confirms ATEAM_RENDERER=mpb selects the mpb
// backend.
func TestNewPoolRendererMpb(t *testing.T) {
	t.Setenv("ATEAM_RENDERER", "mpb")
	var buf bytes.Buffer
	r := newPoolRenderer(&buf)
	if _, ok := r.(*mpbPoolRenderer); !ok {
		t.Errorf("expected *mpbPoolRenderer, got %T", r)
	}
	r.Close()
}

// TestMpbPoolRendererSmoke exercises the mpb renderer's lifecycle via a
// bytes.Buffer. mpb auto-disables ANSI rendering on non-TTY writers, so
// the output check is loose — we just verify the API contract holds and
// Close completes without deadlocking.
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

	if r.Trimmed() {
		t.Errorf("mpb renderer should report Trimmed=false")
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
		Calls:  3,
		Detail: "2m",
		Path:   ".ateam/roles/alpha/report.md",
	}
	got := formatPoolRowSingleLine(row)
	if !bytes.Contains([]byte(got), []byte("→ .ateam/roles/alpha/report.md")) {
		t.Errorf("expected path inlined with arrow, got %q", got)
	}
	if bytes.Contains([]byte(got), []byte("\n")) {
		t.Errorf("single-line formatter must not emit newlines, got %q", got)
	}
}
