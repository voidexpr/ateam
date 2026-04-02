package runner

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStreamFormatterSystem(t *testing.T) {
	f := &StreamFormatter{Color: false}
	line := []byte(`{"type":"system","subtype":"init","session_id":"abc123","model":"opus","cwd":"/tmp","claude_code_version":"1.0"}`)
	out := f.FormatLine(line)
	if !strings.Contains(out, "session abc123") {
		t.Errorf("expected session id, got: %s", out)
	}
	if !strings.Contains(out, "model=opus") {
		t.Errorf("expected model, got: %s", out)
	}
}

func TestStreamFormatterUser(t *testing.T) {
	f := &StreamFormatter{Color: false}
	line := []byte(`{"type":"user"}`)
	out := f.FormatLine(line)
	if !strings.Contains(out, "=== Turn 1 ===") {
		t.Errorf("expected turn 1, got: %s", out)
	}
	if f.TurnNum != 1 {
		t.Errorf("expected TurnNum=1, got %d", f.TurnNum)
	}

	out = f.FormatLine(line)
	if !strings.Contains(out, "=== Turn 2 ===") {
		t.Errorf("expected turn 2, got: %s", out)
	}
}

func TestStreamFormatterToolUse(t *testing.T) {
	f := &StreamFormatter{Color: false}
	line := []byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"ls -la"}}]}}`)
	out := f.FormatLine(line)
	if !strings.Contains(out, "tool #1: Bash") {
		t.Errorf("expected tool #1 Bash, got: %s", out)
	}
	if !strings.Contains(out, "ls -la") {
		t.Errorf("expected command detail, got: %s", out)
	}
	if f.ToolCount != 1 {
		t.Errorf("expected ToolCount=1, got %d", f.ToolCount)
	}
}

func TestStreamFormatterText(t *testing.T) {
	f := &StreamFormatter{Color: false}
	line := []byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"Hello world"}]}}`)
	out := f.FormatLine(line)
	if !strings.Contains(out, "text #1:") {
		t.Errorf("expected text #1, got: %s", out)
	}
	if !strings.Contains(out, "Hello world") {
		t.Errorf("expected text content, got: %s", out)
	}
}

func TestStreamFormatterResult(t *testing.T) {
	f := &StreamFormatter{Color: false}
	line := []byte(`{"type":"result","total_cost_usd":0.15,"duration_ms":60000,"num_turns":3,"is_error":false,"usage":{"input_tokens":1000,"output_tokens":500,"cache_read_input_tokens":200,"cache_write_input_tokens":75}}`)
	out := f.FormatLine(line)
	if !strings.Contains(out, "=== Result ===") {
		t.Errorf("expected result header, got: %s", out)
	}
	if !strings.Contains(out, "$0.15") {
		t.Errorf("expected cost, got: %s", out)
	}
	if !strings.Contains(out, "cache_write=75") {
		t.Errorf("expected cache write tokens, got: %s", out)
	}
	if !f.HasResult() {
		t.Error("expected HasResult=true")
	}
}

func TestStreamFormatterPrefix(t *testing.T) {
	f := &StreamFormatter{Color: false, Prefix: "[42:sec/run] "}
	line := []byte(`{"type":"user"}`)
	out := f.FormatLine(line)
	if !strings.HasPrefix(strings.TrimLeft(out, "\n"), "[42:sec/run]") {
		t.Errorf("expected prefix, got: %s", out)
	}
}

func TestStreamFormatterFormatFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stream.jsonl")

	content := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"s1","model":"opus"}`,
		`{"type":"user"}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/tmp/foo.go"}}]}}`,
		`{"type":"result","total_cost_usd":0.01,"duration_ms":5000,"num_turns":1,"usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":0}}`,
	}, "\n")
	_ = os.WriteFile(path, []byte(content), 0644)

	f := &StreamFormatter{Color: false}
	var buf bytes.Buffer
	if err := f.FormatFile(path, &buf); err != nil {
		t.Fatalf("FormatFile: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "session s1") {
		t.Errorf("expected session in output")
	}
	if !strings.Contains(out, "Turn 1") {
		t.Errorf("expected turn in output")
	}
	if !strings.Contains(out, "Read") {
		t.Errorf("expected tool in output")
	}
	if !strings.Contains(out, "Result") {
		t.Errorf("expected result in output")
	}
}

func TestToolDetail(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{"Bash", `{"command":"echo hello\nworld"}`, "echo hello"},
		{"Read", `{"file_path":"/tmp/foo.go"}`, "/tmp/foo.go"},
		{"Glob", `{"pattern":"**/*.go"}`, "**/*.go"},
		{"Grep", `{"pattern":"func main"}`, "func main"},
		{"WebFetch", `{"url":"https://example.com"}`, "https://example.com"},
		{"WebSearch", `{"query":"golang testing"}`, "golang testing"},
		{"Unknown", `{"foo":"bar"}`, ""},
	}
	for _, tt := range tests {
		got := toolDetail(tt.name, []byte(tt.input))
		if got != tt.expect {
			t.Errorf("toolDetail(%s): got %q, want %q", tt.name, got, tt.expect)
		}
	}
}
