package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/display"
	"github.com/ateam/internal/runner"
	"github.com/spf13/cobra"
)

var (
	psRole      string
	psAction    string
	psBatch     string
	psWorkDir   string
	psPwd       bool
	psLimit     int
	psGitHash   bool
	psGitBranch bool
)

var psCmd = &cobra.Command{
	Use:   "ps",
	Short: "Show recent agent runs from the call database",
	Long: `Display summary data about recent runs, with optional filtering
by project, role, or action.

When run inside a project, results are filtered to that project by default.

Example:
  ateam ps
  ateam ps --role project.security
  ateam ps --action report
  ateam ps --project myproject --role test.gaps
  ateam ps --work-dir /Users/me/projects/foo
  ateam ps --pwd                                # only runs that happened in $(pwd)`,
	Args: cobra.NoArgs,
	RunE: runPs,
}

func init() {
	psCmd.Flags().StringVar(&psRole, "role", "", "filter by role")
	psCmd.Flags().StringVar(&psAction, "action", "", "filter by action (report, review, code, exec)")
	psCmd.Flags().StringVar(&psBatch, "batch", "", "filter by batch")
	psCmd.Flags().StringVar(&psWorkDir, "work-dir", "", "filter by the agent's working directory (absolute path; resolved against cwd)")
	psCmd.Flags().BoolVar(&psPwd, "pwd", false, "shortcut for --work-dir $(pwd)")
	psCmd.Flags().IntVar(&psLimit, "limit", 30, "max rows to show")
	psCmd.Flags().BoolVar(&psGitHash, "git-hash", false, "append GIT_START and GIT_END columns (first 6 chars of each hash)")
	psCmd.Flags().BoolVar(&psGitBranch, "git-branch", false, "append GIT_START_BRANCH and GIT_END_BRANCH columns")
}

func runPs(cmd *cobra.Command, args []string) error {
	env, err := lookupEnv()
	if err != nil {
		return err
	}

	db, err := requireStateDB(env)
	if err != nil {
		return err
	}
	defer db.Close()

	workDirFilter, err := resolvePsWorkDirFilter()
	if err != nil {
		return err
	}

	rows, err := db.RecentRuns(calldb.RecentFilter{
		Role:    psRole,
		Action:  psAction,
		Batch:   psBatch,
		WorkDir: workDirFilter,
		Limit:   psLimit,
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

	printRunsTable(rows, psGitHash, psGitBranch)
	return nil
}

func printRunsTable(rows []calldb.RecentRow, showGitHash, showGitBranch bool) {
	w := newTable()
	header := "ID\tSTARTED\tPROFILE\tACTION\tROLE\tMODEL\tDURATION\tCOST\tTOKENS\tTURNS\tSTATUS\tBATCH\tREASON"
	if showGitHash {
		header += "\tGIT_START\tGIT_END"
	}
	if showGitBranch {
		header += "\tGIT_START_BRANCH\tGIT_END_BRANCH"
	}
	fmt.Fprintln(w, header)
	for _, r := range rows {
		started := display.FmtRFC3339Compact(r.StartedAt)

		dur := ""
		if r.DurationMS > 0 {
			dur = display.FormatDuration(time.Duration(r.DurationMS) * time.Millisecond)
		} else if r.EndedAt == "" {
			if t, err := time.Parse(time.RFC3339, r.StartedAt); err == nil {
				dur = display.FormatDuration(time.Since(t))
			}
		}

		tokens := ""
		total := int64(r.InputTokens + r.OutputTokens + r.CacheReadTokens + r.CacheWriteTokens)
		if total > 0 {
			tokens = display.FmtTokens(total)
		}

		reason := ""
		if r.IsError {
			reason = display.Truncate(runner.SingleLineText(r.ErrorMessage), 120)
		}

		turns := ""
		if r.Turns > 0 {
			turns = strconv.Itoa(r.Turns)
		}

		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s",
			r.ID, started, r.Profile, r.Action, r.Role, r.Model,
			dur, display.FmtCost(r.CostUSD), tokens, turns, runStatus(r), r.Batch, reason)
		if showGitHash {
			fmt.Fprintf(w, "\t%s\t%s", shortHash(r.GitStartHash), shortHash(r.GitEndHash))
		}
		if showGitBranch {
			fmt.Fprintf(w, "\t%s\t%s", r.GitStartBranch, r.GitEndBranch)
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

// resolvePsWorkDirFilter combines --work-dir and --pwd into a single
// absolute-path filter. --pwd alone resolves to os.Getwd(); --work-dir
// (relative or absolute) resolves against the process cwd. Setting both is
// an error: they would always agree only when --work-dir equals cwd.
//
// The path is passed through filepath.EvalSymlinks so it matches the
// symlink-resolved value stored on agent_execs.work_dir (root.realPath
// canonicalizes the runner's WorkDir at resolution time, e.g. /tmp →
// /private/tmp on macOS). Without this, --pwd from /tmp/foo would never
// match a row stored as /private/tmp/foo.
func resolvePsWorkDirFilter() (string, error) {
	if psPwd && psWorkDir != "" {
		return "", fmt.Errorf("--pwd and --work-dir are mutually exclusive")
	}
	var raw string
	switch {
	case psPwd:
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("cannot read cwd for --pwd: %w", err)
		}
		raw = cwd
	case psWorkDir != "":
		abs, err := filepath.Abs(psWorkDir)
		if err != nil {
			return "", fmt.Errorf("cannot resolve --work-dir %q: %w", psWorkDir, err)
		}
		raw = abs
	default:
		return "", nil
	}
	if resolved, err := filepath.EvalSymlinks(raw); err == nil {
		return resolved, nil
	}
	return raw, nil
}
