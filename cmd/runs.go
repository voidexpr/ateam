package cmd

import (
	"fmt"
	"time"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/spf13/cobra"
)

var (
	recentRole   string
	recentAction string
	recentLimit  int
)

var runsCmd = &cobra.Command{
	Use:   "ps",
	Short: "Show recent agent runs from the call database",
	Long: `Display summary data about recent runs, with optional filtering
by project, role, or action.

When run inside a project, results are filtered to that project by default.

Example:
  ateam ps
  ateam ps --role security
  ateam ps --action report
  ateam ps --project myproject --role testing_basic`,
	Args: cobra.NoArgs,
	RunE: runRuns,
}

func init() {
	runsCmd.Flags().StringVar(&recentRole, "role", "", "filter by role")
	runsCmd.Flags().StringVar(&recentAction, "action", "", "filter by action (report, review, code, run)")
	runsCmd.Flags().IntVar(&recentLimit, "limit", 30, "max rows to show")
}

func runRuns(cmd *cobra.Command, args []string) error {
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
	fmt.Fprintln(w, "ID\tSTARTED\tPROFILE\tACTION\tROLE\tMODEL\tDURATION\tCOST\tTOKENS\tSTATUS\tTASK_GROUP")
	for _, r := range rows {
		status := runStatus(r)

		started := r.StartedAt
		if t, err := time.Parse(time.RFC3339, r.StartedAt); err == nil {
			started = t.Format(runner.TimestampFormat)
		}

		dur := ""
		if r.DurationMS > 0 {
			dur = runner.FormatDuration(time.Duration(r.DurationMS) * time.Millisecond)
		} else if r.EndedAt == "" {
			if t, err := time.Parse(time.RFC3339, r.StartedAt); err == nil {
				dur = runner.FormatDuration(time.Since(t))
			}
		}

		tokens := ""
		total := int64(r.InputTokens + r.OutputTokens + r.CacheReadTokens)
		if total > 0 {
			tokens = fmtTokens(total)
		}

		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.ID, started, r.Profile, r.Action, r.Role, r.Model,
			dur, fmtCost(r.CostUSD), tokens, status, r.TaskGroup)
	}
	w.Flush()

	return nil
}

func runStatus(r calldb.RecentRow) string {
	if r.IsError {
		return "error"
	}
	if r.EndedAt != "" {
		return "ok"
	}
	if r.PID > 0 && isProcessAlive(r.PID) {
		if r.ContainerID != "" {
			return "running (docker)"
		}
		return fmt.Sprintf("running (%d)", r.PID)
	}
	return "canceled"
}

func fmtTokens(n int64) string {
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
