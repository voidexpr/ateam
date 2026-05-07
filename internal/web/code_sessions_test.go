package web

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ateam/internal/calldb"
)

func TestScanCodeSessions_AcceptsExecIDDirsAndCountsTasksFromRuntime(t *testing.T) {
	projDir := t.TempDir()
	db, err := calldb.Open(filepath.Join(projDir, "state.sqlite"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	rowTime := time.Date(2026, 3, 19, 0, 35, 57, 0, time.Local)
	id, err := db.InsertCall(&calldb.Call{
		Action:    "code",
		Role:      "supervisor",
		StartedAt: rowTime,
	})
	if err != nil {
		t.Fatalf("InsertCall: %v", err)
	}

	idStr := itoa(id)
	canonical := filepath.Join(projDir, "supervisor", "code", idStr)
	runtime := filepath.Join(projDir, "runtime", idStr)
	if err := os.MkdirAll(canonical, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(runtime, 0755); err != nil {
		t.Fatal(err)
	}
	// Canonical: post-promote files (no prompts).
	if err := os.WriteFile(filepath.Join(canonical, "execution_report.md"), []byte("rep"), 0644); err != nil {
		t.Fatal(err)
	}
	// Runtime: where prompts actually live (promote skips them).
	for _, name := range []string{"task1_code_prompt.md", "task2_code_prompt.md", "task3_code_prompt.md"} {
		if err := os.WriteFile(filepath.Join(runtime, name), []byte("p"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	sessions := scanCodeSessions(projDir, db)
	if len(sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(sessions))
	}
	got := sessions[0]
	if got.DirName != idStr {
		t.Errorf("DirName = %q, want %q", got.DirName, idStr)
	}
	if got.TaskCount != 3 {
		t.Errorf("TaskCount = %d, want 3 (regression: prompts must be counted from runtime/)", got.TaskCount)
	}
	if !got.HasReport {
		t.Errorf("HasReport must be true when execution_report.md is in canonical")
	}
	if !got.Timestamp.Equal(rowTime) {
		t.Errorf("Timestamp = %v, want %v (from agent_execs row)", got.Timestamp, rowTime)
	}
}

func TestScanCodeSessions_LegacyTimestampDirsStillWork(t *testing.T) {
	projDir := t.TempDir()
	db, err := calldb.Open(filepath.Join(projDir, "state.sqlite"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	tsName := "2026-03-19_00-35-57"
	canonical := filepath.Join(projDir, "supervisor", "code", tsName)
	if err := os.MkdirAll(canonical, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"taskA_code_prompt.md", "taskB_code_prompt.md", "execution_report.md"} {
		if err := os.WriteFile(filepath.Join(canonical, name), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	sessions := scanCodeSessions(projDir, db)
	if len(sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(sessions))
	}
	got := sessions[0]
	if got.DirName != tsName {
		t.Errorf("DirName = %q, want %q", got.DirName, tsName)
	}
	if got.TaskCount != 2 {
		t.Errorf("TaskCount = %d, want 2 (legacy: count from canonical)", got.TaskCount)
	}
	if !got.HasReport {
		t.Errorf("HasReport must be true")
	}
}
