package runner

import (
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// =============================================================================
// BUG: truncate slices by bytes, not runes — produces invalid UTF-8
// File: runner.go, func truncate
//
// truncate uses len(s) (byte count) and s[:max] (byte slice). For multi-byte
// UTF-8 characters, this cuts in the middle of a character, producing invalid
// UTF-8 output.
// =============================================================================

func TestTruncateMultiByteUTF8ProducesInvalidOutput(t *testing.T) {
	// "hello 世界!" — the Chinese chars are 3 bytes each.
	// Bytes: h(1) e(1) l(1) l(1) o(1) (1) 世(3) 界(3) !(1) = 13 bytes total
	// Truncating at 8 bytes cuts in the middle of 世 (which starts at byte 6).
	input := "hello 世界!"
	result := truncate(input, 8)

	// The result should be valid UTF-8.
	if !utf8.ValidString(result) {
		t.Errorf("truncate(%q, 8) produced invalid UTF-8: %q (bytes: %x)",
			input, result, []byte(result))
	}
}

func TestTruncateMultiByteEmojiSplit(t *testing.T) {
	// Emoji like 🔥 is 4 bytes (F0 9F 94 A5). Cutting at 2 bytes splits it.
	input := "🔥🔥🔥"
	result := truncate(input, 5) // cuts inside second emoji

	if !utf8.ValidString(result) {
		t.Errorf("truncate(%q, 5) produced invalid UTF-8: %q (bytes: %x)",
			input, result, []byte(result))
	}
}

func TestTruncateASCIIStillWorks(t *testing.T) {
	// Sanity check: ASCII strings should still work correctly.
	result := truncate("hello world", 5)
	if result != "hello…" {
		t.Errorf("truncate ASCII: got %q, want %q", result, "hello…")
	}
}

func TestTruncateNoTruncationNeeded(t *testing.T) {
	result := truncate("short", 100)
	if result != "short" {
		t.Errorf("truncate no-op: got %q, want %q", result, "short")
	}
}

// =============================================================================
// BUG: FormatDuration returns "60s" for durations just under 1 minute
// File: runner.go, func FormatDuration
//
// At 59.5 seconds, d.Seconds() = 59.5, %.0f rounds to 60, output is "60s".
// But 60 seconds should display as "1m" or "1m0s" for consistency.
// =============================================================================

func TestFormatDurationBoundaryAt59Point5Seconds(t *testing.T) {
	// 59.5 seconds is < time.Minute, so the first branch fires.
	// fmt.Sprintf("%.0fs", 59.5) rounds to "60s".
	// But "60s" is inconsistent — 60 seconds = 1 minute.
	d := 59*time.Second + 500*time.Millisecond

	result := FormatDuration(d)
	if result == "60s" {
		t.Errorf("FormatDuration(%v) = %q — 60 seconds should display as a minute, not '60s'", d, result)
	}
}

func TestFormatDurationBoundaryAt59Point9Seconds(t *testing.T) {
	d := 59*time.Second + 999*time.Millisecond
	result := FormatDuration(d)
	if result == "60s" {
		t.Errorf("FormatDuration(%v) = %q — should not show '60s'", d, result)
	}
}

func TestFormatDurationExactlyOneMinute(t *testing.T) {
	result := FormatDuration(time.Minute)
	if result != "1m" {
		t.Errorf("FormatDuration(1m) = %q, want %q", result, "1m")
	}
}

func TestFormatDurationZero(t *testing.T) {
	result := FormatDuration(0)
	if result != "0s" {
		t.Errorf("FormatDuration(0) = %q, want %q", result, "0s")
	}
}

// =============================================================================
// BUG: streamTailMessages panics when n=0
// File: format.go, func streamTailMessages
//
// When n=0: make([]string, 0, 0) succeeds, but on first TextLine,
// len(messages) >= 0 is true, then messages[1:] panics on empty slice.
// StreamTailError is an exported function — callers can pass any int.
// =============================================================================

func TestStreamTailErrorZeroMaxMessages(t *testing.T) {
	content := `{"type":"assistant","message":{"content":[{"type":"text","text":"some message"}]}}
`
	path := writeTempStreamBugs(t, content)

	// This should NOT panic. With n=0, it should return "" gracefully.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("StreamTailError panicked with maxMessages=0: %v", r)
		}
	}()

	result := StreamTailError(path, "agent", 0)
	_ = result // we just care it doesn't panic
}

func TestStreamTailErrorNegativeMaxMessages(t *testing.T) {
	content := `{"type":"assistant","message":{"content":[{"type":"text","text":"some message"}]}}
`
	path := writeTempStreamBugs(t, content)

	// Negative n causes make([]string, 0, n) to panic.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("StreamTailError panicked with maxMessages=-1: %v", r)
		}
	}()

	result := StreamTailError(path, "agent", -1)
	_ = result
}

// =============================================================================
// StreamFormatter turn counting: only UserLine increments TurnNum
// File: format_stream.go, func fmtUser
//
// A conversation of user→assistant→user→assistant should show Turn 1 and Turn 2
// only. TextLine never increments TurnNum, so there is no double-counting.
// =============================================================================

func TestStreamFormatterTurnCountingAccuracy(t *testing.T) {
	// Build a JSONL with: system, user, assistant-text, user, assistant-text
	// Expected: Turn 1 (user), Turn 2 (user) — NOT Turn 1, Turn 2, Turn 3, Turn 4
	content := `{"type":"system","subtype":"init","session_id":"s1","model":"test"}
{"type":"user"}
{"type":"assistant","message":{"content":[{"type":"text","text":"First response."}]}}
{"type":"user"}
{"type":"assistant","message":{"content":[{"type":"text","text":"Second response."}]}}
{"type":"result","total_cost_usd":0.01,"duration_ms":1000,"num_turns":2,"is_error":false,"usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":0}}
`
	path := writeTempStreamBugs(t, content)

	var buf strings.Builder
	sf := &StreamFormatter{Color: false}
	if err := sf.FormatFile(path, &buf); err != nil {
		t.Fatalf("StreamFormatter.FormatFile error: %v", err)
	}
	out := buf.String()

	// With 2 user messages, the highest turn number should be 2, not 4.
	if strings.Contains(out, "Turn 3") || strings.Contains(out, "Turn 4") {
		t.Errorf("StreamFormatter inflates turn numbers — shows Turn 3 or Turn 4 for a 2-turn conversation.\nGot:\n%s", out)
	}
}

// =============================================================================
// BUG: CacheWriteTokens absent from ResultLine — never parsed or displayed
// File: parse_stream.go (ResultLine struct), format_stream.go (display)
//
// The DB schema has cache_write_tokens (added in commit a695951), but:
// - streamutil.ResultEvent.Usage has no CacheWriteTokens field
// - runner.ResultLine has no CacheWriteTokens field
// - runner.StreamEvent has no CacheWriteTokens field
// - runner.RunSummary.CacheWriteTokens is always 0
//
// So cache_write_tokens in the DB is always 0 — the data is silently dropped.
// =============================================================================

func TestResultLineShouldHaveCacheWriteTokens(t *testing.T) {
	// Parse a result event that includes cache_write_input_tokens.
	content := `{"type":"result","total_cost_usd":0.05,"duration_ms":5000,"num_turns":3,"is_error":false,"usage":{"input_tokens":1000,"output_tokens":200,"cache_read_input_tokens":500,"cache_write_input_tokens":300}}
`
	path := writeTempStreamBugs(t, content)

	res := scanStreamFileForResult(path)
	if res == nil {
		t.Fatal("expected non-nil result")
	}

	// The ResultLine struct has CacheReadTokens but no CacheWriteTokens.
	// This test documents the missing field: cache_write_input_tokens=300
	// is present in the JSON but silently dropped during parsing.
	if res.CacheReadTokens != 500 {
		t.Errorf("CacheReadTokens = %d, want 500", res.CacheReadTokens)
	}
	if res.CacheWriteTokens != 300 {
		t.Errorf("CacheWriteTokens = %d, want 300", res.CacheWriteTokens)
	}
}

// helper
func writeTempStreamBugs(t *testing.T, content string) string {
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
