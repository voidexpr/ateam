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

// TestNewPoolRendererDefault confirms the factory still hands back a
// legacy renderer until the mpb backend lands.
func TestNewPoolRendererDefault(t *testing.T) {
	var buf bytes.Buffer
	r := newPoolRenderer(&buf)
	if _, ok := r.(*legacyPoolRenderer); !ok {
		t.Errorf("expected *legacyPoolRenderer, got %T", r)
	}
	r.Close()
}
