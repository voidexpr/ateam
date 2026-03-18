package runner

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTailerStaticFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stream.jsonl")

	content := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"s1","model":"opus"}`,
		`{"type":"user"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"hello"}]}}`,
		`{"type":"result","total_cost_usd":0.01,"duration_ms":5000,"num_turns":1,"usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":0}}`,
	}, "\n") + "\n"
	os.WriteFile(path, []byte(content), 0644)

	var buf bytes.Buffer
	tailer := NewTailer(&buf, nil, false, false)
	tailer.AddSource(1, "security", "run", path, "")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	tailer.Run(ctx)

	out := buf.String()
	if !strings.Contains(out, "Turn 1") {
		t.Errorf("expected Turn 1 in output, got: %s", out)
	}
	if !strings.Contains(out, "Result") {
		t.Errorf("expected Result in output, got: %s", out)
	}
	if !strings.Contains(out, "[1:security/run]") {
		t.Errorf("expected prefix in output, got: %s", out)
	}
}

func TestTailerWaitTimeout(t *testing.T) {
	var buf bytes.Buffer
	tailer := NewTailer(&buf, nil, false, false)
	tailer.WaitTimeout = 100 * time.Millisecond // fast timeout for test

	ctx := context.Background()
	tailer.Run(ctx)

	if !strings.Contains(buf.String(), "No running processes found") {
		t.Errorf("expected timeout message, got: %s", buf.String())
	}
}

func TestTailerGrowingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stream.jsonl")

	// Start with partial content
	f, _ := os.Create(path)
	f.WriteString(`{"type":"system","subtype":"init","session_id":"s1","model":"opus"}` + "\n")
	f.WriteString(`{"type":"user"}` + "\n")
	f.Close()

	var buf bytes.Buffer
	tailer := NewTailer(&buf, nil, false, false)
	tailer.PollInterval = 50 * time.Millisecond
	tailer.AddSource(1, "test", "run", path, "")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Append result after a delay
	go func() {
		time.Sleep(200 * time.Millisecond)
		f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
		f.WriteString(`{"type":"result","total_cost_usd":0.01,"duration_ms":5000,"num_turns":1,"usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":0}}` + "\n")
		f.Close()
	}()

	tailer.Run(ctx)

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
