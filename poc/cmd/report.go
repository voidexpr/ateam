package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/ateam-poc/internal/agents"
	"github.com/ateam-poc/internal/prompts"
	"github.com/ateam-poc/internal/root"
	"github.com/ateam-poc/internal/runner"
	"github.com/spf13/cobra"
)

var (
	reportAgents      []string
	reportExtraPrompt string
	reportTimeout     int
	reportDelta       bool
)

var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "Run agents to produce analysis reports",
	Long: `Run one or more agents in parallel to analyze the project source code
and produce markdown reports.

Works from any git project directory — discovers or creates the .ateam/ structure automatically.

Example:
  ateam report --agents all
  ateam report --agents testing_basic,security
  ateam report --agents refactor_small --extra-prompt "Focus on the auth module"
  ateam report --agents all --extra-prompt @notes.md`,
	RunE: runReport,
}

func init() {
	reportCmd.Flags().StringSliceVar(&reportAgents, "agents", nil, agents.FlagUsage()+" (required)")
	reportCmd.Flags().StringVar(&reportExtraPrompt, "extra-prompt", "", "additional instructions (text or @filepath)")
	reportCmd.Flags().IntVar(&reportTimeout, "agent-report-timeout", 0, "timeout in minutes per agent (overrides config)")
	reportCmd.Flags().BoolVar(&reportDelta, "delta", false, "produce delta report (not yet implemented)")
	_ = reportCmd.MarkFlagRequired("agents")
}

func runReport(cmd *cobra.Command, args []string) error {
	if reportDelta {
		return fmt.Errorf("--delta is not yet implemented")
	}

	agentIDs, err := agents.ResolveAgentList(reportAgents)
	if err != nil {
		return err
	}

	proj, err := root.Resolve(agentIDs)
	if err != nil {
		return err
	}

	// Ensure agent dirs and default prompts exist
	if err := root.EnsureAgents(proj.AteamRoot, proj.ProjectDir, agentIDs); err != nil {
		return err
	}

	extraPrompt, err := prompts.ResolveOptional(reportExtraPrompt)
	if err != nil {
		return err
	}

	timeout := proj.Config.Execution.EffectiveTimeout(reportTimeout)
	reportType := "full"

	// Build tasks
	var tasks []runner.AgentTask
	for _, agentID := range agentIDs {
		prompt, err := prompts.AssembleAgentPrompt(proj.AteamRoot, proj.ProjectDir, agentID, proj.SourceDir, extraPrompt)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: skipping %s — %v\n", agentID, err)
			continue
		}
		tasks = append(tasks, runner.AgentTask{
			AgentID:    agentID,
			Prompt:     prompt,
			OutputFile: proj.AgentReportPath(agentID, reportType),
			WorkDir:    proj.SourceDir,
		})
	}

	if len(tasks) == 0 {
		return fmt.Errorf("no valid agents to run")
	}

	fmt.Printf("Running %d agent(s) (max %d parallel, %dm timeout)...\n\n",
		len(tasks), proj.Config.Execution.MaxParallel, timeout)

	for _, t := range tasks {
		fmt.Printf("  %-25s queued\n", t.AgentID)
	}
	fmt.Println()

	ctx := context.Background()
	results := runner.RunPool(ctx, tasks, proj.Config.Execution.MaxParallel, timeout)

	var succeeded, failed int
	for _, r := range results {
		reportPath := proj.AgentReportPath(r.AgentID, reportType)
		if r.Result.Err != nil {
			fmt.Printf("  %-25s FAILED  (%s) — %v\n", r.AgentID, runner.FormatDuration(r.Result.Duration), r.Result.Err)
			errorReport := fmt.Sprintf("# Report Failed: %s\n\nError: %v\n\nDuration: %s\n",
				r.AgentID, r.Result.Err, runner.FormatDuration(r.Result.Duration))
			if err := os.WriteFile(reportPath, []byte(errorReport), 0644); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not write error report for %s: %v\n", r.AgentID, err)
			}
			failed++
		} else {
			producedAt := time.Now().Format("2006-01-02 15:04")
			fmt.Printf("%s: %s (produced at %s, took %s)\n", r.AgentID, reportPath, producedAt, runner.FormatDuration(r.Result.Duration))
			historyDir := proj.AgentHistoryDir(r.AgentID)
			if err := runner.ArchiveFile(reportPath, historyDir, reportType+"_report.md"); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not archive report for %s: %v\n", r.AgentID, err)
			}
			succeeded++
		}
	}

	fmt.Printf("\n%d succeeded, %d failed\n", succeeded, failed)
	if succeeded > 0 {
		fmt.Printf("\nRun 'ateam review' to have the supervisor synthesize findings.\n")
	}

	return nil
}
