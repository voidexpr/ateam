package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ateam/internal/display"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/spf13/cobra"
)

var (
	reportRoles                []string
	reportExtraPrompt          string
	reportTimeout              int
	reportParallel             int
	reportPrint                bool
	reportDryRun               bool
	reportIgnorePreviousReport bool
	reportCheaperModel         bool
	reportProfile              string
	reportAgent                string
	reportVerbose              bool
	reportForce                bool
	reportReview               bool
)

var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "Run roles to produce analysis reports",
	Long: `Run one or more roles in parallel to analyze the project source code
and produce markdown reports. Defaults to all enabled roles.

Works from any project directory — discovers the .ateamorg/ and .ateam/ structure.

Example:
  ateam report
  ateam report --roles testing_basic,security
  ateam report --roles refactor_small --extra-prompt "Focus on the auth module"
  ateam report --extra-prompt @notes.md`,
	RunE: runReport,
}

func init() {
	reportCmd.Flags().StringSliceVar(&reportRoles, "roles", nil, prompts.RoleFlagUsage()+" (default: all)")
	reportCmd.Flags().StringVar(&reportExtraPrompt, "extra-prompt", "", "additional instructions (text or @filepath)")
	reportCmd.Flags().IntVar(&reportTimeout, "timeout", 0, "timeout in minutes per role (overrides config)")
	reportCmd.Flags().IntVar(&reportParallel, "parallel", 0, "max parallel roles (overrides config max_parallel)")
	reportCmd.Flags().BoolVar(&reportPrint, "print", false, "print reports to stdout after completion")
	reportCmd.Flags().BoolVar(&reportReview, "review", false, "run review automatically after reports complete")
	reportCmd.Flags().BoolVar(&reportDryRun, "dry-run", false, "print the computed prompt for each role without running")
	reportCmd.Flags().BoolVar(&reportIgnorePreviousReport, "ignore-previous-report", false, "do not include the role's previous report in the prompt")
	addCheaperModelFlag(reportCmd, &reportCheaperModel)
	addProfileFlags(reportCmd, &reportProfile, &reportAgent)
	addVerboseFlag(reportCmd, &reportVerbose)
	addForceFlag(reportCmd, &reportForce)
}

func runReport(cmd *cobra.Command, args []string) error {
	env, err := root.Resolve(orgFlag, projectFlag)
	if err != nil {
		return err
	}

	roles := reportRoles
	if len(roles) == 0 {
		roles = []string{"all"}
	}
	roleIDs, err := prompts.ResolveRoleList(roles, env.Config.Roles, env.ProjectDir, env.OrgDir)
	if err != nil {
		return err
	}

	if err := root.EnsureRoles(env.ProjectDir, roleIDs); err != nil {
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

	db := openProjectDB(env)
	if db != nil {
		defer db.Close()
		cr.CallDB = db
	}

	if !reportForce {
		if err := checkConcurrentRuns(db, "", runner.ActionReport, roleIDs); err != nil {
			return err
		}
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
		roleDir := env.RoleDir(roleID)
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

	maxParallel := env.Config.Report.EffectiveMaxParallel(reportParallel)

	reportStart := time.Now()

	fmt.Printf("Running %d role(s) (max %d parallel, %dm timeout)...\n\n",
		len(tasks), maxParallel, timeout)

	cwd, _ := os.Getwd()

	// Build role order index for status display
	roleIndex := make(map[string]int, len(tasks))
	statuses := make([]string, len(tasks))
	for i, t := range tasks {
		roleIndex[t.RoleID] = i
		statuses[i] = fmt.Sprintf("  %-25s queued", t.RoleID)
	}
	printStatuses(statuses)

	// Process completions and progress as they arrive
	completed := make(chan runner.RunSummary, len(tasks))
	progress := make(chan runner.RunProgress, 64)
	var succeeded, failed int
	var results []runner.RunSummary
	var statusMu sync.Mutex

	ctx, stop := cmdContext()
	defer stop()
	go func() {
		runner.RunPool(ctx, cr, tasks, maxParallel, progress, completed)
		close(progress)
	}()

	agentName := cr.Agent.Name()

	// Consume progress events to update in-flight status lines.
	// Rate-limit terminal redraws to avoid excessive output with many parallel roles.
	var progressDone sync.WaitGroup
	progressDone.Add(1)
	go func() {
		defer progressDone.Done()
		var lastRedraw time.Time
		for p := range progress {
			idx, ok := roleIndex[p.RoleID]
			if !ok {
				continue
			}
			elapsed := runner.FormatDuration(p.Elapsed)
			statusMu.Lock()
			switch p.Phase {
			case runner.PhaseInit:
				statuses[idx] = fmt.Sprintf("  %-25s running  %s", p.RoleID, elapsed)
			case runner.PhaseTool:
				statuses[idx] = fmt.Sprintf("  %-25s running  %d calls  %s  %s", p.RoleID, p.ToolCount, elapsed, p.ToolName)
			default:
				statuses[idx] = fmt.Sprintf("  %-25s running  %d calls  %s", p.RoleID, p.ToolCount, elapsed)
			}
			if time.Since(lastRedraw) >= 500*time.Millisecond {
				reprintStatuses(statuses)
				lastRedraw = time.Now()
			}
			statusMu.Unlock()
		}
	}()

	for r := range completed {
		elapsed := runner.FormatDuration(r.Duration)
		endedAt := r.EndedAt.Format("15:04:05")
		tokens := display.FmtTokens(int64(r.InputTokens + r.OutputTokens + r.CacheReadTokens))

		statusMu.Lock()
		idx := roleIndex[r.RoleID]
		if r.Err != nil {
			logsRef := streamFilePrefix(r.StreamFilePath, cwd)
			statuses[idx] = fmt.Sprintf("  %-25s ERROR    %s  %s  %s  %s", r.RoleID, endedAt, elapsed, tokens, logsRef)
			failed++
		} else {
			reportPath := env.RoleReportPath(r.RoleID)
			historyDir := env.RoleHistoryDir(r.RoleID)
			if err := runner.ArchiveFile(reportPath, historyDir, prompts.ReportFile, r.StartedAt); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not archive report for %s: %v\n", r.RoleID, err)
			}
			statuses[idx] = fmt.Sprintf("  %-25s done     %s  %s  %s  %s", r.RoleID, endedAt, elapsed, tokens, relPath(cwd, reportPath))
			succeeded++
		}
		reprintStatuses(statuses)
		statusMu.Unlock()
		results = append(results, r)
	}
	progressDone.Wait()

	fmt.Printf("\n%d succeeded, %d failed (%s)\n", succeeded, failed, runner.FormatDuration(time.Since(reportStart)))

	if failed > 0 {
		for _, r := range results {
			if r.Err == nil {
				continue
			}
			tail := runner.StreamTailError(r.StreamFilePath, agentName, 5)
			if tail == "" {
				continue
			}
			fmt.Printf("\n  %s:\n", r.RoleID)
			for _, line := range strings.Split(tail, "\n") {
				fmt.Printf("        %s\n", line)
			}
		}
	}

	if reportPrint && succeeded > 0 {
		for _, r := range results {
			if r.Err != nil {
				continue
			}
			fmt.Printf("\n══════ %s ══════\n\n%s\n", r.RoleID, r.Output)
		}
	}

	if failed > 0 {
		return fmt.Errorf("%d role(s) failed", failed)
	}

	if reportReview && succeeded > 0 {
		fmt.Println()
		return runReview(nil, nil)
	}

	if succeeded > 0 {
		fmt.Printf("\nRun 'ateam review' to have the supervisor synthesize findings.\n")
	}

	return nil
}

// streamFilePrefix returns the log file prefix (without _stream.jsonl suffix)
// relative to cwd, with a trailing "*" glob hint.
func streamFilePrefix(streamPath, cwd string) string {
	// Stream files are named <prefix>_stream.jsonl — strip the suffix.
	prefix := strings.TrimSuffix(streamPath, "_stream.jsonl")
	rel := relPath(cwd, prefix)
	return rel + "*"
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
