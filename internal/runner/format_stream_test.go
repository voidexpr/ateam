package runner

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestStreamFormatterRateLimit(t *testing.T) {
	resets := time.Now().Add(2*time.Hour + 30*time.Minute).Unix()
	line := []byte(fmt.Sprintf(`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":%d,"rateLimitType":"five_hour","overageStatus":"rejected","overageDisabledReason":"org_level_disabled_until","isUsingOverage":false}}`, resets))

	f := &StreamFormatter{Color: false}
	out := f.FormatLine(line)
	if !strings.Contains(out, "rate_limit:") {
		t.Errorf("expected rate_limit prefix, got: %q", out)
	}
	if !strings.Contains(out, "five_hour") || !strings.Contains(out, "allowed") {
		t.Errorf("expected type+status, got: %q", out)
	}
	if !strings.Contains(out, "resets in") {
		t.Errorf("expected resets-in countdown, got: %q", out)
	}
	if !strings.Contains(out, "overage rejected") {
		t.Errorf("expected overage status, got: %q", out)
	}
	if strings.Contains(out, "org_level_disabled_until") {
		t.Errorf("non-verbose should not include disable reason, got: %q", out)
	}

	fv := &StreamFormatter{Color: false, Verbose: true}
	outv := fv.FormatLine(line)
	if !strings.Contains(outv, "org_level_disabled_until") {
		t.Errorf("verbose should include disable reason, got: %q", outv)
	}
	if !strings.Contains(outv, "resetsAt=") {
		t.Errorf("verbose should include absolute resetsAt, got: %q", outv)
	}
}

func TestStreamFormatterThinkingNonVerbose(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"content":[{"type":"thinking","text":"I notice an issue: foo collides with bar in baz.go"}]}}`)
	f := &StreamFormatter{Color: false}
	out := f.FormatLine(line)
	if !strings.Contains(out, "thinking:") {
		t.Errorf("non-verbose should still show thinking line, got: %q", out)
	}
	if !strings.Contains(out, "I notice an issue") {
		t.Errorf("expected thinking preview, got: %q", out)
	}
}

func TestStreamFormatterToolResultNonVerbose(t *testing.T) {
	// User event nests a tool_result block (modern claude format).
	line := []byte(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_x","content":"line1\nline2\nline3\n","is_error":false}]}}`)
	f := &StreamFormatter{Color: false}
	out := f.FormatLine(line)
	if !strings.Contains(out, "Turn 1") {
		t.Errorf("expected turn divider, got: %q", out)
	}
	if !strings.Contains(out, "result:") {
		t.Errorf("non-verbose should show tool_result size line, got: %q", out)
	}
	if !strings.Contains(out, "3 lines") {
		t.Errorf("expected line count, got: %q", out)
	}
	if strings.Contains(out, "line1") {
		t.Errorf("non-verbose should not echo content, got: %q", out)
	}
}

func TestStreamFormatterToolResultErrorMark(t *testing.T) {
	line := []byte(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_x","content":"boom","is_error":true}]}}`)
	f := &StreamFormatter{Color: false}
	out := f.FormatLine(line)
	if !strings.Contains(out, "(error)") {
		t.Errorf("expected error mark, got: %q", out)
	}
}

func TestStreamFormatterUsageSuffixVerbose(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":8,"output_tokens":1251,"cache_creation_input_tokens":38521,"cache_read_input_tokens":144121,"cache_creation":{"ephemeral_1h_input_tokens":38521,"ephemeral_5m_input_tokens":0}}}}`)

	fv := &StreamFormatter{Color: false, Verbose: true, Model: "claude-sonnet-4-6"}
	outv := fv.FormatLine(line)
	if !strings.Contains(outv, "in=8") {
		t.Errorf("expected in=8, got: %q", outv)
	}
	if !strings.Contains(outv, "out=1.3K") {
		t.Errorf("expected out=1.3K, got: %q", outv)
	}
	if !strings.Contains(outv, "cache_read=144.1K") {
		t.Errorf("expected cache_read=144.1K, got: %q", outv)
	}
	if !strings.Contains(outv, "cc_1h=38.5K") {
		t.Errorf("expected cc_1h=38.5K, got: %q", outv)
	}
	// 8 + 144121 + 38521 = 182650 → 91% of 200000
	if !strings.Contains(outv, "91%") {
		t.Errorf("expected ctx percentage 91%%, got: %q", outv)
	}

	// Non-verbose suppresses the usage suffix.
	f := &StreamFormatter{Color: false, Model: "claude-sonnet-4-6"}
	out := f.FormatLine(line)
	if strings.Contains(out, "in=8") {
		t.Errorf("non-verbose should not show usage suffix, got: %q", out)
	}
}

func TestStreamFormatterUsageOnEveryBlock(t *testing.T) {
	// Multi-block message: thinking + tool_use. Usage applies to both
	// (claude charges per message); we want the suffix on each block so
	// it's visible per render line, even though the numbers repeat.
	line := []byte(`{"type":"assistant","message":{"content":[{"type":"thinking","text":"plan"},{"type":"tool_use","name":"Read","input":{"file_path":"/x"}}],"usage":{"input_tokens":10,"output_tokens":20}}}`)
	fv := &StreamFormatter{Color: false, Verbose: true}
	out := fv.FormatLine(line)
	count := strings.Count(out, "in=10")
	if count != 2 {
		t.Errorf("expected usage suffix twice (one per block), got %d times in: %q", count, out)
	}
}

func TestStreamFormatterToolResultBodyDroppedInVerbose(t *testing.T) {
	// Verbose used to dump the truncated body; now both modes show only
	// the size + line count.
	line := []byte(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t","content":"line1\nline2\nline3\n"}]}}`)
	fv := &StreamFormatter{Color: false, Verbose: true}
	out := fv.FormatLine(line)
	if !strings.Contains(out, "result:") || !strings.Contains(out, "3 lines") {
		t.Errorf("expected size+lines summary, got: %q", out)
	}
	if strings.Contains(out, "line1") || strings.Contains(out, "line2") {
		t.Errorf("verbose should NOT echo tool_result body, got: %q", out)
	}
}

func TestStreamFormatterErrorMessagesRed(t *testing.T) {
	const reset = "\x1b[0m"
	const red = "\x1b[31m"

	// Synthetic API Error text → red.
	syn := []byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"API Error: Stream idle timeout - partial response received"}]}}`)
	f := &StreamFormatter{Color: true}
	out := f.FormatLine(syn)
	if !strings.Contains(out, red) || !strings.Contains(out, reset) {
		t.Errorf("expected red ANSI escape on API Error text, got: %q", out)
	}

	// tool_result with is_error → red.
	bad := []byte(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t","content":"boom","is_error":true}]}}`)
	f2 := &StreamFormatter{Color: true}
	out2 := f2.FormatLine(bad)
	if !strings.Contains(out2, red) {
		t.Errorf("expected red ANSI on errored tool_result, got: %q", out2)
	}

	// Result with is_error → "error" status in red.
	res := []byte(`{"type":"result","is_error":true,"duration_ms":1000,"num_turns":1,"usage":{"input_tokens":1,"output_tokens":1}}`)
	f3 := &StreamFormatter{Color: true}
	out3 := f3.FormatLine(res)
	if !strings.Contains(out3, red+"error"+reset) {
		t.Errorf("expected red 'error' status, got: %q", out3)
	}

	// Plain assistant text without error prefix → NOT red.
	plain := []byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"Hello there"}]}}`)
	f4 := &StreamFormatter{Color: true}
	out4 := f4.FormatLine(plain)
	if strings.Contains(out4, red) {
		t.Errorf("plain text should not be red, got: %q", out4)
	}
}

func TestStreamFormatterSessionStart(t *testing.T) {
	start := time.Date(2026, 4, 27, 23, 8, 54, 0, time.Local)
	f := &StreamFormatter{Color: false, SessionStart: start}

	// System line should include the started clock.
	sysLine := []byte(`{"type":"system","subtype":"init","session_id":"abc","model":"claude-sonnet-4-6"}`)
	out := f.FormatLine(sysLine)
	if !strings.Contains(out, "started=2026-04-27 23:08:54") {
		t.Errorf("expected started clock, got: %q", out)
	}

	// Result line should compute Ended from SessionStart + duration_ms.
	// 60_000 ms + 23:08:54 → 23:09:54
	resLine := []byte(`{"type":"result","duration_ms":60000,"num_turns":1,"usage":{"input_tokens":10,"output_tokens":5}}`)
	out2 := f.FormatLine(resLine)
	if !strings.Contains(out2, "Started:") {
		t.Errorf("expected Started: row, got: %q", out2)
	}
	if !strings.Contains(out2, "Ended:     2026-04-27 23:09:54") {
		t.Errorf("expected Ended: row, got: %q", out2)
	}
}

func TestStreamFormatterModernUserToolResult(t *testing.T) {
	// Regression: confirm modern claude format (tool_result nested inside
	// user.message.content) renders both the turn divider AND the result
	// size line.
	line := []byte(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_x","content":"a\nb\nc\nd\n"}]}}`)
	f := &StreamFormatter{Color: false}
	out := f.FormatLine(line)
	if !strings.Contains(out, "Turn 1") {
		t.Errorf("expected turn divider, got: %q", out)
	}
	if !strings.Contains(out, "4 lines") {
		t.Errorf("expected 4 lines, got: %q", out)
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
