package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ateam-poc/internal/gitutil"
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
	reportPrint       bool
	reportDryRun      bool
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

	if err := root.EnsureAgents(env.ProjectDir, agentIDs); err != nil {
		return err
	}

	extraPrompt, err := prompts.ResolveOptional(reportExtraPrompt)
	if err != nil {
		return err
	}

	meta, _ := gitutil.GetProjectMeta(env.SourceDir)

	timeout := env.Config.Report.EffectiveTimeout(reportTimeout)
	reportType := "full"

	var tasks []runner.AgentTask
	for _, agentID := range agentIDs {
		prompt, err := prompts.AssembleAgentPrompt(env.OrgDir, env.ProjectDir, agentID, env.SourceDir, extraPrompt, meta)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: skipping %s — %v\n", agentID, err)
			continue
		}
		tasks = append(tasks, runner.AgentTask{
			AgentID:    agentID,
			Prompt:     prompt,
			OutputFile: env.AgentReportPath(agentID, reportType),
			WorkDir:    env.SourceDir,
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
	results := runner.RunPool(ctx, tasks, env.Config.Report.MaxParallel, timeout)

	var succeeded, failed int
	for _, r := range results {
		reportPath := env.AgentReportPath(r.AgentID, reportType)
		agentDir := filepath.Dir(reportPath)
		if r.Result.Err != nil {
			fmt.Printf("  %-25s FAILED  (%s) — %v\n", r.AgentID, runner.FormatDuration(r.Result.Duration), r.Result.Err)

			// Write brief marker to the report file
			errorReport := fmt.Sprintf("# Report Failed: %s\n\nError: %v\n\nDuration: %s\n",
				r.AgentID, r.Result.Err, runner.FormatDuration(r.Result.Duration))
			_ = os.WriteFile(reportPath, []byte(errorReport), 0644)

			// Write detailed error log
			errorLogPath := filepath.Join(agentDir, prompts.FullReportErrorFile)
			var detail strings.Builder
			fmt.Fprintf(&detail, "# Report Error: %s\n\n", r.AgentID)
			fmt.Fprintf(&detail, "**Time:** %s\n\n", time.Now().Format("2006-01-02 15:04:05"))
			fmt.Fprintf(&detail, "**Duration:** %s\n\n", runner.FormatDuration(r.Result.Duration))
			fmt.Fprintf(&detail, "**Error:** %v\n\n", r.Result.Err)
			fmt.Fprintf(&detail, "**Work Dir:** %s\n\n", env.SourceDir)
			if r.Result.Stderr != "" {
				fmt.Fprintf(&detail, "## Stderr\n\n```\n%s\n```\n", r.Result.Stderr)
			}
			if r.Result.Output != "" {
				fmt.Fprintf(&detail, "\n## Stdout\n\n```\n%s\n```\n", r.Result.Output)
			}
			_ = os.WriteFile(errorLogPath, []byte(detail.String()), 0644)

			// Show last lines of stderr/stdout for quick diagnosis
			if r.Result.Stderr != "" {
				fmt.Printf("    stderr (last lines):\n")
				printLastLines(r.Result.Stderr, 5)
			}
			if r.Result.Output != "" {
				fmt.Printf("    stdout (last lines):\n")
				printLastLines(r.Result.Output, 5)
			}
			fmt.Printf("    details: %s\n", errorLogPath)
			fmt.Printf("    logs:    %s/last_run_{stdout,stderr}.log\n", agentDir)

			failed++
		} else {
			producedAt := time.Now().Format("2006-01-02 15:04")
			fmt.Printf("  %-25s OK      (%s, produced at %s)\n", r.AgentID, runner.FormatDuration(r.Result.Duration), producedAt)
			historyDir := env.AgentHistoryDir(r.AgentID)
			if err := runner.ArchiveFile(reportPath, historyDir, reportType+"_report.md"); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not archive report for %s: %v\n", r.AgentID, err)
			}
			succeeded++
		}
	}

	fmt.Printf("\n%d succeeded, %d failed\n", succeeded, failed)
	if failed == 0 && succeeded > 0 {
		fmt.Printf("\nRun 'ateam review' to have the supervisor synthesize findings.\n")
	}

	if reportPrint && succeeded > 0 {
		for _, r := range results {
			if r.Result.Err != nil {
				continue
			}
			fmt.Printf("\n══════ %s ══════\n\n%s\n", r.AgentID, r.Result.Output)
		}
	}

	return nil
}

func printLastLines(s string, n int) {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	start := 0
	if len(lines) > n {
		start = len(lines) - n
	}
	for _, line := range lines[start:] {
		fmt.Printf("      %s\n", line)
	}
}
