package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/ateam-poc/internal/prompts"
	"github.com/ateam-poc/internal/root"
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

Works from any git project directory or from within the .ateam/ tree.

Example:
  ateam review
  ateam review --extra-prompt "Focus on security findings"
  ateam review --prompt @custom_review.md`,
	RunE: runReview,
}

func init() {
	reviewCmd.Flags().StringVar(&reviewExtraPrompt, "extra-prompt", "", "additional instructions (text or @filepath)")
	reviewCmd.Flags().StringVar(&reviewCustomPrompt, "prompt", "", "custom prompt replacing default supervisor role (text or @filepath)")
	reviewCmd.Flags().IntVar(&reviewTimeout, "timeout", 0, "timeout in minutes (overrides config)")
}

func runReview(cmd *cobra.Command, args []string) error {
	proj, err := root.Resolve(nil)
	if err != nil {
		return err
	}

	extraPrompt, err := prompts.ResolveOptional(reviewExtraPrompt)
	if err != nil {
		return err
	}

	customPrompt, err := prompts.ResolveOptional(reviewCustomPrompt)
	if err != nil {
		return err
	}

	prompt, err := prompts.AssembleReviewPrompt(proj.AteamRoot, proj.ProjectDir, extraPrompt, customPrompt)
	if err != nil {
		return err
	}

	timeout := proj.Config.Execution.EffectiveTimeout(reviewTimeout)

	reviewFile := proj.ReviewPath()
	historyDir := proj.ReviewHistoryDir()

	if err := os.MkdirAll(historyDir, 0755); err != nil {
		return fmt.Errorf("cannot create review history directory: %w", err)
	}

	fmt.Printf("Supervisor reviewing reports (%dm timeout)...\n", timeout)

	ctx := context.Background()
	result := runner.RunClaude(ctx, prompt, reviewFile, "", timeout)

	if result.Err != nil {
		return fmt.Errorf("review failed: %w", result.Err)
	}

	if err := runner.ArchiveFile(reviewFile, historyDir, "review.md"); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not archive review: %v\n", err)
	}

	fmt.Printf("Done (%s)\n\n", runner.FormatDuration(result.Duration))
	fmt.Printf("Review: %s\n", reviewFile)

	return nil
}
