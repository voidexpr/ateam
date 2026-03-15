package calldb

import (
	"database/sql"
	"fmt"
	"strings"
)

type RecentRow struct {
	ID              int64
	ProjectID       string
	Profile         string
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
	CacheReadTokens int
	Turns           int
	PID             int
	ContainerID     string
	StreamFile      string
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

	q := "SELECT id, project_id, profile, action, role, task_group, model, started_at, COALESCE(ended_at,''), COALESCE(duration_ms,0), COALESCE(exit_code,0), is_error, COALESCE(cost_usd,0), COALESCE(input_tokens,0), COALESCE(output_tokens,0), COALESCE(cache_read_tokens,0), COALESCE(turns,0), COALESCE(pid,0), COALESCE(container_id,''), COALESCE(stream_file,'') FROM agent_calls"
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY started_at ASC"

	limit := f.Limit
	if limit <= 0 {
		limit = 30
	}
	q += " LIMIT ?"
	args = append(args, limit)

	rows, err := c.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []RecentRow
	for rows.Next() {
		var r RecentRow
		var isErr int
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.Profile, &r.Action, &r.Role, &r.TaskGroup, &r.Model, &r.StartedAt, &r.EndedAt, &r.DurationMS, &r.ExitCode, &isErr, &r.CostUSD, &r.InputTokens, &r.OutputTokens, &r.CacheReadTokens, &r.Turns, &r.PID, &r.ContainerID, &r.StreamFile); err != nil {
			return results, err
		}
		r.IsError = isErr != 0
		results = append(results, r)
	}
	return results, rows.Err()
}

type RunningRow struct {
	ID          int64
	Role        string
	PID         int
	ContainerID string
	StartedAt   string
}

func (c *CallDB) FindRunning(projectID, action string) ([]RunningRow, error) {
	q := `SELECT id, role, COALESCE(pid,0), COALESCE(container_id,''), started_at
		FROM agent_calls
		WHERE ended_at IS NULL AND project_id = ? AND action = ?`
	rows, err := c.db.Query(q, projectID, action)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []RunningRow
	for rows.Next() {
		var r RunningRow
		if err := rows.Scan(&r.ID, &r.Role, &r.PID, &r.ContainerID, &r.StartedAt); err != nil {
			return results, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

type ActionAgg struct {
	Category        string
	Count           int
	CostUSD         float64
	InputTokens     int64
	OutputTokens    int64
	CacheReadTokens int64
	TotalTokens     int64
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
			COALESCE(SUM(input_tokens), 0) + COALESCE(SUM(output_tokens), 0) + COALESCE(SUM(cache_read_tokens), 0)
		FROM agent_calls`
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
		if err := rows.Scan(&r.Category, &r.Count, &r.CostUSD, &r.InputTokens, &r.OutputTokens, &r.CacheReadTokens, &r.TotalTokens); err != nil {
			return results, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

type TaskGroupRow struct {
	TaskGroup       string
	Action          string
	Count           int
	CostUSD         float64
	InputTokens     int64
	OutputTokens    int64
	CacheReadTokens int64
	TotalTokens     int64
	FirstStarted    string
	LastEnded       sql.NullString
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
			COALESCE(SUM(input_tokens), 0) + COALESCE(SUM(output_tokens), 0) + COALESCE(SUM(cache_read_tokens), 0),
			MIN(started_at),
			MAX(ended_at)
		FROM agent_calls
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
		if err := rows.Scan(&r.TaskGroup, &r.Action, &r.Count, &r.CostUSD, &r.InputTokens, &r.OutputTokens, &r.CacheReadTokens, &r.TotalTokens, &r.FirstStarted, &r.LastEnded); err != nil {
			return results, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

type CallRow struct {
	ID         int64
	Role       string
	Action     string
	TaskGroup  string
	StartedAt  string
	EndedAt    string
	StreamFile string
}

const callRowCols = `id, role, action, task_group, started_at, COALESCE(ended_at,''), COALESCE(stream_file,'')`

func scanCallRow(rows *sql.Rows) (CallRow, error) {
	var r CallRow
	err := rows.Scan(&r.ID, &r.Role, &r.Action, &r.TaskGroup, &r.StartedAt, &r.EndedAt, &r.StreamFile)
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
	q := fmt.Sprintf("SELECT %s FROM agent_calls WHERE id IN (%s) ORDER BY started_at",
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
	q := fmt.Sprintf("SELECT %s FROM agent_calls WHERE task_group = ? ORDER BY started_at", callRowCols)
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

func (c *CallDB) LatestTaskGroup(projectID, prefix string) (string, error) {
	q := `SELECT task_group FROM agent_calls
		WHERE project_id = ? AND task_group LIKE ?
		ORDER BY started_at DESC LIMIT 1`
	var tg string
	err := c.db.QueryRow(q, projectID, prefix+"%").Scan(&tg)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return tg, err
}
