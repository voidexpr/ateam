package streamutil

import (
	"encoding/json"
	"testing"
)

// TestFlexibleContentStringForm covers the historical case: tool_result
// with `"content": "tool output"`. The pre-fix parser handled this; the
// regression check is that we still do.
func TestFlexibleContentStringForm(t *testing.T) {
	var fc FlexibleContent
	if err := json.Unmarshal([]byte(`"hello tool output"`), &fc); err != nil {
		t.Fatalf("unmarshal string: %v", err)
	}
	if fc.Text != "hello tool output" {
		t.Errorf("Text = %q, want %q", fc.Text, "hello tool output")
	}
	if len(fc.Blocks) != 0 {
		t.Errorf("Blocks should be empty, got %v", fc.Blocks)
	}
	if got := fc.String(); got != "hello tool output" {
		t.Errorf("String() = %q, want %q", got, "hello tool output")
	}
}

// TestFlexibleContentArrayForm covers the new case: tool_result with
// `"content": [{"type":"text","text":"…"}, …]`. Pre-fix this returned
// an UnmarshalTypeError and the entire JSONL line was dropped at
// agent/claude.go:162.
func TestFlexibleContentArrayForm(t *testing.T) {
	in := []byte(`[{"type":"text","text":"first"},{"type":"text","text":"second"},{"type":"image","text":""}]`)
	var fc FlexibleContent
	if err := json.Unmarshal(in, &fc); err != nil {
		t.Fatalf("unmarshal array: %v", err)
	}
	if fc.Text != "" {
		t.Errorf("Text should be empty for array form, got %q", fc.Text)
	}
	if len(fc.Blocks) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(fc.Blocks))
	}
	if got, want := fc.String(), "first\nsecond"; got != want {
		t.Errorf("String() = %q, want %q (text blocks joined, image dropped)", got, want)
	}
}

// TestFlexibleContentNullAndEmpty verifies that null and missing
// content survive — we don't want a missing field to fail the whole
// JSONL line.
func TestFlexibleContentNullAndEmpty(t *testing.T) {
	var fc FlexibleContent
	if err := json.Unmarshal([]byte(`null`), &fc); err != nil {
		t.Fatalf("unmarshal null: %v", err)
	}
	if fc.Text != "" || len(fc.Blocks) != 0 {
		t.Errorf("expected zero value for null, got %+v", fc)
	}
	if fc.String() != "" {
		t.Errorf("String() should be empty, got %q", fc.String())
	}
}

// TestFlexibleContentRejectsOtherKinds documents the safety check:
// numbers/objects/booleans aren't valid for content and should error.
// (Better to surface the schema mismatch than silently swallow it.)
func TestFlexibleContentRejectsOtherKinds(t *testing.T) {
	for _, in := range []string{`123`, `{"foo":"bar"}`, `true`} {
		var fc FlexibleContent
		if err := json.Unmarshal([]byte(in), &fc); err == nil {
			t.Errorf("expected error for %q, got nil (parsed as %+v)", in, fc)
		}
	}
}

// TestParseClaudeLineUserToolResultArrayForm is the end-to-end
// regression test for the bug report: the exact JSONL line from the
// repro section must parse without error and produce a UserEvent
// whose tool_result block exposes the array-form text via
// FlexibleContent.String().
func TestParseClaudeLineUserToolResultArrayForm(t *testing.T) {
	line := []byte(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_test","content":[{"type":"text","text":"hello"}]}]}}`)
	typ, ev, err := ParseClaudeLine(line)
	if err != nil {
		t.Fatalf("ParseClaudeLine returned error: %v (was the bug)", err)
	}
	if typ != "user" {
		t.Fatalf("type = %q, want %q", typ, "user")
	}
	u, ok := ev.(*UserEvent)
	if !ok {
		t.Fatalf("expected *UserEvent, got %T", ev)
	}
	if len(u.Message.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(u.Message.Content))
	}
	block := u.Message.Content[0]
	if block.Type != "tool_result" {
		t.Errorf("block type = %q, want tool_result", block.Type)
	}
	if block.ToolUseID != "toolu_test" {
		t.Errorf("tool_use_id = %q, want toolu_test", block.ToolUseID)
	}
	if got := block.Content.String(); got != "hello" {
		t.Errorf("content text = %q, want %q", got, "hello")
	}
}

// TestParseClaudeLineUserToolResultStringForm: same structure but with
// the string-form content. Sanity check that we didn't break the path
// that already worked.
func TestParseClaudeLineUserToolResultStringForm(t *testing.T) {
	line := []byte(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_test","content":"hello"}]}}`)
	typ, ev, err := ParseClaudeLine(line)
	if err != nil {
		t.Fatalf("ParseClaudeLine returned error: %v", err)
	}
	if typ != "user" {
		t.Fatalf("type = %q, want %q", typ, "user")
	}
	u := ev.(*UserEvent)
	if got := u.Message.Content[0].Content.String(); got != "hello" {
		t.Errorf("content text = %q, want %q", got, "hello")
	}
}
