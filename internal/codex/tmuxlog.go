package codex

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// tmuxTracer writes a JSONL trace of every codex-tmux interaction (sends,
// hash-deduped pane captures, waitReady/waitIdle decisions, errors). The
// file is meant for `tail -f` during a stuck run and for post-mortem after
// one. nil receiver methods are no-ops so the rest of RunTmux can call
// tracer.event(...) and tracer.capture(...) without nil checks.
type tmuxTracer struct {
	w        io.Writer
	started  time.Time
	lastHash string
}

// newTmuxTracer opens the trace file at path and returns a tracer plus a
// Closer for the file. Returns (nil, nil, nil) when path is empty — callers
// can treat that as "tracing disabled". Errors creating the directory or
// file are returned but the agent layer treats them as best-effort.
func newTmuxTracer(path string, started time.Time) (*tmuxTracer, io.Closer, error) {
	if path == "" {
		return nil, nil, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, nil, err
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, nil, err
	}
	return &tmuxTracer{w: f, started: started}, f, nil
}

// event writes one structured trace line. kind is the discriminator
// (start, send-literal, send-keys, capture, waitReady, waitIdle, error,
// end, …). Always includes `ts` (RFC3339Nano UTC) and `elapsed_ms` so the
// log can be replayed against wall-clock time. fields may be nil.
func (t *tmuxTracer) event(kind string, fields map[string]any) {
	if t == nil || t.w == nil {
		return
	}
	if fields == nil {
		fields = map[string]any{}
	}
	fields["ts"] = time.Now().UTC().Format(time.RFC3339Nano)
	fields["elapsed_ms"] = time.Since(t.started).Milliseconds()
	fields["kind"] = kind
	b, err := json.Marshal(fields)
	if err != nil {
		// Fall back to a minimal record so the trace stays self-describing.
		b = []byte(`{"kind":"trace_marshal_error","err":` + jsonString(err.Error()) + `}`)
	}
	_, _ = t.w.Write(append(b, '\n'))
}

// capture logs a pane snapshot only when its hash changes since the last
// snapshot. The bottom-20 non-empty lines are recorded as `tail` so a
// stuck run reveals what state the pane was actually in. Full pane dumps
// would balloon the log on long runs.
func (t *tmuxTracer) capture(rendered string, ready, busy bool) {
	if t == nil || t.w == nil {
		return
	}
	h := stableHash(rendered)
	if h == t.lastHash {
		return
	}
	t.lastHash = h
	short := h
	if len(short) > 12 {
		short = short[:12]
	}
	t.event("capture", map[string]any{
		"hash":  short,
		"bytes": len(rendered),
		"ready": ready,
		"busy":  busy,
		"tail":  strings.Join(nonEmptyTail(rendered, 20), "\n"),
	})
}

// jsonString returns a JSON-encoded string literal. Used for the trace
// fallback path where json.Marshal already failed.
func jsonString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}
