package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

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
	reportCmd.Flags().StringSliceVar(&reportAgents, "agents", nil, agents.FlagUsage()+" (required)")
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
	var tasks []runner.AgentTask
	for _, agentID := range agentIDs {
		prompt, err := prompts.AssembleAgentPrompt(projectDir, agentID, cfg.Project.SourceDir, extraPrompt)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: skipping %s — %v\n", agentID, err)
			continue
		}
		agentDir := filepath.Join(projectDir, "agents", agentID)
		if err := os.MkdirAll(agentDir, 0755); err != nil {
			return err
		}
		tasks = append(tasks, runner.AgentTask{
			AgentID:    agentID,
			Prompt:     prompt,
			OutputFile: filepath.Join(agentDir, agentID+".report.md"),
			WorkDir:    cfg.Project.SourceDir,
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
	var succeeded, failed int
	for _, r := range results {
		agentDir := filepath.Join(projectDir, "agents", r.AgentID)
		reportPath := filepath.Join(agentDir, r.AgentID+".report.md")
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
			relPath, _ := filepath.Rel(projectDir, reportPath)
			fmt.Printf("%s: %s (produced at %s, took %s)\n", r.AgentID, relPath, producedAt, runner.FormatDuration(r.Result.Duration))
			archiveDir := filepath.Join(agentDir, "reports")
			_ = runner.ArchiveFile(reportPath, archiveDir, r.AgentID+".report.md")
			succeeded++
		}
	}

	fmt.Printf("\n%d succeeded, %d failed\n", succeeded, failed)
	if succeeded > 0 {
		fmt.Printf("\nReports are in agents/*/\n")
		fmt.Printf("Run 'ateam review' to have the supervisor synthesize findings.\n")
	}

	return nil
}
