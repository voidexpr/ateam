package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ateam-poc/internal/prompts"
	"github.com/ateam-poc/internal/root"
	"github.com/ateam-poc/internal/runner"
	"github.com/spf13/cobra"
)

var (
	reportRoles                []string
	reportExtraPrompt          string
	reportTimeout              int
	reportPrint                bool
	reportDryRun               bool
	reportIgnorePreviousReport bool
	reportCheaperModel         bool
	reportProfile              string
	reportAgent                string
	reportVerbose              bool
)

var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "Run roles to produce analysis reports",
	Long: `Run one or more roles in parallel to analyze the project source code
and produce markdown reports.

Works from any project directory — discovers the .ateamorg/ and .ateam/ structure.

Example:
  ateam report --roles all
  ateam report --roles testing_basic,security
  ateam report --roles refactor_small --extra-prompt "Focus on the auth module"
  ateam report --roles all --extra-prompt @notes.md`,
	RunE: runReport,
}

func init() {
	reportCmd.Flags().StringSliceVar(&reportRoles, "roles", nil, prompts.RoleFlagUsage()+" (required)")
	reportCmd.Flags().StringVar(&reportExtraPrompt, "extra-prompt", "", "additional instructions (text or @filepath)")
	reportCmd.Flags().IntVar(&reportTimeout, "timeout", 0, "timeout in minutes per role (overrides config)")
	reportCmd.Flags().BoolVar(&reportPrint, "print", false, "print reports to stdout after completion")
	reportCmd.Flags().BoolVar(&reportDryRun, "dry-run", false, "print the computed prompt for each role without running")
	reportCmd.Flags().BoolVar(&reportIgnorePreviousReport, "ignore-previous-report", false, "do not include the role's previous report in the prompt")
	addCheaperModelFlag(reportCmd, &reportCheaperModel)
	addProfileFlags(reportCmd, &reportProfile, &reportAgent)
	addVerboseFlag(reportCmd, &reportVerbose)
	_ = reportCmd.MarkFlagRequired("roles")
}

func runReport(cmd *cobra.Command, args []string) error {
	env, err := root.Resolve(orgFlag, projectFlag)
	if err != nil {
		return err
	}

	roleIDs, err := prompts.ResolveRoleList(reportRoles, env.Config.Roles)
	if err != nil {
		return err
	}

	if err := root.EnsureRoles(env.ProjectDir, env.StateDir, roleIDs); err != nil {
		return err
	}

	extraPrompt, err := prompts.ResolveOptional(reportExtraPrompt)
	if err != nil {
		return err
	}

	timeout := env.Config.Report.EffectiveTimeout(reportTimeout)

	cr, err := resolveRunner(env, reportProfile, reportAgent, runner.ActionReport, "")
	if err != nil {
		return err
	}
	applyCheaperModel(cr, reportCheaperModel)

	db := openCallDB(env.OrgDir)
	if db != nil {
		defer db.Close()
		cr.CallDB = db
	}

	taskGroup := "report-" + time.Now().Format(runner.TimestampFormat)

	basePinfo := env.NewProjectInfoParams("")
	var tasks []runner.PoolTask
	for _, roleID := range roleIDs {
		pinfo := basePinfo
		pinfo.Role = "role " + roleID
		prompt, err := prompts.AssembleRolePrompt(env.OrgDir, env.ProjectDir, roleID, env.SourceDir, extraPrompt, pinfo, reportIgnorePreviousReport)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: skipping %s — %v\n", roleID, err)
			continue
		}
		roleDir := filepath.Join(env.ProjectDir, "roles", roleID)
		tasks = append(tasks, runner.PoolTask{
			Prompt: prompt,
			RunOpts: runner.RunOpts{
				RoleID:               roleID,
				Action:               runner.ActionReport,
				LogsDir:              env.RoleLogsDir(roleID),
				LastMessageFilePath:  env.RoleReportPath(roleID),
				ErrorMessageFilePath: filepath.Join(roleDir, prompts.ReportErrorFile),
				WorkDir:              env.SourceDir,
				TimeoutMin:           timeout,
				HistoryDir:           env.RoleHistoryDir(roleID),
				PromptName:           "report_prompt.md",
				Verbose:              reportVerbose,
				TaskGroup:            taskGroup,
			},
		})
	}

	if len(tasks) == 0 {
		return fmt.Errorf("no valid roles to run")
	}

	if reportDryRun {
		for i, t := range tasks {
			if i > 0 {
				fmt.Println()
			}
			fmt.Printf("╔══ %s ══╗\n\n", t.RoleID)
			fmt.Println(t.Prompt)
			fmt.Printf("\n╚══ %s ══╝\n", t.RoleID)
		}
		return nil
	}

	fmt.Printf("Running %d role(s) (max %d parallel, %dm timeout)...\n\n",
		len(tasks), env.Config.Report.MaxParallel, timeout)

	cwd, _ := os.Getwd()

	// Build role order index for status display
	roleIndex := make(map[string]int, len(tasks))
	statuses := make([]string, len(tasks))
	for i, t := range tasks {
		roleIndex[t.RoleID] = i
		statuses[i] = fmt.Sprintf("  %-25s queued", t.RoleID)
	}
	printStatuses(statuses)

	// Process completions as they arrive
	completed := make(chan runner.RunSummary, len(tasks))
	var succeeded, failed int
	var results []runner.RunSummary

	ctx := context.Background()
	go func() {
		runner.RunPool(ctx, cr, tasks, env.Config.Report.MaxParallel, nil, completed)
	}()

	for r := range completed {
		elapsed := runner.FormatDuration(r.Duration)
		endedAt := r.EndedAt.Format("15:04:05")

		idx := roleIndex[r.RoleID]
		if r.Err != nil {
			statuses[idx] = fmt.Sprintf("  %-25s ERROR    %s  %s", r.RoleID, endedAt, elapsed)
			failed++
		} else {
			reportPath := env.RoleReportPath(r.RoleID)
			historyDir := env.RoleHistoryDir(r.RoleID)
			if err := runner.ArchiveFile(reportPath, historyDir, prompts.ReportFile); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not archive report for %s: %v\n", r.RoleID, err)
			}
			statuses[idx] = fmt.Sprintf("  %-25s done     %s  %s  %s", r.RoleID, endedAt, elapsed, relPath(cwd, reportPath))
			succeeded++
		}
		reprintStatuses(statuses)
		results = append(results, r)
	}

	fmt.Printf("\n%d succeeded, %d failed\n", succeeded, failed)
	if failed == 0 && succeeded > 0 {
		fmt.Printf("\nRun 'ateam review' to have the supervisor synthesize findings.\n")
	}

	if reportPrint && succeeded > 0 {
		for _, r := range results {
			if r.Err != nil {
				continue
			}
			fmt.Printf("\n══════ %s ══════\n\n%s\n", r.RoleID, r.Output)
		}
	}

	return nil
}

// printStatuses prints all status lines.
func printStatuses(statuses []string) {
	for _, s := range statuses {
		fmt.Println(s)
	}
}

// reprintStatuses moves the cursor up, reprints all lines, clearing previous content.
func reprintStatuses(statuses []string) {
	// Move cursor up len(statuses) lines and overwrite
	fmt.Printf("\033[%dA", len(statuses))
	for _, s := range statuses {
		fmt.Printf("\033[2K%s\n", s)
	}
}
