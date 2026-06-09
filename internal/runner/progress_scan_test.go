package runner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProgressScannerAccumulates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.jsonl")
	sc := NewProgressScanner(path)

	// Polling before the file exists is a no-op.
	sc.Poll()
	if sc.Turns != 0 || sc.ToolCount != 0 {
		t.Fatalf("expected zero counters before file exists, got turns=%d tools=%d", sc.Turns, sc.ToolCount)
	}

	appendFile := func(s string) {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if _, err := f.WriteString(s); err != nil {
			t.Fatal(err)
		}
	}

	appendFile(`{"type":"system","subtype":"init","session_id":"s1","model":"m1"}` + "\n" +
		`{"type":"assistant","message":{"content":[{"type":"text","text":"plan"},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}],"usage":{"input_tokens":100,"output_tokens":20,"cache_creation_input_tokens":50,"cache_read_input_tokens":1000}}}` + "\n")
	sc.Poll()

	if sc.Turns != 1 {
		t.Errorf("Turns = %d, want 1 (usage counted once per multi-block message)", sc.Turns)
	}
	if sc.ToolCount != 1 || sc.LastTool != "Bash" {
		t.Errorf("ToolCount/LastTool = %d/%q, want 1/Bash", sc.ToolCount, sc.LastTool)
	}
	if sc.CumInputTokens != 100 || sc.CumOutputTokens != 20 {
		t.Errorf("cumulative tokens = %d/%d, want 100/20", sc.CumInputTokens, sc.CumOutputTokens)
	}
	if sc.ContextTokens != 1150 {
		t.Errorf("ContextTokens = %d, want 1150 (input + cache create + cache read)", sc.ContextTokens)
	}

	// A partial line must be carried until its remainder arrives.
	line2 := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t2","name":"Read","input":{"file_path":"x"}}],"usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":0,"cache_read_input_tokens":1200}}}`
	appendFile(line2[:40])
	sc.Poll()
	if sc.ToolCount != 1 {
		t.Fatalf("partial line consumed early: ToolCount = %d, want 1", sc.ToolCount)
	}
	appendFile(line2[40:] + "\n")
	sc.Poll()

	if sc.Turns != 2 || sc.ToolCount != 2 || sc.LastTool != "Read" {
		t.Errorf("after second message: turns=%d tools=%d last=%q, want 2/2/Read", sc.Turns, sc.ToolCount, sc.LastTool)
	}
	if sc.CumInputTokens != 110 || sc.CumOutputTokens != 25 {
		t.Errorf("cumulative tokens = %d/%d, want 110/25", sc.CumInputTokens, sc.CumOutputTokens)
	}
	if sc.ContextTokens != 1210 {
		t.Errorf("ContextTokens = %d, want 1210 (latest message)", sc.ContextTokens)
	}

	appendFile(`{"type":"result","subtype":"success","num_turns":7,"duration_ms":1000,"usage":{"input_tokens":110,"output_tokens":25}}` + "\n")
	sc.Poll()

	if sc.Result == nil {
		t.Fatal("expected Result after terminal event")
	}
	if sc.Turns != 7 {
		t.Errorf("Turns = %d, want 7 (result event is authoritative)", sc.Turns)
	}
}
