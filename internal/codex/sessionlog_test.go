package codex

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadSessionStatsFixture(t *testing.T) {
	stats, err := ReadSessionStats("testdata/rollout-sample.jsonl")
	if err != nil {
		t.Fatalf("ReadSessionStats: %v", err)
	}
	if stats.SessionID != "019e50f2-d8e8-71c0-b971-14dd6da73e10" {
		t.Errorf("SessionID = %q", stats.SessionID)
	}
	if stats.Model != "gpt-5.5" {
		t.Errorf("Model = %q, want gpt-5.5", stats.Model)
	}
	if stats.InputTokens != 42778 || stats.OutputTokens != 1213 || stats.CachedInputTokens != 3456 {
		t.Errorf("token counts wrong: %+v", stats)
	}
	if stats.ReasoningTokens != 1118 || stats.TotalTokens != 43991 {
		t.Errorf("derived tokens wrong: %+v", stats)
	}
	if stats.ContextWindow != 258400 {
		t.Errorf("ContextWindow = %d, want 258400", stats.ContextWindow)
	}
	if stats.DurationMS != 29485 || stats.TimeToFirstTokenMS != 9168 {
		t.Errorf("duration/ttft wrong: %+v", stats)
	}
	if !stats.TaskCompleteFound || !stats.TokenCountFound {
		t.Errorf("completion flags missing: %+v", stats)
	}
}

// TestFindSessionLogIgnoresStaleFiles verifies that we don't return a file
// whose session_meta.cwd doesn't match — even if it happens to be in the
// right date directory. This is the safety net that keeps two concurrent
// codex-tmux runs in different projects from mixing up each other's token
// counts.
func TestFindSessionLogIgnoresStaleFiles(t *testing.T) {
	home := t.TempDir()
	sessions := filepath.Join(home, "sessions", "2026", "05", "22")
	if err := os.MkdirAll(sessions, 0700); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(sessions, "rollout-2026-05-22T11-00-00-other.jsonl")
	body := `{"timestamp":"2026-05-22T18:00:00Z","type":"session_meta","payload":{"id":"x","cwd":"/different/path","timestamp":"2026-05-22T18:00:00Z","originator":"codex-tui","cli_version":"0.132.0"}}` + "\n"
	if err := os.WriteFile(other, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	since, _ := time.Parse(time.RFC3339, "2026-05-22T17:59:00Z")
	path, err := FindSessionLog(home, "/some/workdir", since)
	if err != nil {
		t.Fatalf("FindSessionLog: %v", err)
	}
	if path != "" {
		t.Errorf("expected no match, got %q", path)
	}
}

// TestFindSessionLogMatchesCWD verifies a positive match returns the file.
func TestFindSessionLogMatchesCWD(t *testing.T) {
	home := t.TempDir()
	workdir := t.TempDir()
	sessions := filepath.Join(home, "sessions", "2026", "05", "22")
	if err := os.MkdirAll(sessions, 0700); err != nil {
		t.Fatal(err)
	}
	rollout := filepath.Join(sessions, "rollout-2026-05-22T11-00-00-target.jsonl")
	body := `{"timestamp":"2026-05-22T18:00:00Z","type":"session_meta","payload":{"id":"y","cwd":"` + workdir + `","timestamp":"2026-05-22T18:00:00Z","originator":"codex-tui","cli_version":"0.132.0"}}` + "\n"
	if err := os.WriteFile(rollout, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	// Backdate the file's mtime so the timestamp parse drives the decision.
	when, _ := time.Parse(time.RFC3339, "2026-05-22T18:00:00Z")
	if err := os.Chtimes(rollout, when, when); err != nil {
		t.Fatal(err)
	}
	since, _ := time.Parse(time.RFC3339, "2026-05-22T17:59:00Z")
	path, err := FindSessionLog(home, workdir, since)
	if err != nil {
		t.Fatalf("FindSessionLog: %v", err)
	}
	if path != rollout {
		t.Errorf("path = %q, want %q", path, rollout)
	}
}
