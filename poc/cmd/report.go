package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ateam-poc/internal/agents"
	"github.com/ateam-poc/internal/config"
	"github.com/ateam-poc/internal/prompts"
	"github.com/ateam-poc/internal/runner"
	"github.com/spf13/cobra"
)

var (
	reportAgents       []string
	reportExtraPrompt  string
	reportTimeout      int
)

var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "Run agents to produce analysis reports",
	Long: `Run one or more agents in parallel to analyze the project source code
and produce markdown reports.

Must be run from an ATeam project directory (containing config.toml).

Example:
  ateam report --agents all
  ateam report --agents testing_basic,security
  ateam report --agents refactor_small --extra-prompt "Focus on the auth module"
  ateam report --agents all --extra-prompt @notes.md`,
	RunE: runReport,
}

func init() {
	reportCmd.Flags().StringSliceVar(&reportAgents, "agents", nil, "comma-separated agent list, or 'all' (required)")
	reportCmd.Flags().StringVar(&reportExtraPrompt, "extra-prompt", "", "additional instructions (text or @filepath)")
	reportCmd.Flags().IntVar(&reportTimeout, "agent-report-timeout", 0, "timeout in minutes per agent (overrides config)")
	_ = reportCmd.MarkFlagRequired("agents")
}

func runReport(cmd *cobra.Command, args []string) error {
	// Find project directory (current dir must have config.toml)
	projectDir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := config.Load(projectDir)
	if err != nil {
		return err
	}

	// Resolve agent list
	agentIDs, err := agents.ResolveAgentList(reportAgents)
	if err != nil {
		return err
	}

	// Resolve extra prompt (handle @filename)
	extraPrompt := ""
	if reportExtraPrompt != "" {
		resolved, err := prompts.ResolveValue(reportExtraPrompt)
		if err != nil {
			return err
		}
		extraPrompt = resolved
	}

	// Determine timeout
	timeout := cfg.Execution.AgentReportTimeoutMinutes
	if reportTimeout > 0 {
		timeout = reportTimeout
	}

	// Build tasks
	reportsDir := filepath.Join(projectDir, "reports")
	if err := os.MkdirAll(reportsDir, 0755); err != nil {
		return err
	}

	var tasks []runner.AgentTask
	for _, agentID := range agentIDs {
		prompt, err := prompts.AssembleAgentPrompt(projectDir, agentID, cfg.Project.SourceDir, extraPrompt)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: skipping %s — %v\n", agentID, err)
			continue
		}
		tasks = append(tasks, runner.AgentTask{
			AgentID:    agentID,
			Prompt:     prompt,
			OutputFile: filepath.Join(reportsDir, agentID+".report.md"),
		})
	}

	if len(tasks) == 0 {
		return fmt.Errorf("no valid agents to run")
	}

	fmt.Printf("Running %d agent(s) (max %d parallel, %dm timeout)...\n\n",
		len(tasks), cfg.Execution.MaxParallel, timeout)

	for _, t := range tasks {
		fmt.Printf("  %-25s queued\n", t.AgentID)
	}
	fmt.Println()

	// Run in parallel
	ctx := context.Background()
	results := runner.RunPool(ctx, tasks, cfg.Execution.MaxParallel, timeout)

	// Report results and archive
	archiveDir := filepath.Join(projectDir, "archive")
	var succeeded, failed int
	for _, r := range results {
		if r.Result.Err != nil {
			fmt.Printf("  %-25s FAILED  (%s) — %v\n", r.AgentID, runner.FormatDuration(r.Result.Duration), r.Result.Err)
			// Write error to report file so it's visible
			errorReport := fmt.Sprintf("# Report Failed: %s\n\nError: %v\n\nDuration: %s\n",
				r.AgentID, r.Result.Err, runner.FormatDuration(r.Result.Duration))
			reportPath := filepath.Join(reportsDir, r.AgentID+".report.md")
			os.WriteFile(reportPath, []byte(errorReport), 0644)
			failed++
		} else {
			fmt.Printf("  %-25s done    (%s)\n", r.AgentID, runner.FormatDuration(r.Result.Duration))
			// Archive
			reportPath := filepath.Join(reportsDir, r.AgentID+".report.md")
			_ = runner.ArchiveFile(reportPath, archiveDir, r.AgentID+".report.md")
			succeeded++
		}
	}

	fmt.Printf("\n%d succeeded, %d failed\n", succeeded, failed)
	if succeeded > 0 {
		fmt.Printf("\nReports are in %s/\n", reportsDir)
		fmt.Printf("Run 'ateam review' to have the supervisor synthesize findings.\n")
	}

	return nil
}
