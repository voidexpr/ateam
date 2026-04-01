package agent

import (
	"testing"
)

func TestParseCodexLineTurnStarted(t *testing.T) {
	line := []byte(`{"type":"turn.started"}`)
	typ, ev, err := ParseCodexLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if typ != "system" {
		t.Errorf("expected type 'system', got %q", typ)
	}
	if ev == nil {
		t.Error("expected non-nil event")
	}
}

func TestParseCodexLineExecCommand(t *testing.T) {
	line := []byte(`{"type":"exec_command_begin","command":"ls -la"}`)
	typ, ev, err := ParseCodexLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if typ != "tool_use" {
		t.Errorf("expected type 'tool_use', got %q", typ)
	}
	te := ev.(*CodexToolUseEvent)
	if te.ToolName != "exec_command" {
		t.Errorf("expected tool name 'exec_command', got %q", te.ToolName)
	}
	if te.ToolInput != "ls -la" {
		t.Errorf("expected tool input 'ls -la', got %q", te.ToolInput)
	}
}

func TestParseCodexLineExecCommandArray(t *testing.T) {
	line := []byte(`{"type":"exec_command_begin","command":["git","status"]}`)
	typ, ev, err := ParseCodexLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if typ != "tool_use" {
		t.Errorf("expected type 'tool_use', got %q", typ)
	}
	te := ev.(*CodexToolUseEvent)
	if te.ToolInput != "git status" {
		t.Errorf("expected 'git status', got %q", te.ToolInput)
	}
}

func TestParseCodexLineWebSearch(t *testing.T) {
	line := []byte(`{"type":"web_search_begin","query":"golang testing"}`)
	typ, ev, err := ParseCodexLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if typ != "tool_use" {
		t.Errorf("expected type 'tool_use', got %q", typ)
	}
	te := ev.(*CodexToolUseEvent)
	if te.ToolName != "web_search" {
		t.Errorf("expected tool name 'web_search', got %q", te.ToolName)
	}
	if te.ToolInput != "golang testing" {
		t.Errorf("expected query 'golang testing', got %q", te.ToolInput)
	}
}

func TestParseCodexLineMessageDelta(t *testing.T) {
	line := []byte(`{"type":"agent_message_delta","delta":"hello world"}`)
	typ, ev, err := ParseCodexLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if typ != "assistant" {
		t.Errorf("expected type 'assistant', got %q", typ)
	}
	te := ev.(*CodexTextEvent)
	if te.Text != "hello world" {
		t.Errorf("expected 'hello world', got %q", te.Text)
	}
}

func TestParseCodexLineItemCompleted(t *testing.T) {
	line := []byte(`{"type":"item.completed","item":{"type":"agent_message","text":"final answer"}}`)
	typ, ev, err := ParseCodexLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if typ != "item_completed" {
		t.Errorf("expected type 'item_completed', got %q", typ)
	}
	te := ev.(*CodexTextEvent)
	if te.Text != "final answer" {
		t.Errorf("expected 'final answer', got %q", te.Text)
	}
}

func TestParseCodexLineItemCompletedContent(t *testing.T) {
	line := []byte(`{"type":"item.completed","item":{"type":"agent_message","content":[{"text":"part1"},{"text":"part2"}]}}`)
	typ, ev, err := ParseCodexLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if typ != "item_completed" {
		t.Errorf("expected type 'item_completed', got %q", typ)
	}
	te := ev.(*CodexTextEvent)
	if te.Text != "part1\npart2" {
		t.Errorf("expected 'part1\\npart2', got %q", te.Text)
	}
}

func TestParseCodexLineItemCompletedNonMessage(t *testing.T) {
	line := []byte(`{"type":"item.completed","item":{"type":"tool_result"}}`)
	typ, _, err := ParseCodexLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if typ != "" {
		t.Errorf("expected empty type for non-message item, got %q", typ)
	}
}

func TestParseCodexLineTurnCompleted(t *testing.T) {
	line := []byte(`{"type":"turn.completed","duration_ms":5000,"usage":{"input_tokens":100,"output_tokens":50}}`)
	typ, ev, err := ParseCodexLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if typ != "result" {
		t.Errorf("expected type 'result', got %q", typ)
	}
	re := ev.(*CodexResultEvent)
	if re.DurationMS != 5000 {
		t.Errorf("expected duration 5000, got %d", re.DurationMS)
	}
	if re.InputTokens != 100 {
		t.Errorf("expected 100 input tokens, got %d", re.InputTokens)
	}
	if re.OutputTokens != 50 {
		t.Errorf("expected 50 output tokens, got %d", re.OutputTokens)
	}
	if re.IsError {
		t.Error("expected IsError=false")
	}
}

func TestParseCodexLineTurnFailed(t *testing.T) {
	line := []byte(`{"type":"turn.failed","duration_ms":1000,"usage":{"input_tokens":10,"output_tokens":5}}`)
	typ, ev, err := ParseCodexLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if typ != "result" {
		t.Errorf("expected type 'result', got %q", typ)
	}
	re := ev.(*CodexResultEvent)
	if !re.IsError {
		t.Error("expected IsError=true for turn.failed")
	}
}

func TestParseCodexLineError(t *testing.T) {
	line := []byte(`{"type":"error","message":"rate limit exceeded"}`)
	typ, ev, err := ParseCodexLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if typ != "error" {
		t.Errorf("expected type 'error', got %q", typ)
	}
	ee := ev.(*CodexErrorEvent)
	if ee.Message != "rate limit exceeded" {
		t.Errorf("expected 'rate limit exceeded', got %q", ee.Message)
	}
}

func TestParseCodexLineUnknownType(t *testing.T) {
	line := []byte(`{"type":"some_internal_event","data":"foo"}`)
	typ, ev, err := ParseCodexLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if typ != "" || ev != nil {
		t.Errorf("expected empty result for unknown type, got type=%q ev=%v", typ, ev)
	}
}

func TestParseCodexLineEmpty(t *testing.T) {
	typ, ev, err := ParseCodexLine([]byte{})
	if err != nil {
		t.Fatal(err)
	}
	if typ != "" || ev != nil {
		t.Error("expected empty result for empty line")
	}
}

func TestParseCodexLineCamelCaseTokens(t *testing.T) {
	line := []byte(`{"type":"turn.completed","durationMs":3000,"usage":{"inputTokens":200,"outputTokens":80}}`)
	typ, ev, err := ParseCodexLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if typ != "result" {
		t.Errorf("expected type 'result', got %q", typ)
	}
	re := ev.(*CodexResultEvent)
	if re.DurationMS != 3000 {
		t.Errorf("expected duration 3000, got %d", re.DurationMS)
	}
	if re.InputTokens != 200 {
		t.Errorf("expected 200 input tokens, got %d", re.InputTokens)
	}
	if re.OutputTokens != 80 {
		t.Errorf("expected 80 output tokens, got %d", re.OutputTokens)
	}
}

func TestParseCodexLineItemStartedCommandExecution(t *testing.T) {
	line := []byte(`{"type":"item.started","item":{"id":"item_2","type":"command_execution","command":"/opt/homebrew/bin/bash -lc 'git status --short'","aggregated_output":"","exit_code":null,"status":"in_progress"}}`)
	typ, ev, err := ParseCodexLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if typ != "tool_use" {
		t.Errorf("expected type 'tool_use', got %q", typ)
	}
	te := ev.(*CodexToolUseEvent)
	if te.ToolName != "command_execution" {
		t.Errorf("expected tool name 'command_execution', got %q", te.ToolName)
	}
	if te.ToolInput != "/opt/homebrew/bin/bash -lc 'git status --short'" {
		t.Errorf("unexpected tool input: %q", te.ToolInput)
	}
}

func TestParseCodexLineItemStartedNonTool(t *testing.T) {
	line := []byte(`{"type":"item.started","item":{"type":"todo_list"}}`)
	typ, _, err := ParseCodexLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if typ != "" {
		t.Errorf("expected empty type for non-tool item.started, got %q", typ)
	}
}

func TestParseCodexLineCommandExecutionStandalone(t *testing.T) {
	// Standalone command_execution events are output updates, not new tool calls.
	line := []byte(`{"type":"command_execution","exit_code":0,"aggregated_output":"ok"}`)
	typ, _, err := ParseCodexLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if typ != "" {
		t.Errorf("expected empty type for standalone command_execution, got %q", typ)
	}
}

func TestParseCodexLineUnknownBeginSuffix(t *testing.T) {
	// Any unknown _begin event should be treated as a tool call.
	line := []byte(`{"type":"file_search_begin","query":"test pattern"}`)
	typ, ev, err := ParseCodexLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if typ != "tool_use" {
		t.Errorf("expected type 'tool_use', got %q", typ)
	}
	te := ev.(*CodexToolUseEvent)
	if te.ToolName != "file_search" {
		t.Errorf("expected 'file_search', got %q", te.ToolName)
	}
}

func TestParseCodexLinePatchApply(t *testing.T) {
	line := []byte(`{"type":"patch_apply_begin","name":"fix.patch"}`)
	typ, ev, err := ParseCodexLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if typ != "tool_use" {
		t.Errorf("expected type 'tool_use', got %q", typ)
	}
	te := ev.(*CodexToolUseEvent)
	if te.ToolName != "patch_apply" {
		t.Errorf("expected 'patch_apply', got %q", te.ToolName)
	}
	if te.ToolInput != "fix.patch" {
		t.Errorf("expected 'fix.patch', got %q", te.ToolInput)
	}
}
