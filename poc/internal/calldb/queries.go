package calldb

import (
	"database/sql"
	"strings"
)

type RecentRow struct {
	ID              int64
	ProjectID       string
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
}

type RecentFilter struct {
	ProjectID string
	Role      string
	Action    string
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

	q := "SELECT id, project_id, action, role, task_group, model, started_at, COALESCE(ended_at,''), COALESCE(duration_ms,0), COALESCE(exit_code,0), is_error, COALESCE(cost_usd,0), COALESCE(input_tokens,0), COALESCE(output_tokens,0), COALESCE(cache_read_tokens,0), COALESCE(turns,0) FROM agent_calls"
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
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.Action, &r.Role, &r.TaskGroup, &r.Model, &r.StartedAt, &r.EndedAt, &r.DurationMS, &r.ExitCode, &isErr, &r.CostUSD, &r.InputTokens, &r.OutputTokens, &r.CacheReadTokens, &r.Turns); err != nil {
			return results, err
		}
		r.IsError = isErr != 0
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

type CodeSessionRow struct {
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

func (c *CallDB) CostByCodeSession(projectID string) ([]CodeSessionRow, error) {
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
		WHERE task_group LIKE 'code-%'`
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

	var results []CodeSessionRow
	for rows.Next() {
		var r CodeSessionRow
		if err := rows.Scan(&r.TaskGroup, &r.Action, &r.Count, &r.CostUSD, &r.InputTokens, &r.OutputTokens, &r.CacheReadTokens, &r.TotalTokens, &r.FirstStarted, &r.LastEnded); err != nil {
			return results, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}
