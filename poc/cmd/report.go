package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ateam-poc/internal/prompts"
	"github.com/ateam-poc/internal/root"
	"github.com/ateam-poc/internal/runner"
	"github.com/spf13/cobra"
)

var (
	reportAgents               []string
	reportExtraPrompt          string
	reportTimeout              int
	reportDelta                bool
	reportPrint                bool
	reportDryRun               bool
	reportIgnorePreviousReport bool
	reportCheaperModel         bool
)

var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "Run agents to produce analysis reports",
	Long: `Run one or more agents in parallel to analyze the project source code
and produce markdown reports.

Works from any project directory — discovers the .ateamorg/ and .ateam/ structure.

Example:
  ateam report --agents all
  ateam report --agents testing_basic,security
  ateam report --agents refactor_small --extra-prompt "Focus on the auth module"
  ateam report --agents all --extra-prompt @notes.md`,
	RunE: runReport,
}

func init() {
	reportCmd.Flags().StringSliceVar(&reportAgents, "agents", nil, prompts.AgentFlagUsage()+" (required)")
	reportCmd.Flags().StringVar(&reportExtraPrompt, "extra-prompt", "", "additional instructions (text or @filepath)")
	reportCmd.Flags().IntVar(&reportTimeout, "timeout", 0, "timeout in minutes per agent (overrides config)")
	reportCmd.Flags().BoolVar(&reportDelta, "delta", false, "produce delta report (not yet implemented)")
	reportCmd.Flags().BoolVar(&reportPrint, "print", false, "print reports to stdout after completion")
	reportCmd.Flags().BoolVar(&reportDryRun, "dry-run", false, "print the computed prompt for each agent without running")
	reportCmd.Flags().BoolVar(&reportIgnorePreviousReport, "ignore-previous-report", false, "do not include the agent's previous report in the prompt")
	addCheaperModelFlag(reportCmd, &reportCheaperModel)
	_ = reportCmd.MarkFlagRequired("agents")
}

func runReport(cmd *cobra.Command, args []string) error {
	if reportDelta {
		return fmt.Errorf("--delta is not yet implemented")
	}

	agentIDs, err := prompts.ResolveAgentList(reportAgents)
	if err != nil {
		return err
	}

	env, err := root.Resolve(orgFlag, projectFlag)
	if err != nil {
		return err
	}

	if err := root.EnsureAgents(env.ProjectDir, env.StateDir, agentIDs); err != nil {
		return err
	}

	extraPrompt, err := prompts.ResolveOptional(reportExtraPrompt)
	if err != nil {
		return err
	}

	timeout := env.Config.Report.EffectiveTimeout(reportTimeout)
	reportType := "full"

	cr := newClaudeRunner(env)
	applyCheaperModel(cr, reportCheaperModel)
	basePinfo := env.NewProjectInfoParams("")
	var tasks []runner.PoolTask
	for _, agentID := range agentIDs {
		pinfo := basePinfo
		pinfo.Role = "agent " + agentID
		prompt, err := prompts.AssembleAgentPrompt(env.OrgDir, env.ProjectDir, agentID, env.SourceDir, extraPrompt, pinfo, reportIgnorePreviousReport)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: skipping %s — %v\n", agentID, err)
			continue
		}
		agentDir := filepath.Join(env.ProjectDir, "agents", agentID)
		tasks = append(tasks, runner.PoolTask{
			Prompt: prompt,
			RunOpts: runner.RunOpts{
				AgentID:              agentID,
				OutputDir:            env.AgentLogsDir(agentID, "report"),
				LastMessageFilePath:  env.AgentReportPath(agentID, reportType),
				ErrorMessageFilePath: filepath.Join(agentDir, prompts.FullReportErrorFile),
				WorkDir:              env.SourceDir,
				TimeoutMin:           timeout,
				HistoryDir:           env.AgentHistoryDir(agentID),
				PromptName:           reportType + "_prompt.md",
			},
		})
	}

	if len(tasks) == 0 {
		return fmt.Errorf("no valid agents to run")
	}

	if reportDryRun {
		for i, t := range tasks {
			if i > 0 {
				fmt.Println()
			}
			fmt.Printf("╔══ %s ══╗\n\n", t.AgentID)
			fmt.Println(t.Prompt)
			fmt.Printf("\n╚══ %s ══╝\n", t.AgentID)
		}
		return nil
	}

	fmt.Printf("Running %d agent(s) (max %d parallel, %dm timeout)...\n\n",
		len(tasks), env.Config.Report.MaxParallel, timeout)

	for _, t := range tasks {
		fmt.Printf("  %-25s queued\n", t.AgentID)
	}
	fmt.Println()

	ctx := context.Background()
	results := runner.RunPool(ctx, cr, tasks, env.Config.Report.MaxParallel, nil)

	cwd, _ := os.Getwd()

	var succeeded, failed int
	w := newTable()
	fmt.Fprintln(w, "AGENT\tENDED_AT\tELAPSED\tCOST\tTURNS\tSTATUS\tPATH")
	for _, r := range results {
		endedAt := r.EndedAt.Format("15:04:05")
		elapsed := runner.FormatDuration(r.Duration)
		cost := fmtCost(r.Cost)
		turns := fmtInt(r.Turns)

		if r.Err != nil {
			errorPath := filepath.Join(filepath.Dir(r.StreamFilePath), prompts.FullReportErrorFile)
			if _, err := os.Stat(errorPath); err != nil {
				errorPath = r.StderrFilePath
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\tERROR\t%s\n",
				r.AgentID, endedAt, elapsed, cost, turns, relPath(cwd, errorPath))
			failed++
		} else {
			reportPath := env.AgentReportPath(r.AgentID, reportType)
			historyDir := env.AgentHistoryDir(r.AgentID)
			if err := runner.ArchiveFile(reportPath, historyDir, reportType+"_report.md"); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not archive report for %s: %v\n", r.AgentID, err)
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\tOK\t%s\n",
				r.AgentID, endedAt, elapsed, cost, turns, relPath(cwd, reportPath))
			succeeded++
		}
	}
	w.Flush()

	fmt.Printf("\n%d succeeded, %d failed\n", succeeded, failed)
	if failed == 0 && succeeded > 0 {
		fmt.Printf("\nRun 'ateam review' to have the supervisor synthesize findings.\n")
	}

	if reportPrint && succeeded > 0 {
		for _, r := range results {
			if r.Err != nil {
				continue
			}
			fmt.Printf("\n══════ %s ══════\n\n%s\n", r.AgentID, r.Output)
		}
	}

	return nil
}

