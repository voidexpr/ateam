package cmd

import (
	"fmt"
	"time"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/display"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/spf13/cobra"
)

var (
	recentRole    string
	recentAction  string
	recentBatch   string
	recentLimit   int
	recentGitHash bool
)

var runsCmd = &cobra.Command{
	Use:   "ps",
	Short: "Show recent agent runs from the call database",
	Long: `Display summary data about recent runs, with optional filtering
by project, role, or action.

When run inside a project, results are filtered to that project by default.

Example:
  ateam ps
  ateam ps --role project.security
  ateam ps --action report
  ateam ps --project myproject --role test.gaps`,
	Args: cobra.NoArgs,
	RunE: runRuns,
}

func init() {
	runsCmd.Flags().StringVar(&recentRole, "role", "", "filter by role")
	runsCmd.Flags().StringVar(&recentAction, "action", "", "filter by action (report, review, code, exec)")
	runsCmd.Flags().StringVar(&recentBatch, "batch", "", "filter by batch")
	runsCmd.Flags().IntVar(&recentLimit, "limit", 30, "max rows to show")
	runsCmd.Flags().BoolVar(&recentGitHash, "git-hash", false, "append GIT_START and GIT_END columns (first 6 chars of each hash)")
}

func runRuns(cmd *cobra.Command, args []string) error {
	env, err := root.Resolve(orgFlag, projectFlag)
	if err != nil {
		return fmt.Errorf("cannot find project: %w", err)
	}

	db, err := requireProjectDB(env)
	if err != nil {
		return err
	}
	defer db.Close()

	rows, err := db.RecentRuns(calldb.RecentFilter{
		Role:   recentRole,
		Action: recentAction,
		Batch:  recentBatch,
		Limit:  recentLimit,
	})
	if err != nil {
		return fmt.Errorf("query failed: %w", err)
	}

	if len(rows) == 0 {
		fmt.Println("No runs found.")
		return nil
	}

	// CLI prints oldest-first (ASC) for natural reading order.
	// DB returns DESC; reverse for display.
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}

	printRunsTable(rows, recentGitHash)
	return nil
}

func printRunsTable(rows []calldb.RecentRow, showGitHash bool) {
	w := newTable()
	header := "ID\tSTARTED\tPROFILE\tACTION\tROLE\tMODEL\tDURATION\tCOST\tTOKENS\tSTATUS\tBATCH\tREASON"
	if showGitHash {
		header += "\tGIT_START\tGIT_END"
	}
	fmt.Fprintln(w, header)
	for _, r := range rows {
		started := fmtStartedAt(r.StartedAt)

		dur := ""
		if r.DurationMS > 0 {
			dur = runner.FormatDuration(time.Duration(r.DurationMS) * time.Millisecond)
		} else if r.EndedAt == "" {
			if t, err := time.Parse(time.RFC3339, r.StartedAt); err == nil {
				dur = runner.FormatDuration(time.Since(t))
			}
		}

		tokens := ""
		total := int64(r.InputTokens + r.OutputTokens + r.CacheReadTokens + r.CacheWriteTokens)
		if total > 0 {
			tokens = display.FmtTokens(total)
		}

		reason := ""
		if r.IsError {
			reason = runner.Truncate(runner.SingleLineText(r.ErrorMessage), 120)
		}

		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s",
			r.ID, started, r.Profile, r.Action, r.Role, r.Model,
			dur, display.FmtCost(r.CostUSD), tokens, runStatus(r), r.Batch, reason)
		if showGitHash {
			fmt.Fprintf(w, "\t%s\t%s", shortHash(r.GitStartHash), shortHash(r.GitEndHash))
		}
		fmt.Fprintln(w)
	}
	w.Flush()
}

func shortHash(h string) string {
	if len(h) <= 6 {
		return h
	}
	return h[:6]
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

// fmtStartedAt is an alias for display.FmtRFC3339AsTimestamp.
var fmtStartedAt = display.FmtRFC3339AsTimestamp
