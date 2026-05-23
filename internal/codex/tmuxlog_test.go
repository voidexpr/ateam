package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestTmuxTracerWritesJSONLine: a non-nil tracer writes one well-formed
// JSON record per event() call, with `ts`, `elapsed_ms`, `kind`, and the
// custom fields preserved.
func TestTmuxTracerWritesJSONLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tmux.log")
	tracer, closer, err := newTmuxTracer(path, time.Now())
	if err != nil {
		t.Fatalf("newTmuxTracer: %v", err)
	}
	defer closer.Close()

	tracer.event("start", map[string]any{"session": "ateam-codex-42", "exec_id": int64(42)})
	tracer.event("send-keys", map[string]any{"keys": []string{"Enter"}})

	if err := closer.Close(); err != nil {
		t.Fatalf("closer.Close: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2:\n%s", len(lines), raw)
	}

	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("line 0 not JSON: %v\n%s", err, lines[0])
	}
	for _, k := range []string{"ts", "elapsed_ms", "kind", "session", "exec_id"} {
		if _, ok := first[k]; !ok {
			t.Errorf("missing field %q in: %s", k, lines[0])
		}
	}
	if first["kind"] != "start" {
		t.Errorf("kind = %v, want start", first["kind"])
	}
}

// TestTmuxTracerCaptureDeduplicates: a sequence of identical captures
// produces exactly one capture entry — the dedup is what keeps tmux.log
// from ballooning during a long idle wait (250ms polling × 5 min would
// otherwise emit 1200 entries).
func TestTmuxTracerCaptureDeduplicates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tmux.log")
	tracer, closer, err := newTmuxTracer(path, time.Now())
	if err != nil {
		t.Fatalf("newTmuxTracer: %v", err)
	}
	defer closer.Close()

	for i := 0; i < 100; i++ {
		tracer.capture("›\n  gpt-5.5 xhigh · ~/repo", true, false)
	}
	tracer.capture("›\n  gpt-5.5 xhigh · ~/repo · Context 5% used", true, false)
	for i := 0; i < 50; i++ {
		tracer.capture("›\n  gpt-5.5 xhigh · ~/repo · Context 5% used", true, false)
	}

	if err := closer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	count := strings.Count(string(raw), `"kind":"capture"`)
	if count != 2 {
		t.Errorf("got %d capture entries, want 2 (one per unique hash). Trace:\n%s", count, raw)
	}
}

// TestTmuxTracerNilSafe: a nil receiver is a no-op. RunTmux constructs the
// tracer optionally — codex/agent tests that don't pass a TmuxLogPath get
// nil, and every tracer.event/capture call site must tolerate that.
func TestTmuxTracerNilSafe(t *testing.T) {
	var tr *tmuxTracer // intentionally nil
	tr.event("anything", map[string]any{"x": 1})
	tr.capture("some pane", true, false)
	// no panic, no file written — success
}

// TestTmuxTracerEmptyPathSkipsWriter: empty path means tracing disabled.
// newTmuxTracer returns (nil, nil, nil) so callers can blanket-defer
// closer.Close() with no nil check on the closer either.
func TestTmuxTracerEmptyPathSkipsWriter(t *testing.T) {
	tr, closer, err := newTmuxTracer("", time.Now())
	if err != nil {
		t.Errorf("newTmuxTracer(\"\") err = %v, want nil", err)
	}
	if tr != nil {
		t.Errorf("tracer = %+v, want nil", tr)
	}
	if closer != nil {
		t.Errorf("closer non-nil for empty path")
	}
}
