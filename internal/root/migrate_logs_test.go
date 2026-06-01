package root

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ateam/internal/calldb"
)

func TestMigrateLogsLayout_NoOpOnFreshProject(t *testing.T) {
	projDir := t.TempDir()
	dbPath := filepath.Join(projDir, "state.sqlite")
	db, err := calldb.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	env := &ResolvedEnv{ProjectDir: projDir}
	if err := MigrateLogsLayout(env, db); err != nil {
		t.Fatalf("MigrateLogsLayout: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projDir, layoutSentinel)); err != nil {
		t.Errorf("sentinel not written on no-op path: %v", err)
	}
}

func TestMigrateLogsLayout_MovesLegacyFiles(t *testing.T) {
	projDir := t.TempDir()
	dbPath := filepath.Join(projDir, "state.sqlite")
	db, err := calldb.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	// Set up legacy layout: logs/roles/security/<TS>_report_*
	roleLogsDir := filepath.Join(projDir, "logs", "roles", "security")
	if err := os.MkdirAll(roleLogsDir, 0755); err != nil {
		t.Fatal(err)
	}
	ts := time.Now().Format("2006-01-02_15-04-05")
	prefix := filepath.Join(roleLogsDir, ts+"_report")
	for _, suffix := range []string{"_stream.jsonl", "_stderr.log", "_settings.json", "_exec.md"} {
		if err := os.WriteFile(prefix+suffix, []byte("legacy:"+suffix), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Legacy prompt archive at the matching timestamp.
	histDir := filepath.Join(projDir, "roles", "security", "history")
	if err := os.MkdirAll(histDir, 0755); err != nil {
		t.Fatal(err)
	}
	promptPath := filepath.Join(histDir, ts+".report_prompt.md")
	if err := os.WriteFile(promptPath, []byte("the prompt body"), 0644); err != nil {
		t.Fatal(err)
	}
	// Output history file (must NOT be deleted).
	outputPath := filepath.Join(histDir, ts+".report.md")
	if err := os.WriteFile(outputPath, []byte("the output"), 0644); err != nil {
		t.Fatal(err)
	}

	// Legacy runner.log + canonical error file (must be deleted).
	if err := os.WriteFile(filepath.Join(projDir, "logs", "runner.log"), []byte("rl"), 0644); err != nil {
		t.Fatal(err)
	}
	roleDir := filepath.Join(projDir, "roles", "security")
	if err := os.WriteFile(filepath.Join(roleDir, "report_error.md"), []byte("e"), 0644); err != nil {
		t.Fatal(err)
	}

	// DB row pointing at the legacy stream path. Parse in local tz so the
	// RFC3339-encoded started_at carries the same wall-clock the file used
	// (that's how InsertCall stored it for real runs).
	relStream, _ := filepath.Rel(projDir, prefix+"_stream.jsonl")
	startedAt, _ := time.ParseInLocation("2006-01-02_15-04-05", ts, time.Local)
	id, err := db.InsertCall(&calldb.Call{
		Action:    "report",
		Role:      "security",
		StartedAt: startedAt,
		AgentFile: relStream,
	})
	if err != nil {
		t.Fatalf("InsertCall: %v", err)
	}

	env := &ResolvedEnv{ProjectDir: projDir}
	if err := MigrateLogsLayout(env, db); err != nil {
		t.Fatalf("MigrateLogsLayout: %v", err)
	}

	newDir := filepath.Join(projDir, "logs", relativeIDDir(id))
	for _, name := range []string{"agent.jsonl", "stderr.out", "settings.json", "cmd.md", "prompt.md"} {
		if _, err := os.Stat(filepath.Join(newDir, name)); err != nil {
			t.Errorf("expected %s in new layout dir: %v", name, err)
		}
	}
	if _, err := os.Stat(promptPath); err == nil {
		t.Errorf("legacy prompt history file should have been moved, still exists at %s", promptPath)
	}
	if _, err := os.Stat(outputPath); err != nil {
		t.Errorf("legacy output history file %s must be preserved: %v", outputPath, err)
	}
	if _, err := os.Stat(filepath.Join(projDir, "logs", "runner.log")); err == nil {
		t.Errorf("runner.log should have been removed")
	}
	if _, err := os.Stat(filepath.Join(roleDir, "report_error.md")); err == nil {
		t.Errorf("legacy report_error.md should have been removed")
	}

	// Sentinel written.
	if _, err := os.Stat(filepath.Join(projDir, layoutSentinel)); err != nil {
		t.Errorf("sentinel not written: %v", err)
	}

	// Second invocation must be a no-op.
	if err := MigrateLogsLayout(env, db); err != nil {
		t.Errorf("second invocation: %v", err)
	}
}

// TestMigrateLogsLayout_PromptSkewMatching covers prompt timestamps that drift
// from started_at by more than the old ±5s probe (3s and 4s were silently
// missed). Also asserts that an unmatched prompt is left in place — never
// destroyed by migration.
func TestMigrateLogsLayout_PromptSkewMatching(t *testing.T) {
	cases := []struct {
		name      string
		skewSecs  int
		wantMoved bool
	}{
		{"exact match", 0, true},
		{"3s skew", 3, true},
		{"4s skew", 4, true},
		{"30s skew (within window)", 30, true},
		{"90s skew (outside window)", 90, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			projDir := t.TempDir()
			db, err := calldb.Open(filepath.Join(projDir, "state.sqlite"))
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer db.Close()

			rowTime := time.Now().Truncate(time.Second)
			fileTime := rowTime.Add(time.Duration(tc.skewSecs) * time.Second)

			roleLogsDir := filepath.Join(projDir, "logs", "roles", "security")
			if err := os.MkdirAll(roleLogsDir, 0755); err != nil {
				t.Fatal(err)
			}
			fileTS := fileTime.Format("2006-01-02_15-04-05")
			rowTS := rowTime.Format("2006-01-02_15-04-05")
			prefix := filepath.Join(roleLogsDir, rowTS+"_report")
			for _, suffix := range []string{"_stream.jsonl", "_stderr.log", "_settings.json", "_exec.md"} {
				if err := os.WriteFile(prefix+suffix, []byte("legacy"), 0644); err != nil {
					t.Fatal(err)
				}
			}

			histDir := filepath.Join(projDir, "roles", "security", "history")
			if err := os.MkdirAll(histDir, 0755); err != nil {
				t.Fatal(err)
			}
			promptPath := filepath.Join(histDir, fileTS+".report_prompt.md")
			if err := os.WriteFile(promptPath, []byte("p"), 0644); err != nil {
				t.Fatal(err)
			}

			relStream, _ := filepath.Rel(projDir, prefix+"_stream.jsonl")
			id, err := db.InsertCall(&calldb.Call{
				Action:    "report",
				Role:      "security",
				StartedAt: rowTime,
				AgentFile: relStream,
			})
			if err != nil {
				t.Fatalf("InsertCall: %v", err)
			}

			env := &ResolvedEnv{ProjectDir: projDir}
			if err := MigrateLogsLayout(env, db); err != nil {
				t.Fatalf("MigrateLogsLayout: %v", err)
			}

			newPrompt := filepath.Join(projDir, "logs", formatInt(id), "prompt.md")
			_, newErr := os.Stat(newPrompt)
			_, oldErr := os.Stat(promptPath)
			if tc.wantMoved {
				if newErr != nil {
					t.Errorf("expected prompt at %s after migration: %v", newPrompt, newErr)
				}
				if oldErr == nil {
					t.Errorf("legacy prompt %s should have been moved", promptPath)
				}
			} else {
				if newErr == nil {
					t.Errorf("prompt should NOT have been moved (skew %ds outside window)", tc.skewSecs)
				}
				if oldErr != nil {
					t.Errorf("unmatched legacy prompt must remain on disk for forensics: %v", oldErr)
				}
			}
		})
	}
}

// TestMigrateLogsLayout_LeavesUnmatchedPromptsAlone covers the scenario where
// no DB row exists for a legacy prompt. The migration must not delete it.
func TestMigrateLogsLayout_LeavesUnmatchedPromptsAlone(t *testing.T) {
	projDir := t.TempDir()
	db, err := calldb.Open(filepath.Join(projDir, "state.sqlite"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	// One legacy stream file → migration triggers, but the prompt below has
	// no matching DB row.
	roleLogsDir := filepath.Join(projDir, "logs", "roles", "security")
	if err := os.MkdirAll(roleLogsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(roleLogsDir, "x_report_stream.jsonl"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	histDir := filepath.Join(projDir, "roles", "security", "history")
	if err := os.MkdirAll(histDir, 0755); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(histDir, "2020-01-01_00-00-00.report_prompt.md")
	if err := os.WriteFile(orphan, []byte("orphan"), 0644); err != nil {
		t.Fatal(err)
	}

	env := &ResolvedEnv{ProjectDir: projDir}
	if err := MigrateLogsLayout(env, db); err != nil {
		t.Fatalf("MigrateLogsLayout: %v", err)
	}

	if _, err := os.Stat(orphan); err != nil {
		t.Errorf("orphan prompt must NOT be deleted: %v", err)
	}
}

// relativeIDDir returns id formatted as a directory name. Mirrors what
// MigrateLogsLayout uses internally so the test doesn't reach into private
// state.
func relativeIDDir(id int64) string {
	// strconv.FormatInt(id, 10) — small enough, just inline.
	return formatInt(id)
}

func formatInt(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
