package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ateam/internal/calldb"
)

func TestTailerStaticFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.jsonl")

	content := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"s1","model":"opus"}`,
		`{"type":"user"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"hello"}]}}`,
		`{"type":"result","total_cost_usd":0.01,"duration_ms":5000,"num_turns":1,"usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":0}}`,
	}, "\n") + "\n"
	_ = os.WriteFile(path, []byte(content), 0644)

	var buf bytes.Buffer
	tailer := NewTailer(&buf, nil, false, false)
	tailer.AddSource(1, "security", "exec", path, "")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = tailer.Run(ctx)

	out := buf.String()
	if !strings.Contains(out, "Turn 1") {
		t.Errorf("expected Turn 1 in output, got: %s", out)
	}
	if !strings.Contains(out, "Result") {
		t.Errorf("expected Result in output, got: %s", out)
	}
	if !strings.Contains(out, "[1:security/exec]") {
		t.Errorf("expected prefix in output, got: %s", out)
	}
}

func TestTailerWaitTimeout(t *testing.T) {
	var buf bytes.Buffer
	tailer := NewTailer(&buf, nil, false, false)
	tailer.WaitTimeout = 100 * time.Millisecond // fast timeout for test

	ctx := context.Background()
	_ = tailer.Run(ctx)

	if !strings.Contains(buf.String(), "No running processes found") {
		t.Errorf("expected timeout message, got: %s", buf.String())
	}
}

func TestTailerGrowingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.jsonl")

	// Start with partial content
	f, _ := os.Create(path)
	_, _ = f.WriteString(`{"type":"system","subtype":"init","session_id":"s1","model":"opus"}` + "\n")
	_, _ = f.WriteString(`{"type":"user"}` + "\n")
	f.Close()

	var buf bytes.Buffer
	tailer := NewTailer(&buf, nil, false, false)
	tailer.PollInterval = 50 * time.Millisecond
	tailer.AddSource(1, "test", "exec", path, "")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Append result after a delay
	go func() {
		time.Sleep(200 * time.Millisecond)
		f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
		_, _ = f.WriteString(`{"type":"result","total_cost_usd":0.01,"duration_ms":5000,"num_turns":1,"usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":0}}` + "\n")
		f.Close()
	}()

	_ = tailer.Run(ctx)

	out := buf.String()
	if !strings.Contains(out, "Result") {
		t.Errorf("expected Result in output after file grow, got: %s", out)
	}
}

func TestSplitLines(t *testing.T) {
	buf := []byte("line1\nline2\npartial")
	lines, remainder := splitLines(buf)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	if string(lines[0]) != "line1" {
		t.Errorf("expected line1, got %q", string(lines[0]))
	}
	if string(lines[1]) != "line2" {
		t.Errorf("expected line2, got %q", string(lines[1]))
	}
	if string(remainder) != "partial" {
		t.Errorf("expected remainder 'partial', got %q", string(remainder))
	}

	// All complete
	buf = []byte("a\nb\n")
	lines, remainder = splitLines(buf)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	if remainder != nil {
		t.Errorf("expected nil remainder, got %q", string(remainder))
	}
}

func TestTailerFinalMessageJSONL(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.sqlite")
	db, err := openTestCallDB(t, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	streamPath := filepath.Join(dir, "agent.jsonl")
	content := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"s1","model":"opus"}`,
		`{"type":"user"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"PASSED: all tests green"}]}}`,
		`{"type":"result","total_cost_usd":0.42,"duration_ms":12345,"num_turns":2,"usage":{"input_tokens":1000,"output_tokens":200,"cache_read_input_tokens":50,"cache_write_input_tokens":25}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(streamPath, []byte(content), 0644); err != nil {
		t.Fatalf("write stream: %v", err)
	}

	id, err := db.InsertCall(&calldb.Call{
		ProjectID: "proj",
		Agent:     "claude",
		Action:    "exec",
		Role:      "security",
		Model:     "opus",
		StartedAt: time.Now(),
		AgentFile: streamPath,
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := db.UpdateCall(id, &calldb.CallResult{
		EndedAt:          time.Now(),
		DurationMS:       12345,
		ExitCode:         0,
		IsError:          false,
		CostUSD:          0.42,
		InputTokens:      1000,
		OutputTokens:     200,
		CacheReadTokens:  50,
		CacheWriteTokens: 25,
		Turns:            2,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	var buf bytes.Buffer
	tailer := NewTailer(&buf, db, false, false)
	tailer.FinalMessageOnly = true
	tailer.AddSource(id, "security", "exec", streamPath, "opus")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := tailer.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	out := buf.String()
	// Must be exactly one JSONL line.
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 JSONL line, got %d: %q", len(lines), out)
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, lines[0])
	}
	if got, ok := rec["final_message"].(string); !ok || got != "PASSED: all tests green" {
		t.Errorf("final_message: got %v", rec["final_message"])
	}
	if got, _ := rec["role"].(string); got != "security" {
		t.Errorf("role: got %v", rec["role"])
	}
	if got, _ := rec["action"].(string); got != "exec" {
		t.Errorf("action: got %v", rec["action"])
	}
	if got, _ := rec["agent"].(string); got != "claude" {
		t.Errorf("agent: got %v", rec["agent"])
	}
	if got, _ := rec["is_error"].(bool); got {
		t.Errorf("is_error: got true, want false")
	}
	// Numeric fields land as float64 after json.Unmarshal into any.
	if got, _ := rec["duration_ms"].(float64); got != 12345 {
		t.Errorf("duration_ms: got %v", rec["duration_ms"])
	}
	if got, _ := rec["cost_usd"].(float64); got != 0.42 {
		t.Errorf("cost_usd: got %v", rec["cost_usd"])
	}
	if tailer.AnyError {
		t.Error("AnyError should be false for a successful run")
	}
}

func TestTailerFinalMessagePropagatesError(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.sqlite")
	db, err := openTestCallDB(t, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	streamPath := filepath.Join(dir, "agent.jsonl")
	content := strings.Join([]string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"FAILED: ran out of turns"}]}}`,
		`{"type":"result","total_cost_usd":0.05,"is_error":true,"duration_ms":1000,"num_turns":1,"usage":{"input_tokens":10,"output_tokens":20,"cache_read_input_tokens":0}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(streamPath, []byte(content), 0644); err != nil {
		t.Fatalf("write stream: %v", err)
	}

	id, err := db.InsertCall(&calldb.Call{
		ProjectID: "proj", Agent: "claude", Action: "exec", Role: "test",
		StartedAt: time.Now(), AgentFile: streamPath,
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := db.UpdateCall(id, &calldb.CallResult{
		EndedAt: time.Now(), IsError: true, ErrorMessage: "max turns",
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	var buf bytes.Buffer
	tailer := NewTailer(&buf, db, false, false)
	tailer.FinalMessageOnly = true
	tailer.AddSource(id, "test", "exec", streamPath, "")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := tailer.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !tailer.AnyError {
		t.Error("AnyError should be true when a run reports is_error")
	}
}

func openTestCallDB(t *testing.T, path string) (*calldb.CallDB, error) {
	t.Helper()
	return calldb.Open(path)
}

// lineWriter is an io.Writer that pushes each complete \n-terminated line
// to a channel as it is written. Used to assert that the Tailer emits one
// JSONL line per source AS each source completes — not buffered to the end.
type lineWriter struct {
	ch  chan string
	buf []byte
}

func newLineWriter() *lineWriter {
	return &lineWriter{ch: make(chan string, 16)}
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := -1
		for j, b := range w.buf {
			if b == '\n' {
				i = j
				break
			}
		}
		if i < 0 {
			break
		}
		line := string(w.buf[:i])
		w.buf = w.buf[i+1:]
		w.ch <- line
	}
	return len(p), nil
}

// TestTailerFinalMessageStreamsPerSource verifies the pipelining promise:
// when source A has finished and source B is still running, the JSONL line
// for A is flushed immediately rather than waiting for B. The test gates B's
// completion on observing A's line, so the assertion can ONLY succeed if A
// streamed out first.
func TestTailerFinalMessageStreamsPerSource(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.sqlite")
	db, err := openTestCallDB(t, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	// Source A: fully written, result line included.
	streamA := filepath.Join(dir, "a.jsonl")
	contentA := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"sA","model":"opus"}`,
		`{"type":"user"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"A done first"}]}}`,
		`{"type":"result","total_cost_usd":0.01,"duration_ms":1000,"num_turns":1,"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":0}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(streamA, []byte(contentA), 0644); err != nil {
		t.Fatalf("write A: %v", err)
	}
	idA, err := db.InsertCall(&calldb.Call{
		ProjectID: "proj", Agent: "claude", Action: "exec", Role: "alpha",
		StartedAt: time.Now(), AgentFile: streamA,
	})
	if err != nil {
		t.Fatalf("insert A: %v", err)
	}
	if err := db.UpdateCall(idA, &calldb.CallResult{
		EndedAt: time.Now(), DurationMS: 1000, CostUSD: 0.01, Turns: 1,
	}); err != nil {
		t.Fatalf("update A: %v", err)
	}

	// Source B: starts incomplete (no result line yet, still "running" from
	// the DB's perspective — no EndedAt either).
	streamB := filepath.Join(dir, "b.jsonl")
	partialB := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"sB","model":"opus"}`,
		`{"type":"user"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(streamB, []byte(partialB), 0644); err != nil {
		t.Fatalf("write B partial: %v", err)
	}
	idB, err := db.InsertCall(&calldb.Call{
		ProjectID: "proj", Agent: "claude", Action: "exec", Role: "beta",
		StartedAt: time.Now(), AgentFile: streamB,
	})
	if err != nil {
		t.Fatalf("insert B: %v", err)
	}

	lw := newLineWriter()
	tailer := NewTailer(lw, db, false, false)
	tailer.FinalMessageOnly = true
	tailer.PollInterval = 50 * time.Millisecond
	tailer.AddSource(idA, "alpha", "exec", streamA, "opus")
	tailer.AddSource(idB, "beta", "exec", streamB, "opus")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	runDone := make(chan struct{})
	go func() {
		_ = tailer.Run(ctx)
		close(runDone)
	}()

	// Read the first JSONL line — must be A. We do this BEFORE completing B,
	// proving the Tailer flushed A's record without waiting for B.
	var firstLine string
	select {
	case firstLine = <-lw.ch:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for first JSONL line — Tailer never flushed source A")
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(firstLine), &first); err != nil {
		t.Fatalf("first line not JSON: %v\n%s", err, firstLine)
	}
	if got, _ := first["exec_id"].(float64); int64(got) != idA {
		t.Fatalf("first line should be source A (id=%d), got id=%v", idA, first["exec_id"])
	}
	if first["final_message"] != "A done first" {
		t.Errorf("first line final_message: got %v", first["final_message"])
	}

	// Now complete B — append the assistant text and result line, then
	// update the DB row so the EndedAt path also triggers.
	appendB := strings.Join([]string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"B done second"}]}}`,
		`{"type":"result","total_cost_usd":0.02,"duration_ms":2000,"num_turns":1,"usage":{"input_tokens":20,"output_tokens":10,"cache_read_input_tokens":0}}`,
	}, "\n") + "\n"
	f, err := os.OpenFile(streamB, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("reopen B: %v", err)
	}
	if _, err := f.WriteString(appendB); err != nil {
		t.Fatalf("append B: %v", err)
	}
	f.Close()
	if err := db.UpdateCall(idB, &calldb.CallResult{
		EndedAt: time.Now(), DurationMS: 2000, CostUSD: 0.02, Turns: 1,
	}); err != nil {
		t.Fatalf("update B: %v", err)
	}

	// Read the second JSONL line — must be B.
	var secondLine string
	select {
	case secondLine = <-lw.ch:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for second JSONL line — Tailer never flushed source B after it completed")
	}
	var second map[string]any
	if err := json.Unmarshal([]byte(secondLine), &second); err != nil {
		t.Fatalf("second line not JSON: %v\n%s", err, secondLine)
	}
	if got, _ := second["exec_id"].(float64); int64(got) != idB {
		t.Errorf("second line should be source B (id=%d), got id=%v", idB, second["exec_id"])
	}
	if second["final_message"] != "B done second" {
		t.Errorf("second line final_message: got %v", second["final_message"])
	}

	cancel()
	select {
	case <-runDone:
	case <-time.After(2 * time.Second):
		t.Error("Tailer.Run did not return after cancel")
	}
}
