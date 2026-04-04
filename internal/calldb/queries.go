package calldb

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type RecentRow struct {
	ID              int64
	ProjectID       string
	Profile         string
	Agent           string
	Action          string
	Role            string
	TaskGroup       string
	Model           string
	StartedAt       string
	EndedAt         string
	DurationMS      int64
	ExitCode        int
	IsError         bool
	CostUSD         float64
	InputTokens     int
	OutputTokens    int
	CacheReadTokens  int
	CacheWriteTokens int
	Turns            int
	PID              int
	ContainerID     string
	StreamFile      string
	OutputFile      string
}

type RecentFilter struct {
	ProjectID string
	Role      string
	Action    string
	TaskGroup string
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
	if f.TaskGroup != "" {
		where = append(where, "task_group = ?")
		args = append(args, f.TaskGroup)
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

const recentCols = "id, project_id, profile, COALESCE(agent,''), action, role, task_group, model, started_at, COALESCE(ended_at,''), COALESCE(duration_ms,0), COALESCE(exit_code,0), is_error, COALESCE(cost_usd,0), COALESCE(input_tokens,0), COALESCE(output_tokens,0), COALESCE(cache_read_tokens,0), COALESCE(cache_write_tokens,0), COALESCE(turns,0), COALESCE(pid,0), COALESCE(container_id,''), COALESCE(stream_file,''), COALESCE(output_file,'')"

func scanRecentRow(rows *sql.Rows) (RecentRow, error) {
	var r RecentRow
	var isErr int
	err := rows.Scan(&r.ID, &r.ProjectID, &r.Profile, &r.Agent, &r.Action, &r.Role, &r.TaskGroup, &r.Model, &r.StartedAt, &r.EndedAt, &r.DurationMS, &r.ExitCode, &isErr, &r.CostUSD, &r.InputTokens, &r.OutputTokens, &r.CacheReadTokens, &r.CacheWriteTokens, &r.Turns, &r.PID, &r.ContainerID, &r.StreamFile, &r.OutputFile)
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
				WHEN action = 'run' AND task_group LIKE 'code-%' THEN 'code-task-run'
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

type TaskGroupRow struct {
	TaskGroup        string
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

// CostByTaskGroup returns cost data grouped by task_group and action for all
// runs that have a non-empty task_group (code sessions, report batches, etc.).
func (c *CallDB) CostByTaskGroup(projectID string) ([]TaskGroupRow, error) {
	q := `
		SELECT
			task_group,
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
		WHERE task_group != ''`
	var args []any
	if projectID != "" {
		q += " AND project_id = ?"
		args = append(args, projectID)
	}
	q += " GROUP BY task_group, action ORDER BY task_group DESC, action"

	rows, err := c.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []TaskGroupRow
	for rows.Next() {
		var r TaskGroupRow
		if err := rows.Scan(&r.TaskGroup, &r.Action, &r.Count, &r.CostUSD, &r.InputTokens, &r.OutputTokens, &r.CacheReadTokens, &r.CacheWriteTokens, &r.TotalTokens, &r.FirstStarted, &r.LastEnded); err != nil {
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
	TaskGroup  string
	StartedAt  string
	EndedAt    string
	StreamFile string
	OutputFile string
}

const callRowCols = `id, COALESCE(agent,''), COALESCE(model,''), role, action, task_group, started_at, COALESCE(ended_at,''), COALESCE(stream_file,''), COALESCE(output_file,'')`

func scanCallRow(rows *sql.Rows) (CallRow, error) {
	var r CallRow
	err := rows.Scan(&r.ID, &r.Agent, &r.Model, &r.Role, &r.Action, &r.TaskGroup, &r.StartedAt, &r.EndedAt, &r.StreamFile, &r.OutputFile)
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

func (c *CallDB) CallsByTaskGroup(taskGroup string) ([]CallRow, error) {
	q := fmt.Sprintf("SELECT %s FROM agent_execs WHERE task_group = ? ORDER BY started_at", callRowCols)
	rows, err := c.db.Query(q, taskGroup)
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
func (c *CallDB) RunCostByActionRole(action, role string) (map[string]RunCost, error) {
	q := `SELECT started_at,
			COALESCE(cost_usd, 0),
			COALESCE(input_tokens, 0) + COALESCE(output_tokens, 0) + COALESCE(cache_read_tokens, 0) + COALESCE(cache_write_tokens, 0)
		FROM agent_execs WHERE action = ? AND role = ?`
	rows, err := c.db.Query(q, action, role)
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

func (c *CallDB) LatestTaskGroup(projectID, prefix string) (string, error) {
	q := `SELECT task_group FROM agent_execs
		WHERE project_id = ? AND task_group LIKE ?
		ORDER BY started_at DESC LIMIT 1`
	var tg string
	err := c.db.QueryRow(q, projectID, prefix+"%").Scan(&tg)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return tg, err
}
