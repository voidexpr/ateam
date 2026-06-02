// Package calldb provides a SQLite-based database for tracking agent execution calls and run metadata.
package calldb

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// schema defines the agent_execs table and its indexes.
//
// # Call lifecycle
//
// A row is inserted when an agent execution starts (InsertCall). When the execution
// completes, the row is updated with timing and cost data (UpdateCall). A NULL ended_at
// means the call is still running or crashed before it could record a result.
//
// # Indexes
//
//   - idx_execs_started   – global chronological listing (used by "ateam ps" and log views)
//   - idx_execs_project   – per-project history filtered by time
//   - idx_execs_action    – filter by action type (report, code, review, …) ordered by time
//   - idx_execs_batch     – group all execs that belong to the same batch for cost aggregation
//   - idx_execs_role      – per-role history ordered by time
//
// # batch
//
// batch is a caller-supplied token that ties related agent_execs rows together
// (e.g. all execs spawned by a single "ateam code" invocation). It enables cost and
// token aggregation across a batch without a separate join table.
//
// # Columns
//
// Non-obvious column semantics:
//
//   - ended_at         – NULL while still running; also NULL (never set) for execs that were killed before completion.
//   - duration_ms      – NULL while still running; otherwise computed from ended_at - started_at in milliseconds.
//   - exit_code        – NULL while still running or killed; 0 = success, non-zero = failure.
//   - is_error         – set by the agent protocol to flag an error reported by the agent itself (independent of OS exit code).
//   - error_message    – human-readable description of the failure; free-form, may be empty even when is_error is set.
//   - exit_code vs. is_error vs. error_message – three independent error indicators: exit_code is the OS process exit code,
//     is_error is the agent-protocol error flag, and error_message is a human description. Any combination may be present.
//   - git_start_branch / git_end_branch – empty string on detached HEAD or in a non-git directory.
//   - output_file      – empty string has two meanings: either the file has not yet been written, or this is a legacy row
//     without the field; consumers should check for file existence before reading.
//   - work_dir         – host filesystem path; may differ from the container-internal path when running in docker.
//   - container_id     – empty string for non-containerized runs.
const schema = `
CREATE TABLE IF NOT EXISTS agent_execs (
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id        TEXT NOT NULL DEFAULT '',
  profile           TEXT NOT NULL DEFAULT '',
  agent             TEXT NOT NULL DEFAULT '',
  container         TEXT NOT NULL DEFAULT 'none',
  action            TEXT NOT NULL DEFAULT '',
  role              TEXT NOT NULL DEFAULT '',
  batch             TEXT NOT NULL DEFAULT '',
  model             TEXT NOT NULL DEFAULT '',
  prompt_hash       TEXT NOT NULL DEFAULT '',
  started_at        TEXT NOT NULL,
  stream_file       TEXT NOT NULL DEFAULT '',
  output_file       TEXT NOT NULL DEFAULT '',
  ended_at          TEXT,
  duration_ms       INTEGER,
  exit_code         INTEGER,
  is_error          INTEGER NOT NULL DEFAULT 0,
  error_message     TEXT NOT NULL DEFAULT '',
  cost_usd          REAL,
  input_tokens      INTEGER,
  output_tokens     INTEGER,
  cache_read_tokens  INTEGER,
  cache_write_tokens INTEGER,
  turns              INTEGER,
  pid                INTEGER NOT NULL DEFAULT 0,
  container_id       TEXT NOT NULL DEFAULT '',
  git_start_hash     TEXT NOT NULL DEFAULT '',
  git_end_hash       TEXT NOT NULL DEFAULT '',
  git_start_branch   TEXT NOT NULL DEFAULT '',
  git_end_branch     TEXT NOT NULL DEFAULT '',
  work_dir           TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_execs_started ON agent_execs(started_at);
CREATE INDEX IF NOT EXISTS idx_execs_project ON agent_execs(project_id, started_at);
CREATE INDEX IF NOT EXISTS idx_execs_action ON agent_execs(action, started_at);
CREATE INDEX IF NOT EXISTS idx_execs_batch ON agent_execs(batch);
CREATE INDEX IF NOT EXISTS idx_execs_role ON agent_execs(role, started_at);
CREATE INDEX IF NOT EXISTS idx_execs_work_dir ON agent_execs(work_dir);
`

type Call struct {
	ProjectID      string
	Profile        string
	Agent          string
	Container      string
	Action         string
	Role           string
	Batch          string
	Model          string
	PromptHash     string
	StartedAt      time.Time
	AgentFile      string
	OutputFile     string
	GitStartHash   string
	GitStartBranch string
	WorkDir        string // absolute path of the agent's working directory
}

type CallResult struct {
	EndedAt           time.Time
	DurationMS        int64
	ExitCode          int
	IsError           bool
	ErrorMessage      string
	CostUSD           float64
	InputTokens       int
	OutputTokens      int
	CacheReadTokens   int
	CacheWriteTokens  int
	Turns             int
	Model             string // if non-empty, updates the model column
	PeakContextTokens int
	ContextWindow     int
	GitEndHash        string
	GitEndBranch      string
}

type CallDB struct {
	db *sql.DB
}

// OpenIfExists opens an existing database file. Returns (nil, nil) if the file
// does not exist, avoiding creation of empty DB files on read-only code paths.
func OpenIfExists(dbPath string) (*CallDB, error) {
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return nil, nil
	}
	return Open(dbPath)
}

func Open(dbPath string) (*CallDB, error) {
	// Pre-create the file with owner-only permissions so the SQLite driver
	// inherits 0600 rather than the default 0666.
	f, err := os.OpenFile(dbPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", dbPath, err)
	}
	f.Close()

	dsn := dbPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dbPath, err)
	}

	// Serialize writes through a single connection to avoid SQLITE_BUSY.
	// Reads can still proceed concurrently via WAL.
	db.SetMaxOpenConns(1)

	// Skip schema creation when either an old (agent_calls) or pre-rename
	// (agent_execs with task_group column) table is present — migrate()
	// brings them up to the current schema. Running the schema against a
	// pre-rename agent_execs would fail at `CREATE INDEX idx_execs_batch`
	// because the batch column doesn't exist yet.
	var hasOldTable, hasNewTable bool
	_ = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='agent_calls'").Scan(&hasOldTable)
	_ = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='agent_execs'").Scan(&hasNewTable)
	if !hasOldTable && !hasNewTable {
		if _, err := db.Exec(schema); err != nil {
			db.Close()
			return nil, fmt.Errorf("create schema: %w", err)
		}
	}

	if err := migrate(db, dbPath); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return &CallDB{db: db}, nil
}

func migrate(db *sql.DB, dbPath string) error {
	orgDir := filepath.Dir(dbPath)

	// Check if old table name exists and needs migration.
	var oldTableExists bool
	err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='agent_calls'").Scan(&oldTableExists)
	if err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if oldTableExists {
		// Convert absolute stream_file paths to relative (relative to orgDir).
		rows, err := tx.Query("SELECT id, stream_file FROM agent_calls WHERE stream_file != '' AND stream_file LIKE '/%'")
		if err != nil {
			return err
		}
		var updates []struct {
			id  int64
			rel string
		}
		for rows.Next() {
			var id int64
			var sf string
			if err := rows.Scan(&id, &sf); err != nil {
				rows.Close()
				return err
			}
			rel, relErr := filepath.Rel(orgDir, sf)
			if relErr != nil || strings.HasPrefix(rel, ".."+string(filepath.Separator)+"..") {
				continue
			}
			updates = append(updates, struct {
				id  int64
				rel string
			}{id, rel})
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, u := range updates {
			if _, err := tx.Exec("UPDATE agent_calls SET stream_file = ? WHERE id = ?", u.rel, u.id); err != nil {
				return err
			}
		}

		// Rename old table to new name.
		if _, err := tx.Exec("ALTER TABLE agent_calls RENAME TO agent_execs"); err != nil {
			return err
		}
		// Drop old indexes (they still reference agent_calls internally but
		// SQLite keeps them working after rename; re-create with new names).
		for _, idx := range []string{
			"idx_calls_started", "idx_calls_project", "idx_calls_action",
			"idx_calls_task_group", "idx_calls_role",
		} {
			_, _ = tx.Exec("DROP INDEX IF EXISTS " + idx)
		}
		// Indexed against the legacy task_group column because the rename
		// has not happened yet at this point; the column-rename block below
		// drops idx_execs_task_group and creates idx_execs_batch.
		if _, err := tx.Exec(`
			CREATE INDEX IF NOT EXISTS idx_execs_started ON agent_execs(started_at);
			CREATE INDEX IF NOT EXISTS idx_execs_project ON agent_execs(project_id, started_at);
			CREATE INDEX IF NOT EXISTS idx_execs_action ON agent_execs(action, started_at);
			CREATE INDEX IF NOT EXISTS idx_execs_task_group ON agent_execs(task_group);
			CREATE INDEX IF NOT EXISTS idx_execs_role ON agent_execs(role, started_at);
		`); err != nil {
			return err
		}
	}

	// Add missing columns (works on both old-migrated and new tables).
	tRows, err := tx.Query("PRAGMA table_info(agent_execs)")
	if err != nil {
		return err
	}

	hasPID := false
	hasContainerID := false
	hasCacheWriteTokens := false
	hasOutputFile := false
	hasPeakContextTokens := false
	hasContextWindow := false
	hasTaskGroup := false
	hasBatch := false
	hasGitStartHash := false
	hasGitEndHash := false
	hasGitStartBranch := false
	hasGitEndBranch := false
	hasWorkDir := false
	for tRows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var dflt sql.NullString
		var pk int
		if err := tRows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			tRows.Close()
			return err
		}
		switch name {
		case "pid":
			hasPID = true
		case "container_id":
			hasContainerID = true
		case "cache_write_tokens":
			hasCacheWriteTokens = true
		case "output_file":
			hasOutputFile = true
		case "peak_context_tokens":
			hasPeakContextTokens = true
		case "context_window":
			hasContextWindow = true
		case "task_group":
			hasTaskGroup = true
		case "batch":
			hasBatch = true
		case "git_start_hash":
			hasGitStartHash = true
		case "git_end_hash":
			hasGitEndHash = true
		case "git_start_branch":
			hasGitStartBranch = true
		case "git_end_branch":
			hasGitEndBranch = true
		case "work_dir":
			hasWorkDir = true
		}
	}
	tRows.Close()
	if err := tRows.Err(); err != nil {
		return err
	}

	if hasTaskGroup && !hasBatch {
		if _, err := tx.Exec("ALTER TABLE agent_execs RENAME COLUMN task_group TO batch"); err != nil {
			return err
		}
		_, _ = tx.Exec("DROP INDEX IF EXISTS idx_execs_task_group")
		if _, err := tx.Exec("CREATE INDEX IF NOT EXISTS idx_execs_batch ON agent_execs(batch)"); err != nil {
			return err
		}
	}

	if !hasPID {
		if _, err := tx.Exec("ALTER TABLE agent_execs ADD COLUMN pid INTEGER NOT NULL DEFAULT 0"); err != nil {
			return err
		}
	}
	if !hasContainerID {
		if _, err := tx.Exec("ALTER TABLE agent_execs ADD COLUMN container_id TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	if !hasCacheWriteTokens {
		if _, err := tx.Exec("ALTER TABLE agent_execs ADD COLUMN cache_write_tokens INTEGER"); err != nil {
			return err
		}
	}
	if !hasOutputFile {
		if _, err := tx.Exec("ALTER TABLE agent_execs ADD COLUMN output_file TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	if !hasPeakContextTokens {
		if _, err := tx.Exec("ALTER TABLE agent_execs ADD COLUMN peak_context_tokens INTEGER"); err != nil {
			return err
		}
	}
	if !hasContextWindow {
		if _, err := tx.Exec("ALTER TABLE agent_execs ADD COLUMN context_window INTEGER"); err != nil {
			return err
		}
	}
	if !hasGitStartHash {
		if _, err := tx.Exec("ALTER TABLE agent_execs ADD COLUMN git_start_hash TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	if !hasGitEndHash {
		if _, err := tx.Exec("ALTER TABLE agent_execs ADD COLUMN git_end_hash TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	if !hasGitStartBranch {
		if _, err := tx.Exec("ALTER TABLE agent_execs ADD COLUMN git_start_branch TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	if !hasGitEndBranch {
		if _, err := tx.Exec("ALTER TABLE agent_execs ADD COLUMN git_end_branch TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	if !hasWorkDir {
		if _, err := tx.Exec("ALTER TABLE agent_execs ADD COLUMN work_dir TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
		if _, err := tx.Exec("CREATE INDEX IF NOT EXISTS idx_execs_work_dir ON agent_execs(work_dir)"); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	// Backfill work_dir from logs/<id>/cmd.md for any rows that pre-date the
	// column. dbPath is .ateam/state.sqlite, so its parent is .ateam itself.
	if err := backfillWorkDir(db, filepath.Dir(dbPath)); err != nil {
		// Non-fatal: backfill failures shouldn't block Open(). Rows just stay
		// empty and can be re-backfilled later or filled in lazily by readers.
		fmt.Fprintf(os.Stderr, "Warning: work_dir backfill incomplete: %v\n", err)
	}

	return nil
}

// backfillWorkDir scans agent_execs for rows with empty work_dir and parses
// the `* cwd: <path>` line from <ateamDir>/logs/<id>/cmd.md. cmd.md's cwd is
// always absolute (runner.go writes it via os.Getwd or an absolutized flag),
// so we store it verbatim. Missing or unparseable cmd.md leaves the row empty.
// Updates run inside a single transaction so partial failures roll back.
func backfillWorkDir(db *sql.DB, ateamDir string) error {
	rows, err := db.Query(`SELECT id FROM agent_execs WHERE work_dir = ''`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type pending struct {
		id  int64
		cwd string
	}
	var updates []pending
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		cmdPath := filepath.Join(ateamDir, "logs", strconv.FormatInt(id, 10), "cmd.md")
		cwd := parseCwdFromCmdMD(cmdPath)
		if cwd == "" {
			continue
		}
		updates = append(updates, pending{id: id, cwd: cwd})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(updates) == 0 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare("UPDATE agent_execs SET work_dir = ? WHERE id = ?")
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()

	for _, u := range updates {
		if _, err := stmt.Exec(u.cwd, u.id); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("update id=%d: %w", u.id, err)
		}
	}
	return tx.Commit()
}

// parseCwdFromCmdMD reads cmd.md and returns the value of the first `* cwd: …`
// line. Returns "" on any read or parse failure.
func parseCwdFromCmdMD(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, "* cwd: "); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func (c *CallDB) InsertCall(call *Call) (int64, error) {
	res, err := c.db.Exec(`
		INSERT INTO agent_execs (
			project_id, profile, agent, container, action, role,
			batch, model, prompt_hash, started_at, stream_file, output_file,
			git_start_hash, git_start_branch, work_dir
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		call.ProjectID, call.Profile, call.Agent, call.Container,
		call.Action, call.Role, call.Batch, call.Model,
		call.PromptHash, call.StartedAt.Format(time.RFC3339), call.AgentFile,
		call.OutputFile, call.GitStartHash, call.GitStartBranch, call.WorkDir,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (c *CallDB) UpdateCall(id int64, result *CallResult) error {
	isError := 0
	if result.IsError {
		isError = 1
	}
	q := `UPDATE agent_execs SET
			ended_at = ?, duration_ms = ?, exit_code = ?,
			is_error = ?, error_message = ?, cost_usd = ?,
			input_tokens = ?, output_tokens = ?, cache_read_tokens = ?,
			cache_write_tokens = ?, turns = ?,
			peak_context_tokens = ?, context_window = ?,
			git_end_hash = ?, git_end_branch = ?`
	args := []any{
		result.EndedAt.Format(time.RFC3339), result.DurationMS, result.ExitCode,
		isError, result.ErrorMessage, result.CostUSD,
		result.InputTokens, result.OutputTokens, result.CacheReadTokens,
		result.CacheWriteTokens, result.Turns,
		result.PeakContextTokens, result.ContextWindow,
		result.GitEndHash, result.GitEndBranch,
	}
	if result.Model != "" {
		q += `, model = ?`
		args = append(args, result.Model)
	}
	q += ` WHERE id = ?`
	args = append(args, id)
	_, err := c.db.Exec(q, args...)
	return err
}

func (c *CallDB) SetPID(id int64, pid int, containerID string) error {
	_, err := c.db.Exec("UPDATE agent_execs SET pid = ?, container_id = ? WHERE id = ?", pid, containerID, id)
	return err
}

// UpdateStreamFile sets the stream_file path for an exec row. Used after
// InsertCall returns the new id so the path can be derived from the id (e.g.
// logs/<id>/agent.jsonl).
func (c *CallDB) UpdateStreamFile(id int64, agentFile string) error {
	_, err := c.db.Exec("UPDATE agent_execs SET stream_file = ? WHERE id = ?", agentFile, id)
	return err
}

// UpdateOutputFile sets the output_file path. Called at finalize once the
// canonical output path is known. Display-only; not unique across rows.
func (c *CallDB) UpdateOutputFile(id int64, outputFile string) error {
	_, err := c.db.Exec("UPDATE agent_execs SET output_file = ? WHERE id = ?", outputFile, id)
	return err
}

func (c *CallDB) RawDB() *sql.DB {
	return c.db
}

func (c *CallDB) Close() error {
	return c.db.Close()
}
