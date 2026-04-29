package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ateam/internal/calldb"
)

func TestRunCatFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stream.jsonl")

	lines := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"abc42","model":"claude-opus","cwd":"/tmp"}`,
		`{"type":"user"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Hello from cat test"}]}}`,
		`{"type":"result","total_cost_usd":0.05,"duration_ms":10000,"num_turns":1,"usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":0}}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(lines), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Disable color so output is plain text.
	saved := catNoColor
	catNoColor = true
	defer func() { catNoColor = saved }()

	out := captureStdout(t, func() {
		if err := runCatFiles([]string{path}); err != nil {
			t.Errorf("runCatFiles: %v", err)
		}
	})

	if !strings.Contains(out, "session abc42") {
		t.Errorf("expected session id in output:\n%s", out)
	}
	if !strings.Contains(out, "Turn 1") {
		t.Errorf("expected turn header in output:\n%s", out)
	}
	if !strings.Contains(out, "Hello from cat test") {
		t.Errorf("expected assistant text in output:\n%s", out)
	}
	if !strings.Contains(out, "Result") {
		t.Errorf("expected result section in output:\n%s", out)
	}
}

func TestRunCatIDs(t *testing.T) {
	_, projPath, env := setupTestProject(t)

	// Write a minimal JSONL stream file into the project dir.
	streamRelPath := filepath.Join("logs", "roles", "testing_basic", "stream.jsonl")
	streamAbsPath := filepath.Join(env.ProjectDir, streamRelPath)
	if err := os.MkdirAll(filepath.Dir(streamAbsPath), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	lines := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"sid99","model":"claude-sonnet","cwd":"/tmp"}`,
		`{"type":"user"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"IDs path output"}]}}`,
		`{"type":"result","total_cost_usd":0.02,"duration_ms":5000,"num_turns":1,"usage":{"input_tokens":50,"output_tokens":20,"cache_read_input_tokens":0}}`,
	}, "\n")
	if err := os.WriteFile(streamAbsPath, []byte(lines), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Seed calldb with a call that points to the stream file.
	db, err := calldb.Open(env.ProjectDBPath())
	if err != nil {
		t.Fatalf("Open calldb: %v", err)
	}
	defer db.Close()
	now := time.Now()
	id, err := db.InsertCall(&calldb.Call{
		ProjectID:  env.ProjectID(),
		Action:     "run",
		Role:       "testing_basic",
		StreamFile: streamRelPath,
		StartedAt:  now.Add(-5 * time.Minute),
	})
	if err != nil {
		t.Fatalf("InsertCall: %v", err)
	}
	if err := db.UpdateCall(id, &calldb.CallResult{
		EndedAt: now, DurationMS: 300000,
	}); err != nil {
		t.Fatalf("UpdateCall: %v", err)
	}

	// Point the global org flag at the org parent so root.Resolve finds the project.
	savedOrg := orgFlag
	savedNoColor := catNoColor
	defer func() {
		orgFlag = savedOrg
		catNoColor = savedNoColor
	}()
	orgFlag = filepath.Dir(env.OrgDir)
	catNoColor = true

	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			if err := runCatIDs([]string{"1"}); err != nil {
				t.Errorf("runCatIDs: %v", err)
			}
		})
	})

	if !strings.Contains(out, "[ID:1]") {
		t.Errorf("expected [ID:1] header in output:\n%s", out)
	}
	if !strings.Contains(out, "IDs path output") {
		t.Errorf("expected stream text in output:\n%s", out)
	}
}
