package cmd

import (
	"fmt"
	"time"

	"github.com/ateam-poc/internal/calldb"
	"github.com/ateam-poc/internal/root"
	"github.com/ateam-poc/internal/runner"
	"github.com/spf13/cobra"
)

var (
	recentRole   string
	recentAction string
	recentLimit  int
)

var recentRunsCmd = &cobra.Command{
	Use:   "recent-runs",
	Short: "Show recent agent runs from the call database",
	Long: `Display summary data about recent runs, with optional filtering
by project, role, or action.

When run inside a project, results are filtered to that project by default.

Example:
  ateam recent-runs
  ateam recent-runs --role security
  ateam recent-runs --action report
  ateam recent-runs --project myproject --role testing_basic`,
	Args: cobra.NoArgs,
	RunE: runRecentRuns,
}

func init() {
	recentRunsCmd.Flags().StringVar(&recentRole, "role", "", "filter by role")
	recentRunsCmd.Flags().StringVar(&recentAction, "action", "", "filter by action (report, review, code, run)")
	recentRunsCmd.Flags().IntVar(&recentLimit, "limit", 30, "max rows to show")
}

func runRecentRuns(cmd *cobra.Command, args []string) error {
	env, err := root.Lookup()
	if err != nil {
		return fmt.Errorf("cannot find .ateamorg/: %w", err)
	}

	db := openCallDB(env.OrgDir)
	if db == nil {
		return fmt.Errorf("cannot open call database")
	}
	defer db.Close()

	projectID := ""
	if projectFlag != "" {
		projectID = projectFlag
	} else if env.ProjectDir != "" {
		projectID = env.ProjectID()
	}

	rows, err := db.RecentRuns(calldb.RecentFilter{
		ProjectID: projectID,
		Role:      recentRole,
		Action:    recentAction,
		Limit:     recentLimit,
	})
	if err != nil {
		return fmt.Errorf("query failed: %w", err)
	}

	if len(rows) == 0 {
		fmt.Println("No runs found.")
		return nil
	}

	w := newTable()
	fmt.Fprintln(w, "ID\tSTARTED\tACTION\tROLE\tMODEL\tDURATION\tCOST\tTOKENS\tSTATUS\tTASK_GROUP")
	for _, r := range rows {
		status := "ok"
		if r.IsError {
			status = "error"
		} else if r.EndedAt == "" {
			status = "running"
		}

		started := r.StartedAt
		if t, err := time.Parse(time.RFC3339, r.StartedAt); err == nil {
			started = t.Format(runner.TimestampFormat)
		}

		dur := ""
		if r.DurationMS > 0 {
			dur = runner.FormatDuration(time.Duration(r.DurationMS) * time.Millisecond)
		}

		tokens := ""
		total := r.InputTokens + r.OutputTokens + r.CacheReadTokens
		if total > 0 {
			tokens = fmtTokens(total)
		}

		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.ID, started, r.Action, r.Role, r.Model,
			dur, fmtCost(r.CostUSD), tokens, status, r.TaskGroup)
	}
	w.Flush()

	return nil
}

func fmtTokens(n int) string {
	if n <= 0 {
		return ""
	}
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}
