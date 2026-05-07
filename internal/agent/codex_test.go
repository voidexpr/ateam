package agent

import (
	"slices"
	"testing"
)

func TestCodexAgentDebugCommandArgs(t *testing.T) {
	a := &CodexAgent{
		Command: "codex",
		Args:    []string{"--ask-for-approval", "never"},
	}
	tests := []struct {
		name   string
		model  string
		effort string
		want   []string
	}{
		{
			name: "no overrides",
			want: []string{"--ask-for-approval", "never", "exec", "--json"},
		},
		{
			name:  "model only",
			model: "gpt-5",
			want:  []string{"--ask-for-approval", "never", "--model", "gpt-5", "exec", "--json"},
		},
		{
			name:   "effort only — must precede 'exec' subcommand",
			effort: "high",
			want:   []string{"--ask-for-approval", "never", "-c", "model_reasoning_effort=high", "exec", "--json"},
		},
		{
			name:   "model and effort both before exec",
			model:  "gpt-5",
			effort: "medium",
			want:   []string{"--ask-for-approval", "never", "--model", "gpt-5", "-c", "model_reasoning_effort=medium", "exec", "--json"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a.Model = tt.model
			a.Effort = tt.effort
			_, args := a.DebugCommandArgs(nil)
			if !slices.Equal(args, tt.want) {
				t.Errorf("args = %v, want %v", args, tt.want)
			}
		})
	}
}

func TestParseCodexLineTurnStarted(t *testing.T) {
	line := []byte(`{"type":"turn.started"}`)
	typ, ev, err := ParseCodexLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if typ != "system" {
		t.Errorf("expected type 'system', got %q", typ)
	}
	sys, ok := ev.(*CodexSystemEvent)
	if !ok {
		t.Fatalf("expected *CodexSystemEvent, got %T", ev)
	}
	if sys.SessionID != "" {
		t.Errorf("turn.started should not carry SessionID, got %q", sys.SessionID)
	}
}

func TestParseCodexLineThreadStartedSessionID(t *testing.T) {
	line := []byte(`{"type":"thread.started","thread_id":"019df527-3195-79d1-a838-9adc1bebae81"}`)
	typ, ev, err := ParseCodexLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if typ != "system" {
		t.Fatalf("type = %q, want system", typ)
	}
	sys, ok := ev.(*CodexSystemEvent)
	if !ok {
		t.Fatalf("expected *CodexSystemEvent, got %T", ev)
	}
	if sys.SessionID != "019df527-3195-79d1-a838-9adc1bebae81" {
		t.Errorf("SessionID = %q", sys.SessionID)
	}
}

func TestParseCodexLineCachedInputTokens(t *testing.T) {
	line := []byte(`{"type":"turn.completed","usage":{"input_tokens":2420850,"cached_input_tokens":2296704,"output_tokens":16207}}`)
	typ, ev, err := ParseCodexLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if typ != "result" {
		t.Fatalf("type = %q, want result", typ)
	}
	re := ev.(*CodexResultEvent)
	if re.CacheReadTokens != 2296704 {
		t.Errorf("CacheReadTokens = %d, want 2296704", re.CacheReadTokens)
	}
	if re.InputTokens != 2420850 {
		t.Errorf("InputTokens = %d", re.InputTokens)
	}
	if re.OutputTokens != 16207 {
		t.Errorf("OutputTokens = %d", re.OutputTokens)
	}
}

func TestParseCodexLineCachedInputTokensCamelCase(t *testing.T) {
	line := []byte(`{"type":"turn.completed","usage":{"inputTokens":100,"cachedInputTokens":80,"outputTokens":20}}`)
	_, ev, err := ParseCodexLine(line)
	if err != nil {
		t.Fatal(err)
	}
	re := ev.(*CodexResultEvent)
	if re.CacheReadTokens != 80 {
		t.Errorf("CacheReadTokens = %d, want 80", re.CacheReadTokens)
	}
}

func TestParseCodexLineAgentReasoningDelta(t *testing.T) {
	line := []byte(`{"type":"agent_reasoning_delta","delta":"thinking through the diff…"}`)
	typ, ev, err := ParseCodexLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if typ != "thinking" {
		t.Fatalf("type = %q, want thinking", typ)
	}
	te := ev.(*CodexTextEvent)
	if te.Text != "thinking through the diff…" {
		t.Errorf("Text = %q", te.Text)
	}
}

func TestCodexResultCostUsesCachedRate(t *testing.T) {
	// End-to-end: a turn.completed JSONL line with cached_input_tokens
	// should flow through ParseCodexLine → CodexResultEvent and price
	// the cached subset at CachedInputPerToken — not full input rate.
	// Without cache-aware pricing, the cost would be inflated by ~2.6×
	// for a 76% cache hit (matches the ratio we measured on run 582).
	line := []byte(`{"type":"turn.completed","usage":{"input_tokens":94963,"cached_input_tokens":72704,"output_tokens":1204}}`)
	_, ev, err := ParseCodexLine(line)
	if err != nil {
		t.Fatal(err)
	}
	re := ev.(*CodexResultEvent)

	table := PricingTable{
		"gpt-5.3-codex": {
			InputPerToken:       1.75 / 1e6,
			CachedInputPerToken: 0.175 / 1e6,
			OutputPerToken:      14.00 / 1e6,
		},
	}
	cost := EstimateCost(table, "gpt-5.3-codex", "", re.InputTokens, re.CacheReadTokens, re.OutputTokens)

	uncached := re.InputTokens - re.CacheReadTokens
	want := float64(uncached)*1.75/1e6 + float64(re.CacheReadTokens)*0.175/1e6 + float64(re.OutputTokens)*14.00/1e6
	if cost < want-1e-9 || cost > want+1e-9 {
		t.Errorf("cost = %v, want %v", cost, want)
	}

	// Sanity check: the inflated (pre-cache-aware) cost is what we used to
	// report. Make sure we're meaningfully below it so a regression that
	// drops the cached-rate path would fail this test.
	inflated := float64(re.InputTokens)*1.75/1e6 + float64(re.OutputTokens)*14.00/1e6
	if cost >= inflated*0.95 {
		t.Errorf("cost %v not meaningfully below inflated %v — cache discount lost?", cost, inflated)
	}
}

func TestParseCodexLineAgentReasoningAggregateDropped(t *testing.T) {
	// The aggregate `agent_reasoning` event duplicates the streamed deltas;
	// it should be dropped (typ="", ev=nil) so we don't double-emit.
	line := []byte(`{"type":"agent_reasoning","text":"final summary"}`)
	typ, ev, err := ParseCodexLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if typ != "" || ev != nil {
		t.Errorf("agent_reasoning should be dropped, got typ=%q ev=%v", typ, ev)
	}
}

func TestParseCodexLineTurnFailedCarriesError(t *testing.T) {
	line := []byte(`{"type":"turn.failed","error":{"message":"OpenAI stream timed out","type":"stream_timeout"}}`)
	typ, ev, err := ParseCodexLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if typ != "result" {
		t.Fatalf("type = %q, want result", typ)
	}
	re, ok := ev.(*CodexResultEvent)
	if !ok {
		t.Fatalf("expected *CodexResultEvent, got %T", ev)
	}
	if !re.IsError {
		t.Error("IsError = false, want true")
	}
	if re.ErrorMessage != "OpenAI stream timed out" {
		t.Errorf("ErrorMessage = %q", re.ErrorMessage)
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
