package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/root"
)

// TestTopViewLifecycle drives a topView through discovery of a running
// exec, live progress from its stream file, and finalization once the DB
// row gets an end timestamp.
func TestTopViewLifecycle(t *testing.T) {
	dir := t.TempDir()
	projectDir := filepath.Join(dir, ".ateam")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	db, err := calldb.Open(filepath.Join(projectDir, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	id, err := db.InsertCall(&calldb.Call{
		Role:      "security",
		Action:    "report",
		StartedAt: time.Now(),
		AgentFile: "logs/1/agent.jsonl",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Our own PID keeps the run classified as alive during the test.
	if err := db.SetPID(id, os.Getpid(), ""); err != nil {
		t.Fatal(err)
	}

	view := &topView{
		env:   &root.ResolvedEnv{ProjectDir: projectDir},
		db:    db,
		index: map[int64]int{},
	}

	if err := view.refreshFromDB(); err != nil {
		t.Fatal(err)
	}
	if len(view.rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(view.rows))
	}
	if got := view.rows[0]; got.ExecID != id || got.Label != "security/report" || got.State != poolStateRunning {
		t.Fatalf("unexpected row after discovery: %+v", got)
	}

	streamPath := filepath.Join(projectDir, "logs", "1", "agent.jsonl")
	if err := os.MkdirAll(filepath.Dir(streamPath), 0755); err != nil {
		t.Fatal(err)
	}
	stream := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}],"usage":{"input_tokens":100,"output_tokens":20,"cache_read_input_tokens":1000}}}` + "\n"
	if err := os.WriteFile(streamPath, []byte(stream), 0644); err != nil {
		t.Fatal(err)
	}

	view.pollStreams()
	row := view.rows[0]
	if row.EstTokens != 120 || row.Turns != 1 {
		t.Errorf("EstTokens/Turns = %d/%d, want 120/1", row.EstTokens, row.Turns)
	}
	if !strings.Contains(row.Detail, "Bash") || !strings.Contains(row.Detail, "ctx:") {
		t.Errorf("Detail = %q, want current tool and context size", row.Detail)
	}

	if err := db.UpdateCall(id, &calldb.CallResult{
		EndedAt:      time.Now(),
		DurationMS:   1500,
		InputTokens:  100,
		OutputTokens: 20,
		Turns:        3,
	}); err != nil {
		t.Fatal(err)
	}
	if err := view.refreshFromDB(); err != nil {
		t.Fatal(err)
	}
	row = view.rows[0]
	if row.State != poolStateDone {
		t.Fatalf("State = %q after finalize, want %q (row: %+v)", row.State, poolStateDone, row)
	}
	if row.Turns != 3 {
		t.Errorf("Turns = %d after finalize, want 3 (DB row is authoritative)", row.Turns)
	}

	// Finalized rows must not regress to running on later polls.
	view.pollStreams()
	if view.rows[0].State != poolStateDone {
		t.Errorf("State = %q after extra poll, want %q", view.rows[0].State, poolStateDone)
	}
}
