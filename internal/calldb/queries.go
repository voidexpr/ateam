package calldb

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type RecentRow struct {
	ID                int64
	ProjectID         string
	Profile           string
	Agent             string
	Container         string
	Action            string
	Role              string
	Batch             string
	Model             string
	StartedAt         string
	EndedAt           string
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
	PID               int
	ContainerID       string
	StreamFile        string
	OutputFile        string
	PeakContextTokens int
	ContextWindow     int
}

type RecentFilter struct {
	ProjectID string
	Role      string
	Action    string
	Agent     string   // single agent (legacy); ignored when Agents is set
	Agents    []string // any-of filter; pushed to SQL as `agent IN (...)`
	Batch     string
	Limit     int
}

func (c *CallDB) RecentRuns(f RecentFilter) ([]RecentRow, error) {
	var where []string
	var args []any

	if f.ProjectID != "" {
		where = append(where, "project_id = ?")
		args = append(args, f.ProjectID)
	}
	if f.Role != "" {
		where = append(where, "role = ?")
		args = append(args, f.Role)
	}
	if f.Action != "" {
		where = append(where, "action = ?")
		args = append(args, f.Action)
	}
	if len(f.Agents) > 0 {
		placeholders := strings.Repeat("?,", len(f.Agents))
		placeholders = placeholders[:len(placeholders)-1]
		where = append(where, "agent IN ("+placeholders+")")
		for _, a := range f.Agents {
			args = append(args, a)
		}
	} else if f.Agent != "" {
		where = append(where, "agent = ?")
		args = append(args, f.Agent)
	}
	if f.Batch != "" {
		where = append(where, "batch = ?")
		args = append(args, f.Batch)
	}

	q := "SELECT " + recentCols + " FROM agent_execs"
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY started_at DESC, id DESC"
	if f.Limit >= 0 {
		limit := f.Limit
		if limit == 0 {
			limit = 30
		}
		q += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := c.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []RecentRow
	for rows.Next() {
		r, err := scanRecentRow(rows)
		if err != nil {
			return results, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

const recentCols = "id, project_id, profile, COALESCE(agent,''), COALESCE(container,''), action, role, batch, model, started_at, COALESCE(ended_at,''), COALESCE(duration_ms,0), COALESCE(exit_code,0), is_error, COALESCE(error_message,''), COALESCE(cost_usd,0), COALESCE(input_tokens,0), COALESCE(output_tokens,0), COALESCE(cache_read_tokens,0), COALESCE(cache_write_tokens,0), COALESCE(turns,0), COALESCE(pid,0), COALESCE(container_id,''), COALESCE(stream_file,''), COALESCE(output_file,''), COALESCE(peak_context_tokens,0), COALESCE(context_window,0)"

func scanRecentRow(rows *sql.Rows) (RecentRow, error) {
	var r RecentRow
	var isErr int
	err := rows.Scan(&r.ID, &r.ProjectID, &r.Profile, &r.Agent, &r.Container, &r.Action, &r.Role, &r.Batch, &r.Model, &r.StartedAt, &r.EndedAt, &r.DurationMS, &r.ExitCode, &isErr, &r.ErrorMessage, &r.CostUSD, &r.InputTokens, &r.OutputTokens, &r.CacheReadTokens, &r.CacheWriteTokens, &r.Turns, &r.PID, &r.ContainerID, &r.StreamFile, &r.OutputFile, &r.PeakContextTokens, &r.ContextWindow)
	r.IsError = isErr != 0
	return r, err
}

// GetRunByID returns a single run by ID.
func (c *CallDB) GetRunByID(id int64) (*RecentRow, error) {
	q := "SELECT " + recentCols + " FROM agent_execs WHERE id = ?"
	rows, err := c.db.Query(q, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, nil
	}
	r, err := scanRecentRow(rows)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

type RunningRow struct {
	ID          int64
	Role        string
	Action      string
	PID         int
	ContainerID string
	StartedAt   string
}

// FindRunning returns calls with no ended_at for the given project.
// If action is empty, all actions are returned.
func (c *CallDB) FindRunning(projectID, action string) ([]RunningRow, error) {
	q := `SELECT id, role, action, COALESCE(pid,0), COALESCE(container_id,''), started_at
		FROM agent_execs
		WHERE ended_at IS NULL AND project_id = ?`
	args := []any{projectID}
	if action != "" {
		q += ` AND action = ?`
		args = append(args, action)
	}
	rows, err := c.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []RunningRow
	for rows.Next() {
		var r RunningRow
		if err := rows.Scan(&r.ID, &r.Role, &r.Action, &r.PID, &r.ContainerID, &r.StartedAt); err != nil {
			return results, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

type ActionAgg struct {
	Category         string
	Count            int
	CostUSD          float64
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	TotalTokens      int64
}

func (c *CallDB) CostByAction(projectID string) ([]ActionAgg, error) {
	q := `
		SELECT
			CASE
				WHEN action = 'run' AND batch LIKE 'code-%' THEN 'code-task-run'
				ELSE action
			END AS category,
			COUNT(*) AS cnt,
			COALESCE(SUM(cost_usd), 0),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(cache_read_tokens), 0),
			COALESCE(SUM(cache_write_tokens), 0),
			COALESCE(SUM(input_tokens), 0) + COALESCE(SUM(output_tokens), 0) + COALESCE(SUM(cache_read_tokens), 0) + COALESCE(SUM(cache_write_tokens), 0)
		FROM agent_execs`
	var args []any
	if projectID != "" {
		q += " WHERE project_id = ?"
		args = append(args, projectID)
	}
	q += " GROUP BY category ORDER BY COALESCE(SUM(cost_usd), 0) DESC"

	rows, err := c.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []ActionAgg
	for rows.Next() {
		var r ActionAgg
		if err := rows.Scan(&r.Category, &r.Count, &r.CostUSD, &r.InputTokens, &r.OutputTokens, &r.CacheReadTokens, &r.CacheWriteTokens, &r.TotalTokens); err != nil {
			return results, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

type BatchRow struct {
	Batch            string
	Action           string
	Count            int
	CostUSD          float64
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	TotalTokens      int64
	FirstStarted     string
	LastEnded        sql.NullString
}

// CostByBatch returns cost data grouped by batch and action for all
// agent_execs that have a non-empty batch (code sessions, report batches, etc.).
func (c *CallDB) CostByBatch(projectID string) ([]BatchRow, error) {
	q := `
		SELECT
			batch,
			action,
			COUNT(*),
			COALESCE(SUM(cost_usd), 0),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(cache_read_tokens), 0),
			COALESCE(SUM(cache_write_tokens), 0),
			COALESCE(SUM(input_tokens), 0) + COALESCE(SUM(output_tokens), 0) + COALESCE(SUM(cache_read_tokens), 0) + COALESCE(SUM(cache_write_tokens), 0),
			MIN(started_at),
			MAX(ended_at)
		FROM agent_execs
		WHERE batch != ''`
	var args []any
	if projectID != "" {
		q += " AND project_id = ?"
		args = append(args, projectID)
	}
	q += " GROUP BY batch, action ORDER BY batch DESC, action"

	rows, err := c.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []BatchRow
	for rows.Next() {
		var r BatchRow
		if err := rows.Scan(&r.Batch, &r.Action, &r.Count, &r.CostUSD, &r.InputTokens, &r.OutputTokens, &r.CacheReadTokens, &r.CacheWriteTokens, &r.TotalTokens, &r.FirstStarted, &r.LastEnded); err != nil {
			return results, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

type CallRow struct {
	ID         int64
	Agent      string
	Model      string
	Role       string
	Action     string
	Batch      string
	StartedAt  string
	EndedAt    string
	StreamFile string
	OutputFile string
}

const callRowCols = `id, COALESCE(agent,''), COALESCE(model,''), role, action, batch, started_at, COALESCE(ended_at,''), COALESCE(stream_file,''), COALESCE(output_file,'')`

func scanCallRow(rows *sql.Rows) (CallRow, error) {
	var r CallRow
	err := rows.Scan(&r.ID, &r.Agent, &r.Model, &r.Role, &r.Action, &r.Batch, &r.StartedAt, &r.EndedAt, &r.StreamFile, &r.OutputFile)
	return r, err
}

func (c *CallDB) CallsByIDs(ids []int64) ([]CallRow, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	q := fmt.Sprintf("SELECT %s FROM agent_execs WHERE id IN (%s) ORDER BY started_at",
		callRowCols, strings.Join(placeholders, ","))

	rows, err := c.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []CallRow
	for rows.Next() {
		r, err := scanCallRow(rows)
		if err != nil {
			return results, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

func (c *CallDB) CallsByBatch(batch string) ([]CallRow, error) {
	q := fmt.Sprintf("SELECT %s FROM agent_execs WHERE batch = ? ORDER BY started_at", callRowCols)
	rows, err := c.db.Query(q, batch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []CallRow
	for rows.Next() {
		r, err := scanCallRow(rows)
		if err != nil {
			return results, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// RenameProject updates all rows with oldID to newID, rewriting both the
// project_id column and the stream_file prefix. Returns rows affected.
func (c *CallDB) RenameProject(oldID, newID string) (int64, error) {
	tx, err := c.db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec("UPDATE agent_execs SET project_id = ? WHERE project_id = ?", newID, oldID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()

	oldPrefix := "projects/" + oldID + "/"
	newPrefix := "projects/" + newID + "/"
	if _, err := tx.Exec(
		"UPDATE agent_execs SET stream_file = REPLACE(stream_file, ?, ?) WHERE stream_file LIKE ?",
		oldPrefix, newPrefix, oldPrefix+"%",
	); err != nil {
		return 0, err
	}

	return n, tx.Commit()
}

// RunCost holds cost and token data for a single run.
type RunCost struct {
	CostUSD     float64
	TotalTokens int64
}

// RunCostByActionRole returns cost/token data for all runs matching the given
// action and role, keyed by started_at formatted as runner.TimestampFormat.
// When projectID is non-empty, results are scoped to that project only.
func (c *CallDB) RunCostByActionRole(action, role, projectID string) (map[string]RunCost, error) {
	q := `SELECT started_at,
			COALESCE(cost_usd, 0),
			COALESCE(input_tokens, 0) + COALESCE(output_tokens, 0) + COALESCE(cache_read_tokens, 0) + COALESCE(cache_write_tokens, 0)
		FROM agent_execs WHERE action = ? AND role = ?`
	args := []any{action, role}
	if projectID != "" {
		q += " AND project_id = ?"
		args = append(args, projectID)
	}
	rows, err := c.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := map[string]RunCost{}
	for rows.Next() {
		var startedAt string
		var rc RunCost
		if err := rows.Scan(&startedAt, &rc.CostUSD, &rc.TotalTokens); err != nil {
			return result, err
		}
		t, err := time.Parse(time.RFC3339, startedAt)
		if err != nil {
			continue
		}
		key := t.Format("2006-01-02_15-04-05")
		result[key] = rc
	}
	return result, rows.Err()
}

func (c *CallDB) LatestBatch(projectID, prefix string) (string, error) {
	q := `SELECT batch FROM agent_execs
		WHERE project_id = ? AND batch LIKE ?
		ORDER BY started_at DESC LIMIT 1`
	var batch string
	err := c.db.QueryRow(q, projectID, prefix+"%").Scan(&batch)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return batch, err
}
