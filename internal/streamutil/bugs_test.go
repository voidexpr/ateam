package streamutil

import "testing"

// =============================================================================
// REGRESSION: ResultEvent.Usage missing cache_write_input_tokens field
// File: events.go, type ResultEvent
//
// Claude's stream-json output includes cache_write_input_tokens in the usage
// object, but ResultEvent.Usage only has:
//   - InputTokens
//   - OutputTokens
//   - CacheReadInputTokens
//
// The missing CacheWriteInputTokens field means the value is silently dropped
// during JSON unmarshaling. This is the root cause of cache_write_tokens
// always being 0 in the DB.
// =============================================================================

func TestResultEventParsesCacheWriteTokens(t *testing.T) {
	line := `{
		"type": "result",
		"total_cost_usd": 0.05,
		"cost_usd": 0.03,
		"duration_ms": 5000,
		"num_turns": 3,
		"is_error": false,
		"usage": {
			"input_tokens": 1000,
			"output_tokens": 200,
			"cache_read_input_tokens": 500,
			"cache_write_input_tokens": 300
		}
	}`

	typ, ev, err := ParseClaudeLine([]byte(line))
	if err != nil {
		t.Fatalf("ParseClaudeLine error: %v", err)
	}
	if typ != "result" {
		t.Fatalf("type = %q, want %q", typ, "result")
	}

	result := ev.(*ResultEvent)

	// These fields work fine.
	if result.Usage.InputTokens != 1000 {
		t.Errorf("InputTokens = %d, want 1000", result.Usage.InputTokens)
	}
	if result.Usage.OutputTokens != 200 {
		t.Errorf("OutputTokens = %d, want 200", result.Usage.OutputTokens)
	}
	if result.Usage.CacheReadInputTokens != 500 {
		t.Errorf("CacheReadInputTokens = %d, want 500", result.Usage.CacheReadInputTokens)
	}

	if result.Usage.CacheWriteInputTokens != 300 {
		t.Errorf("CacheWriteInputTokens = %d, want 300", result.Usage.CacheWriteInputTokens)
	}
}

func TestTrimBOMEmpty(t *testing.T) {
	result := TrimBOM(nil)
	if result != nil {
		t.Errorf("TrimBOM(nil) = %v, want nil", result)
	}
}

func TestTrimBOMShortInput(t *testing.T) {
	// Input shorter than 3 bytes should be returned as-is.
	result := TrimBOM([]byte{0xEF, 0xBB})
	if len(result) != 2 {
		t.Errorf("TrimBOM(2 bytes) returned %d bytes, want 2", len(result))
	}
}

func TestTrimBOMWithBOM(t *testing.T) {
	input := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"type":"system"}`)...)
	result := TrimBOM(input)
	if string(result) != `{"type":"system"}` {
		t.Errorf("TrimBOM with BOM: got %q", string(result))
	}
}

func TestTrimBOMWithoutBOM(t *testing.T) {
	input := []byte(`{"type":"system"}`)
	result := TrimBOM(input)
	if string(result) != `{"type":"system"}` {
		t.Errorf("TrimBOM without BOM: got %q", string(result))
	}
}
