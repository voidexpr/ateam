package calldb

import (
	"database/sql"
	"fmt"
	"os"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS agent_calls (
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
  ended_at          TEXT,
  duration_ms       INTEGER,
  exit_code         INTEGER,
  is_error          INTEGER NOT NULL DEFAULT 0,
  error_message     TEXT NOT NULL DEFAULT '',
  cost_usd          REAL,
  input_tokens      INTEGER,
  output_tokens     INTEGER,
  cache_read_tokens INTEGER,
  turns             INTEGER
);

CREATE INDEX IF NOT EXISTS idx_calls_started ON agent_calls(started_at);
CREATE INDEX IF NOT EXISTS idx_calls_project ON agent_calls(project_id, started_at);
CREATE INDEX IF NOT EXISTS idx_calls_action ON agent_calls(action, started_at);
CREATE INDEX IF NOT EXISTS idx_calls_task_group ON agent_calls(task_group);
CREATE INDEX IF NOT EXISTS idx_calls_role ON agent_calls(role, started_at);
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
	CacheReadTokens int
	Turns           int
}

type CallDB struct {
	db *sql.DB
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

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}

	return &CallDB{db: db}, nil
}

func (c *CallDB) InsertCall(call *Call) (int64, error) {
	res, err := c.db.Exec(`
		INSERT INTO agent_calls (
			project_id, profile, agent, container, action, role,
			task_group, model, prompt_hash, started_at, stream_file
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		call.ProjectID, call.Profile, call.Agent, call.Container,
		call.Action, call.Role, call.TaskGroup, call.Model,
		call.PromptHash, call.StartedAt.Format(time.RFC3339), call.StreamFile,
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
	_, err := c.db.Exec(`
		UPDATE agent_calls SET
			ended_at = ?, duration_ms = ?, exit_code = ?,
			is_error = ?, error_message = ?, cost_usd = ?,
			input_tokens = ?, output_tokens = ?, cache_read_tokens = ?,
			turns = ?
		WHERE id = ?`,
		result.EndedAt.Format(time.RFC3339), result.DurationMS, result.ExitCode,
		isError, result.ErrorMessage, result.CostUSD,
		result.InputTokens, result.OutputTokens, result.CacheReadTokens,
		result.Turns, id,
	)
	return err
}

func (c *CallDB) Close() error {
	return c.db.Close()
}
