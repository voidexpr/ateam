package codex

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	if stats.TurnCount != 3 {
		t.Errorf("TurnCount = %d, want 3 (one per agent_message)", stats.TurnCount)
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
	path, err := FindSessionLog(home, "/some/workdir", since, "")
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
	path, err := FindSessionLog(home, workdir, since, "")
	if err != nil {
		t.Fatalf("FindSessionLog: %v", err)
	}
	if path != rollout {
		t.Errorf("path = %q, want %q", path, rollout)
	}
}

// TestGzipCopyFileRoundtrip verifies the gzip-copy preserves content
// byte-for-byte and writes a real gzip stream (so `gunzip < file` works
// for the user, and `ateam inspect` can show the archive size).
func TestGzipCopyFileRoundtrip(t *testing.T) {
	src := filepath.Join(t.TempDir(), "rollout.jsonl")
	content := strings.Repeat(`{"type":"event_msg","payload":{"type":"agent_message","message":"hello world"}}`+"\n", 100)
	if err := os.WriteFile(src, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "out", "codex-session.jsonl.gz")
	n, err := GzipCopyFile(src, dst)
	if err != nil {
		t.Fatalf("GzipCopyFile: %v", err)
	}
	if int(n) != len(content) {
		t.Errorf("returned bytes = %d, want %d (uncompressed)", n, len(content))
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("dst missing: %v", err)
	}
	// Verify the gzip is decodable and the content matches.
	f, err := os.Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	defer gz.Close()
	got, err := io.ReadAll(gz)
	if err != nil {
		t.Fatalf("read gz: %v", err)
	}
	if string(got) != content {
		t.Errorf("roundtrip mismatch:\ngot  %d bytes\nwant %d bytes", len(got), len(content))
	}
}

// TestGzipCopyFileRejectsMissingSrc: cleanly errors instead of
// creating an empty archive when the source doesn't exist.
func TestGzipCopyFileRejectsMissingSrc(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "out.gz")
	_, err := GzipCopyFile(filepath.Join(t.TempDir(), "no-such-file"), dst)
	if err == nil {
		t.Fatal("expected error for missing src")
	}
}

// TestFindSessionLogMarkerDisambiguates: when two rollout files match the
// same workdir+timestamp window, prefer the one whose first user_message
// contains the EXEC_ID marker — locks in the concurrent-run fix that
// resolves the v1.1 known limitation.
func TestFindSessionLogMarkerDisambiguates(t *testing.T) {
	home := t.TempDir()
	workdir := t.TempDir()
	sessions := filepath.Join(home, "sessions", "2026", "05", "22")
	if err := os.MkdirAll(sessions, 0700); err != nil {
		t.Fatal(err)
	}
	// Two rollout files for the same workdir, one second apart. Both pass
	// the cwd + timestamp filter. Without the marker the timestamp
	// tiebreaker would pick the second one ("ourID" run), so we use the
	// marker to deliberately pick the OTHER file ("theirID" run).
	mkRollout := func(name, ts, userMsg string) string {
		p := filepath.Join(sessions, name)
		meta := `{"timestamp":"` + ts + `","type":"session_meta","payload":{"id":"` + name + `","cwd":"` + workdir + `","timestamp":"` + ts + `","originator":"codex-tui","cli_version":"0.132.0"}}`
		um := `{"timestamp":"` + ts + `","type":"event_msg","payload":{"type":"user_message","message":"` + userMsg + `"}}`
		body := meta + "\n" + um + "\n"
		if err := os.WriteFile(p, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		when, _ := time.Parse(time.RFC3339, ts)
		_ = os.Chtimes(p, when, when)
		return p
	}
	older := mkRollout("rollout-A.jsonl", "2026-05-22T18:00:00Z", "Please review [ateam-exec-42]")
	_ = mkRollout("rollout-B.jsonl", "2026-05-22T18:00:01Z", "Please review [ateam-exec-43]")

	since, _ := time.Parse(time.RFC3339, "2026-05-22T17:59:00Z")
	// With marker for exec 42 → must return the OLDER file even though
	// the timestamp tiebreaker would prefer the newer one.
	path, err := FindSessionLog(home, workdir, since, "[ateam-exec-42]")
	if err != nil {
		t.Fatalf("FindSessionLog: %v", err)
	}
	if path != older {
		t.Errorf("path = %q, want %q (marker should override timestamp tiebreaker)", path, older)
	}
	// Without marker → timestamp tiebreaker picks the newer file.
	path, err = FindSessionLog(home, workdir, since, "")
	if err != nil {
		t.Fatalf("FindSessionLog: %v", err)
	}
	if path == older {
		t.Errorf("path = %q, expected the newer of the two without a marker", path)
	}
}
