package runner

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ateam/internal/streamutil"
)

func TestHTMLFormatterSystem(t *testing.T) {
	f := &HTMLStreamFormatter{}
	ev := &SystemLine{SessionID: "abc123", Model: "opus", Version: "1.0"}
	out := f.formatEvent(ev)

	if !strings.Contains(out, "session abc123") {
		t.Errorf("expected session id, got: %s", out)
	}
	if !strings.Contains(out, "model=opus") {
		t.Errorf("expected model, got: %s", out)
	}
	if !strings.Contains(out, `class="sl-session"`) {
		t.Errorf("expected sl-session class, got: %s", out)
	}
	if !strings.Contains(out, "v1.0") {
		t.Errorf("expected version, got: %s", out)
	}
}

func TestHTMLFormatterUser(t *testing.T) {
	f := &HTMLStreamFormatter{}
	out := f.formatEvent(&UserLine{})

	if !strings.Contains(out, "=== Turn 1 ===") {
		t.Errorf("expected turn 1, got: %s", out)
	}
	if !strings.Contains(out, `class="sl-turn"`) {
		t.Errorf("expected sl-turn class, got: %s", out)
	}
	if f.TurnNum != 1 {
		t.Errorf("expected TurnNum=1, got %d", f.TurnNum)
	}

	out = f.formatEvent(&UserLine{})
	if !strings.Contains(out, "=== Turn 2 ===") {
		t.Errorf("expected turn 2, got: %s", out)
	}
}

func TestHTMLFormatterToolCall(t *testing.T) {
	f := &HTMLStreamFormatter{}
	out := f.formatEvent(&ToolCallLine{Name: "Bash", Detail: "ls -la"})

	if !strings.Contains(out, `class="sl-tool"`) {
		t.Errorf("expected sl-tool class, got: %s", out)
	}
	if !strings.Contains(out, `class="sl-tool-num"`) {
		t.Errorf("expected sl-tool-num class, got: %s", out)
	}
	if !strings.Contains(out, `class="sl-tool-name"`) {
		t.Errorf("expected sl-tool-name class, got: %s", out)
	}
	if !strings.Contains(out, "tool #1:") {
		t.Errorf("expected tool #1, got: %s", out)
	}
	if !strings.Contains(out, "Bash") {
		t.Errorf("expected Bash, got: %s", out)
	}
	if !strings.Contains(out, "ls -la") {
		t.Errorf("expected detail, got: %s", out)
	}
	if f.ToolCount != 1 {
		t.Errorf("expected ToolCount=1, got %d", f.ToolCount)
	}
}

func TestHTMLFormatterText(t *testing.T) {
	f := &HTMLStreamFormatter{}
	out := f.formatEvent(&TextLine{Text: "Hello world"})

	if !strings.Contains(out, `class="sl-text"`) {
		t.Errorf("expected sl-text class, got: %s", out)
	}
	if !strings.Contains(out, "text #1:") {
		t.Errorf("expected text #1, got: %s", out)
	}
	if !strings.Contains(out, "Hello world") {
		t.Errorf("expected text content, got: %s", out)
	}
	if f.TextCount != 1 {
		t.Errorf("expected TextCount=1, got %d", f.TextCount)
	}
}

func TestHTMLFormatterResult(t *testing.T) {
	f := &HTMLStreamFormatter{}
	out := f.formatEvent(&ResultLine{
		Cost:             0.15,
		DurationMS:       60000,
		Turns:            3,
		InputTokens:      1000,
		OutputTokens:     500,
		CacheReadTokens:  200,
		CacheWriteTokens: 75,
	})

	if !strings.Contains(out, `class="sl-result"`) {
		t.Errorf("expected sl-result class, got: %s", out)
	}
	if !strings.Contains(out, "=== Result ===") {
		t.Errorf("expected result header, got: %s", out)
	}
	if !strings.Contains(out, "$0.15") {
		t.Errorf("expected cost, got: %s", out)
	}
	if !strings.Contains(out, "cache_write=75") {
		t.Errorf("expected cache_write tokens, got: %s", out)
	}
	if !strings.Contains(out, "Status:    ok") {
		t.Errorf("expected ok status, got: %s", out)
	}
	if f.hasResult != true {
		t.Error("expected hasResult=true")
	}
}

func TestHTMLFormatterError(t *testing.T) {
	f := &HTMLStreamFormatter{}
	out := f.formatEvent(&ErrorLine{Message: "something went wrong"})

	if !strings.Contains(out, `class="sl-error"`) {
		t.Errorf("expected sl-error class, got: %s", out)
	}
	if !strings.Contains(out, "something went wrong") {
		t.Errorf("expected error message, got: %s", out)
	}
}

func TestHTMLFormatterEscapesSpecialChars(t *testing.T) {
	f := &HTMLStreamFormatter{}

	// Text with HTML-special characters
	out := f.formatEvent(&TextLine{Text: `<script>alert("xss")</script> & more`})
	if strings.Contains(out, "<script>") {
		t.Errorf("raw <script> tag should be escaped, got: %s", out)
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Errorf("expected escaped <script>, got: %s", out)
	}
	if !strings.Contains(out, "&amp; more") {
		t.Errorf("expected escaped &, got: %s", out)
	}

	// Tool name with special chars
	f2 := &HTMLStreamFormatter{}
	out = f2.formatEvent(&ToolCallLine{Name: "Tool<>", Detail: "a&b"})
	if strings.Contains(out, "Tool<>") {
		t.Errorf("raw angle brackets should be escaped in tool name, got: %s", out)
	}
	if !strings.Contains(out, "Tool&lt;&gt;") {
		t.Errorf("expected escaped tool name, got: %s", out)
	}
	if !strings.Contains(out, "a&amp;b") {
		t.Errorf("expected escaped detail, got: %s", out)
	}

	// Error with special chars
	f3 := &HTMLStreamFormatter{}
	out = f3.formatEvent(&ErrorLine{Message: `<b>"error"</b>`})
	if strings.Contains(out, "<b>") {
		t.Errorf("raw <b> tag should be escaped, got: %s", out)
	}
	if !strings.Contains(out, "&lt;b&gt;") {
		t.Errorf("expected escaped error message, got: %s", out)
	}

	// Session with special chars
	f4 := &HTMLStreamFormatter{}
	out = f4.formatEvent(&SystemLine{SessionID: "s<1>", Model: "m&1"})
	if strings.Contains(out, "s<1>") {
		t.Errorf("raw angle brackets in session should be escaped, got: %s", out)
	}
}

func TestHTMLFormatterDetailsStructure(t *testing.T) {
	f := &HTMLStreamFormatter{Verbose: true}

	// Verbose tool call should render pre with input
	out := f.formatEvent(&ToolCallLine{
		Name: "Bash",
		Claude: &ToolCallClaudeExt{
			Input: []byte(`{"command":"echo hello"}`),
		},
	})
	if !strings.Contains(out, `<pre class="sl-tool-input">`) {
		t.Errorf("expected sl-tool-input pre in verbose mode, got: %s", out)
	}
	if !strings.Contains(out, `{&#34;command&#34;:&#34;echo hello&#34;}`) {
		t.Errorf("expected escaped JSON input, got: %s", out)
	}

	// Verbose text should use pre tag
	out = f.formatEvent(&TextLine{Text: "line1\nline2"})
	if !strings.Contains(out, `<pre class="sl-text-body">`) {
		t.Errorf("expected sl-text-body pre in verbose mode, got: %s", out)
	}

	// Verbose thinking
	out = f.formatEvent(&ThinkingLine{Text: "pondering..."})
	if !strings.Contains(out, `class="sl-thinking"`) {
		t.Errorf("expected sl-thinking class, got: %s", out)
	}
	if !strings.Contains(out, `<pre class="sl-thinking-body">`) {
		t.Errorf("expected sl-thinking-body pre, got: %s", out)
	}
}

func TestHTMLFormatterThinkingPreviewNonVerbose(t *testing.T) {
	f := &HTMLStreamFormatter{Verbose: false}
	out := f.formatEvent(&ThinkingLine{Text: "secret thoughts here"})
	if !strings.Contains(out, "thinking:") {
		t.Errorf("expected thinking preview, got: %s", out)
	}
	if !strings.Contains(out, "secret thoughts") {
		t.Errorf("expected preview text in output, got: %s", out)
	}
}

func TestHTMLFormatterToolResultSizeNonVerbose(t *testing.T) {
	f := &HTMLStreamFormatter{Verbose: false}
	out := f.formatEvent(&ToolResultLine{Content: "line1\nline2\nline3\n"})
	if !strings.Contains(out, `class="sl-dim"`) {
		t.Errorf("expected sl-dim class, got: %s", out)
	}
	if !strings.Contains(out, "result:") || !strings.Contains(out, "3 lines") {
		t.Errorf("expected size+lines summary, got: %s", out)
	}
	if strings.Contains(out, "line1") {
		t.Errorf("body should not be echoed, got: %s", out)
	}
}

func TestHTMLFormatterToolResultVerboseDropsBody(t *testing.T) {
	// Verbose previously dumped the truncated body; now it shows the same
	// compact size+lines line so HTML matches the text formatter.
	f := &HTMLStreamFormatter{Verbose: true}
	out := f.formatEvent(&ToolResultLine{Content: "result <data>"})
	if !strings.Contains(out, `class="sl-dim"`) {
		t.Errorf("expected sl-dim class, got: %s", out)
	}
	if !strings.Contains(out, "result:") || !strings.Contains(out, "1 lines") {
		t.Errorf("expected size+lines summary, got: %s", out)
	}
	if strings.Contains(out, "&lt;data&gt;") {
		t.Errorf("verbose should NOT echo escaped body, got: %s", out)
	}
}

func TestHTMLFormatterFormatFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stream.jsonl")

	content := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"s1","model":"opus"}`,
		`{"type":"user"}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/tmp/foo.go"}}]}}`,
		`{"type":"result","total_cost_usd":0.01,"duration_ms":5000,"num_turns":1,"usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":0}}`,
	}, "\n")
	_ = os.WriteFile(path, []byte(content), 0644)

	f := &HTMLStreamFormatter{}
	var buf bytes.Buffer
	if err := f.FormatFile(path, &buf); err != nil {
		t.Fatalf("FormatFile: %v", err)
	}

	out := buf.String()
	if !strings.HasPrefix(out, `<div class="stream-log">`) {
		t.Errorf("expected stream-log wrapper start")
	}
	if !strings.HasSuffix(out, `</div>`) {
		t.Errorf("expected stream-log wrapper end")
	}
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

func TestHTMLFormatterFormatFileWithXSSContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stream.jsonl")

	content := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"s1","model":"opus"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"<img src=x onerror=alert(1)>"}]}}`,
		`{"type":"result","total_cost_usd":0.01,"duration_ms":5000,"num_turns":1,"usage":{"input_tokens":100,"output_tokens":50}}`,
	}, "\n")
	_ = os.WriteFile(path, []byte(content), 0644)

	f := &HTMLStreamFormatter{}
	var buf bytes.Buffer
	if err := f.FormatFile(path, &buf); err != nil {
		t.Fatalf("FormatFile: %v", err)
	}

	out := buf.String()
	if strings.Contains(out, "<img") {
		t.Errorf("raw <img> tag should be escaped in output: %s", out)
	}
	if !strings.Contains(out, "&lt;img") {
		t.Errorf("expected escaped <img> tag, got: %s", out)
	}
}

func TestHTMLFormatterResultErrorStatus(t *testing.T) {
	f := &HTMLStreamFormatter{}
	out := f.formatEvent(&ResultLine{IsError: true, DurationMS: 1000})
	if !strings.Contains(out, `<span class="sl-error">error</span>`) {
		t.Errorf("expected sl-error span around 'error', got: %s", out)
	}
}

func TestHTMLFormatterResultEstimated(t *testing.T) {
	f := &HTMLStreamFormatter{Model: "opus"}
	out := f.formatEvent(&ResultLine{
		DurationMS:   1000,
		InputTokens:  100,
		OutputTokens: 50,
	})
	// Cost is 0 (from result) but model is set, so estimation may apply.
	// The "(estimated)" label should appear if cost > 0 after estimation.
	if strings.Contains(out, "(estimated)") && !strings.Contains(out, "sl-dim") {
		t.Errorf("estimated label should have sl-dim class")
	}
}

func TestHTMLFormatterEmptyToolResult(t *testing.T) {
	// Truly empty content produces no output, but whitespace-only content
	// is rendered with its actual byte count — whitespace IS information.
	f := &HTMLStreamFormatter{Verbose: true}
	if out := f.formatEvent(&ToolResultLine{Content: ""}); out != "" {
		t.Errorf("empty tool result should produce no output, got: %s", out)
	}
	if out := f.formatEvent(&ToolResultLine{Content: "   "}); !strings.Contains(out, "3B") {
		t.Errorf("whitespace tool result should show byte count, got: %s", out)
	}
}

func TestHTMLFormatterEmptySystemLine(t *testing.T) {
	f := &HTMLStreamFormatter{}
	out := f.formatEvent(&SystemLine{})
	if out != "" {
		t.Errorf("system line with no session/model should produce no output, got: %s", out)
	}
}

func TestHTMLFormatterVerboseCwd(t *testing.T) {
	f := &HTMLStreamFormatter{Verbose: true}
	out := f.formatEvent(&SystemLine{SessionID: "s1", Model: "opus", Cwd: "/home/user"})
	if !strings.Contains(out, "cwd: /home/user") {
		t.Errorf("expected cwd in verbose mode, got: %s", out)
	}
}

func TestHTMLFormatterRateLimit(t *testing.T) {
	resets := time.Now().Add(2*time.Hour + 30*time.Minute).Unix()
	line := []byte(fmt.Sprintf(`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":%d,"rateLimitType":"five_hour","overageStatus":"rejected","overageDisabledReason":"org_level_disabled_until","isUsingOverage":false}}`, resets))

	var buf bytes.Buffer
	dir := t.TempDir()
	path := filepath.Join(dir, "rl.jsonl")
	if err := os.WriteFile(path, line, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	f := &HTMLStreamFormatter{}
	if err := f.FormatFile(path, &buf); err != nil {
		t.Fatalf("FormatFile: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `class="sl-rate-limit sl-dim"`) {
		t.Errorf("expected sl-rate-limit class, got: %s", out)
	}
	if !strings.Contains(out, "five_hour") || !strings.Contains(out, "allowed") {
		t.Errorf("expected type+status, got: %s", out)
	}
	if !strings.Contains(out, "resets in") {
		t.Errorf("expected resets-in countdown, got: %s", out)
	}
	if strings.Contains(out, "org_level_disabled_until") {
		t.Errorf("non-verbose should not include disable reason, got: %s", out)
	}

	var bv bytes.Buffer
	fv := &HTMLStreamFormatter{Verbose: true}
	if err := fv.FormatFile(path, &bv); err != nil {
		t.Fatalf("FormatFile verbose: %v", err)
	}
	outv := bv.String()
	if !strings.Contains(outv, "org_level_disabled_until") {
		t.Errorf("verbose should include disable reason, got: %s", outv)
	}
	if !strings.Contains(outv, "resetsAt=") {
		t.Errorf("verbose should include absolute resetsAt, got: %s", outv)
	}
}

func TestHTMLFormatterUsageSuffixVerbose(t *testing.T) {
	usage := streamutil.AssistantUsage{
		InputTokens:              8,
		OutputTokens:             1251,
		CacheCreationInputTokens: 38521,
		CacheReadInputTokens:     144121,
	}
	usage.CacheCreation.Ephemeral1hInputTokens = 38521
	tl := &TextLine{Text: "hi", Usage: &usage}

	fv := &HTMLStreamFormatter{Verbose: true, Model: "claude-sonnet-4-6"}
	out := fv.formatEvent(tl)
	if !strings.Contains(out, `class="sl-usage sl-dim"`) {
		t.Errorf("expected sl-usage class, got: %s", out)
	}
	if !strings.Contains(out, "in=8") || !strings.Contains(out, "out=1.3K") {
		t.Errorf("expected token counts, got: %s", out)
	}
	// 8 + 144121 + 38521 = 182650 → 91% of 200000
	if !strings.Contains(out, "91%") {
		t.Errorf("expected ctx percentage 91%%, got: %s", out)
	}

	// Non-verbose: no usage suffix.
	f := &HTMLStreamFormatter{Model: "claude-sonnet-4-6"}
	out2 := f.formatEvent(tl)
	if strings.Contains(out2, "sl-usage") {
		t.Errorf("non-verbose should not show usage, got: %s", out2)
	}
}

func TestHTMLFormatterAPIErrorRed(t *testing.T) {
	tl := &TextLine{Text: "API Error: Stream idle timeout - partial response received"}
	f := &HTMLStreamFormatter{}
	out := f.formatEvent(tl)
	if !strings.Contains(out, `class="sl-text sl-error"`) {
		t.Errorf("expected sl-error on synthetic API error text, got: %s", out)
	}

	// Non-error text should NOT get sl-error.
	plain := &TextLine{Text: "Hello there"}
	out2 := f.formatEvent(plain)
	if strings.Contains(out2, "sl-error") {
		t.Errorf("plain text should not be sl-error, got: %s", out2)
	}
}

func TestHTMLFormatterToolResultErrorRed(t *testing.T) {
	tr := &ToolResultLine{Content: "boom\n", IsError: true}
	f := &HTMLStreamFormatter{}
	out := f.formatEvent(tr)
	if !strings.Contains(out, `class="sl-error"`) {
		t.Errorf("expected sl-error on errored tool_result, got: %s", out)
	}
	if !strings.Contains(out, "(error)") {
		t.Errorf("expected (error) marker, got: %s", out)
	}
}

func TestHTMLFormatterSessionStart(t *testing.T) {
	start := time.Date(2026, 4, 27, 23, 8, 54, 0, time.Local)
	f := &HTMLStreamFormatter{SessionStart: start}

	// System header should include the started clock.
	out := f.formatEvent(&SystemLine{SessionID: "abc", Model: "claude-sonnet-4-6"})
	if !strings.Contains(out, "started=2026-04-27 23:08:54") {
		t.Errorf("expected started clock on session header, got: %s", out)
	}

	// Result block should show Started/Ended computed from SessionStart + duration_ms.
	out2 := f.formatEvent(&ResultLine{DurationMS: 60000, Turns: 1})
	if !strings.Contains(out2, "Started:") || !strings.Contains(out2, "2026-04-27 23:08:54") {
		t.Errorf("expected Started clock in result, got: %s", out2)
	}
	if !strings.Contains(out2, "Ended:") || !strings.Contains(out2, "2026-04-27 23:09:54") {
		t.Errorf("expected Ended clock in result, got: %s", out2)
	}
}
