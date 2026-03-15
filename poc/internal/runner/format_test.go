package runner

import (
	"os"
	"strings"
	"testing"
)

// writeTempStream writes content to a temporary JSONL file and returns its path.
func writeTempStream(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stream*.jsonl")
	if err != nil {
		t.Fatalf("cannot create temp file: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("cannot write temp file: %v", err)
	}
	f.Close()
	return f.Name()
}

// TestFormatStreamOutput verifies that FormatStream produces the expected
// human-readable sections from the sample fixture.
func TestFormatStreamOutput(t *testing.T) {
	var buf strings.Builder
	if err := FormatStream("testdata/sample_stream.jsonl", &buf); err != nil {
		t.Fatalf("FormatStream returned error: %v", err)
	}

	out := buf.String()
	checks := []struct {
		desc    string
		contain string
	}{
		{"system header", "── system (init) ──"},
		{"turn 1 header", "── turn 1 ──"},
		{"assistant text first", "I will analyze the code."},
		{"tool use header", "▶ Bash"},
		{"tool result content", "◀ tool output here"},
		{"turn 2 header", "── turn 2 ──"},
		{"assistant text second", "Analysis complete."},
		{"result header", "── result ──"},
		{"cost line", "$0.0150"},
		{"turns line", "Turns:"},
		{"input tokens line", "Input:"},
		{"output tokens line", "Output:"},
	}
	for _, c := range checks {
		if !strings.Contains(out, c.contain) {
			t.Errorf("%s: output does not contain %q\n\nGot:\n%s", c.desc, c.contain, out)
		}
	}
}

// TestFormatStreamMissingFile verifies that FormatStream returns an error
// when the path does not exist.
func TestFormatStreamMissingFile(t *testing.T) {
	var buf strings.Builder
	err := FormatStream("/nonexistent/path/stream.jsonl", &buf)
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

// TestFormatStreamSkipsUnknownTypes verifies that unrecognised event types
// are silently ignored and do not cause an error.
func TestFormatStreamSkipsUnknownTypes(t *testing.T) {
	content := `{"type":"unknown_future_event","data":"value"}
{"type":"assistant","message":{"content":[{"type":"text","text":"visible"}]}}
`
	path := writeTempStream(t, content)

	var buf strings.Builder
	if err := FormatStream(path, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "visible") {
		t.Errorf("expected 'visible' in output, got: %s", buf.String())
	}
}

// TestScanStreamFileForResultFound verifies that result event fields are
// correctly extracted from the sample fixture.
func TestScanStreamFileForResultFound(t *testing.T) {
	res := scanStreamFileForResult("testdata/sample_stream.jsonl")
	if res == nil {
		t.Fatal("expected non-nil result event")
	}
	if res.TotalCostUSD != 0.0150 {
		t.Errorf("expected TotalCostUSD 0.0150, got %f", res.TotalCostUSD)
	}
	if res.DurationMS != 8500 {
		t.Errorf("expected DurationMS 8500, got %d", res.DurationMS)
	}
	if res.NumTurns != 2 {
		t.Errorf("expected NumTurns 2, got %d", res.NumTurns)
	}
	if res.Usage.InputTokens != 500 {
		t.Errorf("expected InputTokens 500, got %d", res.Usage.InputTokens)
	}
	if res.Usage.OutputTokens != 150 {
		t.Errorf("expected OutputTokens 150, got %d", res.Usage.OutputTokens)
	}
	if res.Usage.CacheReadInputTokens != 100 {
		t.Errorf("expected CacheReadInputTokens 100, got %d", res.Usage.CacheReadInputTokens)
	}
	if res.IsError {
		t.Error("expected IsError false")
	}
}

// TestScanStreamFileForResultNone verifies that nil is returned when no
// result event exists in the stream.
func TestScanStreamFileForResultNone(t *testing.T) {
	content := `{"type":"system","subtype":"init","session_id":"s1"}
{"type":"assistant","message":{"content":[{"type":"text","text":"no result coming"}]}}
`
	path := writeTempStream(t, content)
	res := scanStreamFileForResult(path)
	if res != nil {
		t.Errorf("expected nil for stream with no result event, got %+v", res)
	}
}

// TestScanStreamFileForResultMissingFile verifies that nil is returned when
// the file does not exist.
func TestScanStreamFileForResultMissingFile(t *testing.T) {
	res := scanStreamFileForResult("/nonexistent/path/stream.jsonl")
	if res != nil {
		t.Errorf("expected nil for missing file, got %+v", res)
	}
}

// TestScanStreamFileForResultLastWins verifies that when multiple result
// events appear, the last one is returned.
func TestScanStreamFileForResultLastWins(t *testing.T) {
	content := `{"type":"result","total_cost_usd":0.01,"duration_ms":1000,"num_turns":1,"is_error":false,"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":0}}
{"type":"result","total_cost_usd":0.05,"duration_ms":5000,"num_turns":3,"is_error":false,"usage":{"input_tokens":200,"output_tokens":80,"cache_read_input_tokens":0}}
`
	path := writeTempStream(t, content)
	res := scanStreamFileForResult(path)
	if res == nil {
		t.Fatal("expected non-nil result event")
	}
	if res.TotalCostUSD != 0.05 {
		t.Errorf("expected last result TotalCostUSD 0.05, got %f", res.TotalCostUSD)
	}
	if res.NumTurns != 3 {
		t.Errorf("expected last result NumTurns 3, got %d", res.NumTurns)
	}
}

// TestStreamTailErrorKnownPattern verifies that a known credit error in the
// last assistant message is returned as "{agentName}: {pattern}".
func TestStreamTailErrorKnownPattern(t *testing.T) {
	content := `{"type":"assistant","message":{"content":[{"type":"text","text":"Credit balance is too low for this request."}]}}
`
	path := writeTempStream(t, content)
	result := StreamTailError(path, "claude", 3)
	want := "claude: Credit balance is too low"
	if result != want {
		t.Errorf("expected %q, got %q", want, result)
	}
}

// TestStreamTailErrorNormalMessages verifies that when no known error is
// found, the last maxMessages assistant text blocks are returned.
func TestStreamTailErrorNormalMessages(t *testing.T) {
	content := `{"type":"assistant","message":{"content":[{"type":"text","text":"First message."}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"Second message."}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"Third message."}]}}
`
	path := writeTempStream(t, content)
	result := StreamTailError(path, "claude", 2)

	if !strings.Contains(result, "Second message.") {
		t.Errorf("expected second message in result, got %q", result)
	}
	if !strings.Contains(result, "Third message.") {
		t.Errorf("expected third message in result, got %q", result)
	}
	if strings.Contains(result, "First message.") {
		t.Errorf("expected first message to be excluded (maxMessages=2), got %q", result)
	}
}

// TestStreamTailErrorMissingFile verifies that an empty string is returned
// when the stream file does not exist.
func TestStreamTailErrorMissingFile(t *testing.T) {
	result := StreamTailError("/nonexistent/path.jsonl", "claude", 3)
	if result != "" {
		t.Errorf("expected empty string for missing file, got %q", result)
	}
}

// TestStreamTailErrorNoAssistantMessages verifies that an empty string is
// returned when the stream contains no assistant text blocks.
func TestStreamTailErrorNoAssistantMessages(t *testing.T) {
	content := `{"type":"system","subtype":"init","session_id":"s1"}
{"type":"result","total_cost_usd":0.01,"is_error":false,"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":0}}
`
	path := writeTempStream(t, content)
	result := StreamTailError(path, "claude", 3)
	if result != "" {
		t.Errorf("expected empty string when no assistant messages, got %q", result)
	}
}
