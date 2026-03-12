package runner

import (
	"testing"
)

func TestParseSystemInit(t *testing.T) {
	line := []byte(`{"type":"system","subtype":"init","session_id":"abc-123"}`)
	typ, ev, err := parseStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if typ != "system" {
		t.Fatalf("expected type 'system', got %q", typ)
	}
	sys, ok := ev.(*systemEvent)
	if !ok {
		t.Fatalf("expected *systemEvent, got %T", ev)
	}
	if sys.Subtype != "init" {
		t.Errorf("expected subtype 'init', got %q", sys.Subtype)
	}
	if sys.SessionID != "abc-123" {
		t.Errorf("expected session_id 'abc-123', got %q", sys.SessionID)
	}
}

func TestParseAssistantTextBlocks(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"Hello "},{"type":"text","text":"world"}]}}`)
	typ, ev, err := parseStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if typ != "assistant" {
		t.Fatalf("expected type 'assistant', got %q", typ)
	}
	ast, ok := ev.(*assistantEvent)
	if !ok {
		t.Fatalf("expected *assistantEvent, got %T", ev)
	}
	if len(ast.Message.Content) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(ast.Message.Content))
	}
	text := extractReportText(ast)
	if text != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", text)
	}
}

func TestParseAssistantToolUse(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read"}]}}`)
	typ, ev, err := parseStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if typ != "assistant" {
		t.Fatalf("expected type 'assistant', got %q", typ)
	}
	ast, ok := ev.(*assistantEvent)
	if !ok {
		t.Fatalf("expected *assistantEvent, got %T", ev)
	}
	if len(ast.Message.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(ast.Message.Content))
	}
	if ast.Message.Content[0].Name != "Read" {
		t.Errorf("expected tool name 'Read', got %q", ast.Message.Content[0].Name)
	}
}

func TestParseResult(t *testing.T) {
	line := []byte(`{"type":"result","total_cost_usd":0.12,"cost_usd":0.10,"duration_ms":5000,"num_turns":3,"is_error":false,"usage":{"input_tokens":100,"output_tokens":200,"cache_read_input_tokens":50}}`)
	typ, ev, err := parseStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if typ != "result" {
		t.Fatalf("expected type 'result', got %q", typ)
	}
	res, ok := ev.(*resultEvent)
	if !ok {
		t.Fatalf("expected *resultEvent, got %T", ev)
	}
	if res.TotalCostUSD != 0.12 {
		t.Errorf("expected total_cost_usd 0.12, got %f", res.TotalCostUSD)
	}
	if res.CostUSD != 0.10 {
		t.Errorf("expected cost_usd 0.10, got %f", res.CostUSD)
	}
	if res.DurationMS != 5000 {
		t.Errorf("expected duration_ms 5000, got %d", res.DurationMS)
	}
	if res.NumTurns != 3 {
		t.Errorf("expected num_turns 3, got %d", res.NumTurns)
	}
	if res.IsError {
		t.Error("expected is_error false")
	}
	if res.Usage.InputTokens != 100 {
		t.Errorf("expected input_tokens 100, got %d", res.Usage.InputTokens)
	}
	if res.Usage.OutputTokens != 200 {
		t.Errorf("expected output_tokens 200, got %d", res.Usage.OutputTokens)
	}
	if res.Usage.CacheReadInputTokens != 50 {
		t.Errorf("expected cache_read_input_tokens 50, got %d", res.Usage.CacheReadInputTokens)
	}
}

func TestParseUnknownType(t *testing.T) {
	line := []byte(`{"type":"something_new","data":"value"}`)
	typ, ev, err := parseStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if typ != "" {
		t.Errorf("expected empty type for unknown, got %q", typ)
	}
	if ev != nil {
		t.Errorf("expected nil event for unknown type, got %v", ev)
	}
}

func TestParseEmptyLine(t *testing.T) {
	typ, ev, err := parseStreamLine([]byte(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if typ != "" || ev != nil {
		t.Errorf("expected empty result for empty line, got type=%q ev=%v", typ, ev)
	}
}

func TestExtractReportTextMixed(t *testing.T) {
	ev := &assistantEvent{}
	ev.Message.Content = []contentBlock{
		{Type: "text", Text: "Part one. "},
		{Type: "tool_use", Name: "Bash"},
		{Type: "text", Text: "Part two."},
	}
	text := extractReportText(ev)
	if text != "Part one. Part two." {
		t.Errorf("expected 'Part one. Part two.', got %q", text)
	}
}

func TestExtractReportTextNil(t *testing.T) {
	text := extractReportText(nil)
	if text != "" {
		t.Errorf("expected empty string for nil, got %q", text)
	}
}

func TestParseToolResult(t *testing.T) {
	line := []byte(`{"type":"tool_result","content":"file contents here"}`)
	typ, ev, err := parseStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if typ != "tool_result" {
		t.Fatalf("expected type 'tool_result', got %q", typ)
	}
	tr, ok := ev.(*toolResultEvent)
	if !ok {
		t.Fatalf("expected *toolResultEvent, got %T", ev)
	}
	if tr.Content != "file contents here" {
		t.Errorf("expected 'file contents here', got %q", tr.Content)
	}
}

func TestParseAssistantToolInput(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"ls -la"}}]}}`)
	_, ev, err := parseStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ast := ev.(*assistantEvent)
	if len(ast.Message.Content) != 1 {
		t.Fatalf("expected 1 block, got %d", len(ast.Message.Content))
	}
	if string(ast.Message.Content[0].Input) != `{"command":"ls -la"}` {
		t.Errorf("unexpected input: %s", ast.Message.Content[0].Input)
	}
}

func TestLooksLikeSecret(t *testing.T) {
	secrets := []string{
		"API_KEY", "aws_secret_access_key", "GITHUB_TOKEN",
		"DB_PASSWORD", "PRIVATE_KEY", "AUTH_HEADER",
		"MY_CREDENTIALS", "passwd",
	}
	for _, name := range secrets {
		if !looksLikeSecret(name) {
			t.Errorf("expected %q to be detected as secret", name)
		}
	}

	safe := []string{
		"HOME", "PATH", "GOPATH", "SHELL", "USER", "LANG",
		"TERM", "EDITOR", "PWD",
	}
	for _, name := range safe {
		if looksLikeSecret(name) {
			t.Errorf("expected %q to NOT be detected as secret", name)
		}
	}
}

func TestParseResultWithIsError(t *testing.T) {
	line := []byte(`{"type":"result","total_cost_usd":0.05,"is_error":true,"num_turns":1,"usage":{"input_tokens":10,"output_tokens":20,"cache_read_input_tokens":0}}`)
	_, ev, err := parseStreamLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	res := ev.(*resultEvent)
	if !res.IsError {
		t.Error("expected is_error true")
	}
}
