package cmd

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/ateam/internal/config"
	_ "modernc.org/sqlite"
)

func TestRewriteStreamPath(t *testing.T) {
	tests := []struct {
		old  string
		want string
	}{
		{
			"roles/security/logs/2026-03-18_stream.jsonl",
			"logs/roles/security/2026-03-18_stream.jsonl",
		},
		{
			"roles/testing_basic/logs/2026-03-10_22-17-58_report_stream.jsonl",
			"logs/roles/testing_basic/2026-03-10_22-17-58_report_stream.jsonl",
		},
		{
			"supervisor/logs/2026-03-10_22-18-00_review_stream.jsonl",
			"logs/supervisor/2026-03-10_22-18-00_review_stream.jsonl",
		},
		{
			"roles/security/logs/2026-03-18_report_exec.md",
			"logs/roles/security/2026-03-18_report_exec.md",
		},
		{
			"runner.log",
			"logs/runner.log",
		},
	}

	for _, tt := range tests {
		t.Run(tt.old, func(t *testing.T) {
			got := rewriteStreamPath(tt.old)
			if got != tt.want {
				t.Errorf("rewriteStreamPath(%q) = %q, want %q", tt.old, got, tt.want)
			}
		})
	}
}

func resolvedTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	return resolved
}

func TestMigrateProject(t *testing.T) {
	base := resolvedTempDir(t)

	orgDir := filepath.Join(base, ".ateamorg")
	orgRoot := base

	// Create project structure
	projectRelPath := "services/api"
	projectID := config.PathToProjectID(projectRelPath)
	projectPath := filepath.Join(orgRoot, projectRelPath)
	ateamDir := filepath.Join(projectPath, ".ateam")
	stateDir := filepath.Join(orgDir, "projects", projectID)

	// Create old state directory with log files
	oldRoleLogs := filepath.Join(stateDir, "roles", "security", "logs")
	os.MkdirAll(oldRoleLogs, 0755)
	os.WriteFile(filepath.Join(oldRoleLogs, "2026-03-18_report_stream.jsonl"), []byte(`{"type":"result"}`), 0644)
	os.WriteFile(filepath.Join(oldRoleLogs, "2026-03-18_report_stderr.log"), []byte("stderr"), 0644)

	oldSupLogs := filepath.Join(stateDir, "supervisor", "logs")
	os.MkdirAll(oldSupLogs, 0755)
	os.WriteFile(filepath.Join(oldSupLogs, "2026-03-18_review_stream.jsonl"), []byte(`{"type":"result"}`), 0644)

	os.WriteFile(filepath.Join(stateDir, "runner.log"), []byte("log line\n"), 0644)

	// Create .ateam/ dir (minimal)
	os.MkdirAll(ateamDir, 0755)

	// Create org DB with some rows
	orgDBPath := filepath.Join(orgDir, "state.sqlite")
	orgDB, err := sql.Open("sqlite", orgDBPath+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	orgDB.Exec(`CREATE TABLE agent_execs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		project_id TEXT NOT NULL DEFAULT '',
		profile TEXT NOT NULL DEFAULT '',
		agent TEXT NOT NULL DEFAULT '',
		container TEXT NOT NULL DEFAULT 'none',
		action TEXT NOT NULL DEFAULT '',
		role TEXT NOT NULL DEFAULT '',
		task_group TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		prompt_hash TEXT NOT NULL DEFAULT '',
		started_at TEXT NOT NULL,
		stream_file TEXT NOT NULL DEFAULT '',
		ended_at TEXT,
		duration_ms INTEGER,
		exit_code INTEGER,
		is_error INTEGER NOT NULL DEFAULT 0,
		error_message TEXT NOT NULL DEFAULT '',
		cost_usd REAL,
		input_tokens INTEGER,
		output_tokens INTEGER,
		cache_read_tokens INTEGER,
		turns INTEGER,
		pid INTEGER NOT NULL DEFAULT 0,
		container_id TEXT NOT NULL DEFAULT ''
	)`)

	// Insert rows for our project
	streamFile := "projects/" + projectID + "/roles/security/logs/2026-03-18_report_stream.jsonl"
	orgDB.Exec(`INSERT INTO agent_execs (project_id, profile, agent, action, role, started_at, stream_file, ended_at, cost_usd, is_error)
		VALUES (?, 'default', 'claude', 'report', 'security', '2026-03-18T10:00:00Z', ?, '2026-03-18T10:05:00Z', 0.50, 0)`,
		projectID, streamFile)

	supStreamFile := "projects/" + projectID + "/supervisor/logs/2026-03-18_review_stream.jsonl"
	orgDB.Exec(`INSERT INTO agent_execs (project_id, profile, agent, action, role, started_at, stream_file, ended_at, cost_usd, is_error)
		VALUES (?, 'default', 'claude', 'review', 'supervisor', '2026-03-18T10:10:00Z', ?, '2026-03-18T10:15:00Z', 0.30, 0)`,
		projectID, supStreamFile)

	// Insert a row for a different project (should not be migrated)
	orgDB.Exec(`INSERT INTO agent_execs (project_id, profile, agent, action, role, started_at, stream_file, is_error)
		VALUES ('other_project', 'default', 'claude', 'report', 'security', '2026-03-18T11:00:00Z', 'projects/other_project/roles/security/logs/stream.jsonl', 0)`)

	orgDB.Close()

	// Run migration
	p := migrationProject{
		projectID: projectID,
		relPath:   projectRelPath,
		ateamDir:  ateamDir,
		stateDir:  stateDir,
	}

	filesCopied, rowsCopied, err := migrateProject(orgDir, orgRoot, orgDBPath, p, false)
	if err != nil {
		t.Fatalf("migrateProject: %v", err)
	}

	// Verify files were copied
	if filesCopied != 4 { // 2 role logs + 1 supervisor log + 1 runner.log
		t.Errorf("filesCopied = %d, want 4", filesCopied)
	}

	// Check new log file locations
	newRoleStream := filepath.Join(ateamDir, "logs", "roles", "security", "2026-03-18_report_stream.jsonl")
	if _, err := os.Stat(newRoleStream); err != nil {
		t.Errorf("role stream not found at new location: %v", err)
	}
	newSupStream := filepath.Join(ateamDir, "logs", "supervisor", "2026-03-18_review_stream.jsonl")
	if _, err := os.Stat(newSupStream); err != nil {
		t.Errorf("supervisor stream not found at new location: %v", err)
	}
	newRunnerLog := filepath.Join(ateamDir, "logs", "runner.log")
	if _, err := os.Stat(newRunnerLog); err != nil {
		t.Errorf("runner.log not found at new location: %v", err)
	}

	// Verify DB rows were copied
	if rowsCopied != 2 {
		t.Errorf("rowsCopied = %d, want 2", rowsCopied)
	}

	// Read back from project DB and verify
	projDBPath := filepath.Join(ateamDir, "state.sqlite")
	projDB, err := sql.Open("sqlite", projDBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer projDB.Close()

	var count int
	projDB.QueryRow("SELECT COUNT(*) FROM agent_execs").Scan(&count)
	if count != 2 {
		t.Errorf("project DB has %d rows, want 2", count)
	}

	// Verify project_id was rewritten to ""
	var pid string
	projDB.QueryRow("SELECT project_id FROM agent_execs LIMIT 1").Scan(&pid)
	if pid != "" {
		t.Errorf("project_id = %q, want empty string", pid)
	}

	// Verify stream_file was rewritten
	var sf string
	projDB.QueryRow("SELECT stream_file FROM agent_execs WHERE action='report'").Scan(&sf)
	wantSF := "logs/roles/security/2026-03-18_report_stream.jsonl"
	if sf != wantSF {
		t.Errorf("stream_file = %q, want %q", sf, wantSF)
	}

	var sfSup string
	projDB.QueryRow("SELECT stream_file FROM agent_execs WHERE action='review'").Scan(&sfSup)
	wantSFSup := "logs/supervisor/2026-03-18_review_stream.jsonl"
	if sfSup != wantSFSup {
		t.Errorf("stream_file = %q, want %q", sfSup, wantSFSup)
	}

	// Verify .gitignore was created
	gitignore := filepath.Join(ateamDir, ".gitignore")
	if _, err := os.Stat(gitignore); err != nil {
		t.Errorf(".gitignore not created: %v", err)
	}

	// Verify idempotency: running again should skip DB rows
	filesCopied2, rowsCopied2, err := migrateProject(orgDir, orgRoot, orgDBPath, p, false)
	if err != nil {
		t.Fatalf("second migrateProject: %v", err)
	}
	if rowsCopied2 != 0 {
		t.Errorf("second run: rowsCopied = %d, want 0 (idempotent)", rowsCopied2)
	}
	if filesCopied2 != 0 {
		// runner.log appends, but regular files are skipped
		// runner.log will append again, so it counts as 1
		if filesCopied2 > 1 {
			t.Errorf("second run: filesCopied = %d, want 0 or 1", filesCopied2)
		}
	}
}
