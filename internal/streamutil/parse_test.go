package streamutil

import "testing"

func TestParseClaudeLineEmpty(t *testing.T) {
	typ, ev, err := ParseClaudeLine([]byte(""))
	if err != nil {
		t.Fatalf("expected no error for empty line, got: %v", err)
	}
	if typ != "" || ev != nil {
		t.Errorf("expected (\"\", nil, nil) for empty line, got (%q, %v)", typ, ev)
	}
}

func TestParseClaudeLineWhitespaceOnly(t *testing.T) {
	_, _, err := ParseClaudeLine([]byte("   "))
	if err == nil {
		t.Error("expected error for whitespace-only input, got nil")
	}
}

func TestParseClaudeLineTruncatedJSON(t *testing.T) {
	_, _, err := ParseClaudeLine([]byte(`{"type": "result", "total_cost_usd": `))
	if err == nil {
		t.Error("expected error for truncated JSON, got nil")
	}
}

func TestParseClaudeLineUnknownType(t *testing.T) {
	typ, ev, err := ParseClaudeLine([]byte(`{"type":"unknown_event_xyz"}`))
	if err != nil {
		t.Fatalf("expected no error for unknown type, got: %v", err)
	}
	if typ != "" || ev != nil {
		t.Errorf("expected (\"\", nil) for unknown type, got (%q, %v)", typ, ev)
	}
}

func TestParseClaudeLineNullContentBlocks(t *testing.T) {
	line := `{"type":"assistant","message":{"content":null}}`
	typ, ev, err := ParseClaudeLine([]byte(line))
	if err != nil {
		t.Fatalf("ParseClaudeLine with null content blocks: %v", err)
	}
	if typ != "assistant" {
		t.Errorf("expected type 'assistant', got %q", typ)
	}
	assistant, ok := ev.(*AssistantEvent)
	if !ok {
		t.Fatalf("expected *AssistantEvent, got %T", ev)
	}
	if len(assistant.Message.Content) != 0 {
		t.Errorf("expected empty content blocks for null, got %d blocks", len(assistant.Message.Content))
	}
}
