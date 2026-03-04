package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ateam-poc/internal/config"
	"github.com/ateam-poc/internal/prompts"
	"github.com/ateam-poc/internal/runner"
	"github.com/spf13/cobra"
)

var (
	reviewExtraPrompt  string
	reviewCustomPrompt string
	reviewTimeout      int
)

var reviewCmd = &cobra.Command{
	Use:   "review",
	Short: "Supervisor reviews agent reports and produces decisions",
	Long: `Read all agent reports and have the supervisor produce a prioritized
decisions document.

Must be run from an ATeam project directory after running 'ateam report'.

Example:
  ateam review
  ateam review --extra-prompt "Focus on security findings"
  ateam review --prompt @custom_review.md
  ateam review --extra-prompt "This is a production financial app, prioritize accordingly"`,
	RunE: runReview,
}

func init() {
	reviewCmd.Flags().StringVar(&reviewExtraPrompt, "extra-prompt", "", "additional instructions (text or @filepath)")
	reviewCmd.Flags().StringVar(&reviewCustomPrompt, "prompt", "", "custom prompt replacing default supervisor role (text or @filepath)")
	reviewCmd.Flags().IntVar(&reviewTimeout, "agent-report-timeout", 0, "timeout in minutes (overrides config)")
}

func runReview(cmd *cobra.Command, args []string) error {
	projectDir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := config.Load(projectDir)
	if err != nil {
		return err
	}

	// Resolve extra prompt
	extraPrompt := ""
	if reviewExtraPrompt != "" {
		resolved, err := prompts.ResolveValue(reviewExtraPrompt)
		if err != nil {
			return err
		}
		extraPrompt = resolved
	}

	// Resolve custom prompt
	customPrompt := ""
	if reviewCustomPrompt != "" {
		resolved, err := prompts.ResolveValue(reviewCustomPrompt)
		if err != nil {
			return err
		}
		customPrompt = resolved
	}

	// Assemble the full review prompt (includes reading all report files)
	prompt, err := prompts.AssembleReviewPrompt(projectDir, extraPrompt, customPrompt)
	if err != nil {
		return err
	}

	// Determine timeout
	timeout := cfg.Execution.AgentReportTimeoutMinutes
	if reviewTimeout > 0 {
		timeout = reviewTimeout
	}

	reviewFile := filepath.Join(projectDir, "review.md")
	archiveDir := filepath.Join(projectDir, "archive")

	fmt.Printf("Supervisor reviewing reports (%dm timeout)...\n", timeout)

	ctx := context.Background()
	result := runner.RunClaude(ctx, prompt, reviewFile, timeout)

	if result.Err != nil {
		return fmt.Errorf("review failed: %w", result.Err)
	}

	// Archive
	_ = runner.ArchiveFile(reviewFile, archiveDir, "review.md")

	fmt.Printf("Done (%s)\n\n", runner.FormatDuration(result.Duration))
	fmt.Printf("Review written to: %s\n", reviewFile)
	fmt.Printf("Archived to: %s/\n", archiveDir)

	return nil
}
