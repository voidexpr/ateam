package calldb

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS agent_execs (
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id        TEXT NOT NULL DEFAULT '',
  profile           TEXT NOT NULL DEFAULT '',
  agent             TEXT NOT NULL DEFAULT '',
  container         TEXT NOT NULL DEFAULT 'none',
  action            TEXT NOT NULL DEFAULT '',
  role              TEXT NOT NULL DEFAULT '',
  task_group        TEXT NOT NULL DEFAULT '',
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
  turns              INTEGER
);

CREATE INDEX IF NOT EXISTS idx_execs_started ON agent_execs(started_at);
CREATE INDEX IF NOT EXISTS idx_execs_project ON agent_execs(project_id, started_at);
CREATE INDEX IF NOT EXISTS idx_execs_action ON agent_execs(action, started_at);
CREATE INDEX IF NOT EXISTS idx_execs_task_group ON agent_execs(task_group);
CREATE INDEX IF NOT EXISTS idx_execs_role ON agent_execs(role, started_at);
`

type Call struct {
	ProjectID  string
	Profile    string
	Agent      string
	Container  string
	Action     string
	Role       string
	TaskGroup  string
	Model      string
	PromptHash string
	StartedAt  time.Time
	StreamFile string
	OutputFile string
}

type CallResult struct {
	EndedAt         time.Time
	DurationMS      int64
	ExitCode        int
	IsError         bool
	ErrorMessage    string
	CostUSD         float64
	InputTokens     int
	OutputTokens    int
	CacheReadTokens  int
	CacheWriteTokens int
	Turns            int
	Model            string // if non-empty, updates the model column
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

	// Skip schema creation when old table exists — migrate() will rename it.
	var hasOldTable bool
	_ = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='agent_calls'").Scan(&hasOldTable)
	if !hasOldTable {
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
		}
	}
	tRows.Close()
	if err := tRows.Err(); err != nil {
		return err
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

	return tx.Commit()
}

func (c *CallDB) InsertCall(call *Call) (int64, error) {
	res, err := c.db.Exec(`
		INSERT INTO agent_execs (
			project_id, profile, agent, container, action, role,
			task_group, model, prompt_hash, started_at, stream_file, output_file
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		call.ProjectID, call.Profile, call.Agent, call.Container,
		call.Action, call.Role, call.TaskGroup, call.Model,
		call.PromptHash, call.StartedAt.Format(time.RFC3339), call.StreamFile,
		call.OutputFile,
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
			cache_write_tokens = ?, turns = ?`
	args := []any{
		result.EndedAt.Format(time.RFC3339), result.DurationMS, result.ExitCode,
		isError, result.ErrorMessage, result.CostUSD,
		result.InputTokens, result.OutputTokens, result.CacheReadTokens,
		result.CacheWriteTokens, result.Turns,
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

func (c *CallDB) RawDB() *sql.DB {
	return c.db
}

func (c *CallDB) Close() error {
	return c.db.Close()
}
