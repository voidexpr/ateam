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
	if p := sc.Progress(0, 0); p.TurnCount != 0 || p.ToolCount != 0 {
		t.Fatalf("expected zero counters before file exists, got turns=%d tools=%d", p.TurnCount, p.ToolCount)
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

	p := sc.Progress(0, 0)
	if p.TurnCount != 1 {
		t.Errorf("TurnCount = %d, want 1 (usage counted once per multi-block message)", p.TurnCount)
	}
	if p.Phase != PhaseTool || p.ToolCount != 1 || p.ToolName != "Bash" {
		t.Errorf("Phase/ToolCount/ToolName = %s/%d/%q, want %s/1/Bash", p.Phase, p.ToolCount, p.ToolName, PhaseTool)
	}
	if p.CumulativeInputTokens != 100 || p.CumulativeOutputTokens != 20 {
		t.Errorf("cumulative tokens = %d/%d, want 100/20", p.CumulativeInputTokens, p.CumulativeOutputTokens)
	}
	if p.ContextTokens != 1150 {
		t.Errorf("ContextTokens = %d, want 1150 (input + cache create + cache read)", p.ContextTokens)
	}

	// A partial line must be carried until its remainder arrives.
	line2 := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t2","name":"Read","input":{"file_path":"x"}}],"usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":0,"cache_read_input_tokens":1200}}}`
	appendFile(line2[:40])
	sc.Poll()
	if p := sc.Progress(0, 0); p.ToolCount != 1 {
		t.Fatalf("partial line consumed early: ToolCount = %d, want 1", p.ToolCount)
	}
	appendFile(line2[40:] + "\n")
	sc.Poll()

	p = sc.Progress(0, 0)
	if p.TurnCount != 2 || p.ToolCount != 2 || p.ToolName != "Read" {
		t.Errorf("after second message: turns=%d tools=%d last=%q, want 2/2/Read", p.TurnCount, p.ToolCount, p.ToolName)
	}
	if p.CumulativeInputTokens != 110 || p.CumulativeOutputTokens != 25 {
		t.Errorf("cumulative tokens = %d/%d, want 110/25", p.CumulativeInputTokens, p.CumulativeOutputTokens)
	}
	if p.ContextTokens != 1210 {
		t.Errorf("ContextTokens = %d, want 1210 (latest message)", p.ContextTokens)
	}

	appendFile(`{"type":"result","subtype":"success","num_turns":7,"duration_ms":1000,"usage":{"input_tokens":110,"output_tokens":25}}` + "\n")
	sc.Poll()

	if sc.result == nil {
		t.Fatal("expected terminal result after result event")
	}
	if p := sc.Progress(0, 0); p.TurnCount != 7 {
		t.Errorf("TurnCount = %d, want 7 (result event is authoritative)", p.TurnCount)
	}
}
