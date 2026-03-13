package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/ateam-poc/internal/prompts"
	"github.com/ateam-poc/internal/root"
	"github.com/ateam-poc/internal/runner"
	"github.com/spf13/cobra"
)

var (
	reviewExtraPrompt  string
	reviewCustomPrompt string
	reviewTimeout      int
	reviewPrint        bool
	reviewDryRun       bool
	reviewCheaperModel bool
	reviewProfile      string
	reviewAgent        string
	reviewVerbose      bool
)

var reviewCmd = &cobra.Command{
	Use:   "review",
	Short: "Supervisor reviews role reports and produces decisions",
	Long: `Read all role reports and have the supervisor produce a prioritized
decisions document.

Works from any project directory — discovers the .ateamorg/ and .ateam/ structure.

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
	reviewCmd.Flags().BoolVar(&reviewPrint, "print", false, "print review to stdout after completion")
	reviewCmd.Flags().BoolVar(&reviewDryRun, "dry-run", false, "print the computed prompt and list reports without running")
	addCheaperModelFlag(reviewCmd, &reviewCheaperModel)
	addProfileFlags(reviewCmd, &reviewProfile, &reviewAgent)
	addVerboseFlag(reviewCmd, &reviewVerbose)
}

func runReview(cmd *cobra.Command, args []string) error {
	env, err := root.Resolve(orgFlag, projectFlag)
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

	pinfo := env.NewProjectInfoParams("the supervisor")
	prompt, err := prompts.AssembleReviewPrompt(env.OrgDir, env.ProjectDir, pinfo, extraPrompt, customPrompt)
	if err != nil {
		return err
	}

	if reviewDryRun {
		return printReviewDryRun(env, prompt)
	}

	timeout := env.Config.Review.EffectiveTimeout(reviewTimeout)

	reviewFile := env.ReviewPath()
	reviewDir := filepath.Dir(reviewFile)
	historyDir := env.ReviewHistoryDir()

	if err := os.MkdirAll(historyDir, 0755); err != nil {
		return fmt.Errorf("cannot create review history directory: %w", err)
	}

	fmt.Printf("Supervisor reviewing reports (%dm timeout)...\n", timeout)

	cr, err := resolveRunner(env, reviewProfile, reviewAgent, runner.ActionReview, "")
	if err != nil {
		return err
	}
	applyCheaperModel(cr, reviewCheaperModel)

	opts := runner.RunOpts{
		RoleID:               "supervisor",
		Action:               runner.ActionReview,
		LogsDir:              env.SupervisorLogsDir(),
		LastMessageFilePath:  reviewFile,
		ErrorMessageFilePath: filepath.Join(reviewDir, "review_error.md"),
		WorkDir:              env.SourceDir,
		TimeoutMin:           timeout,
		HistoryDir:           historyDir,
		PromptName:           "review_prompt.md",
		Verbose:              reviewVerbose,
	}

	ctx := context.Background()
	result := cr.Run(ctx, prompt, opts, nil)

	if result.Err != nil {
		return fmt.Errorf("review failed: %w", result.Err)
	}

	if err := runner.ArchiveFile(reviewFile, historyDir, "review.md"); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not archive review: %v\n", err)
	}

	printDone(result)
	fmt.Printf("Review: %s\n", reviewFile)

	if reviewPrint {
		fmt.Printf("\n%s\n", result.Output)
	}

	return nil
}

func printReviewDryRun(env *root.ResolvedEnv, prompt string) error {
	reports, _ := prompts.DiscoverReports(env.ProjectDir)

	sort.Slice(reports, func(i, j int) bool {
		return reports[i].ModTime.After(reports[j].ModTime)
	})

	fmt.Println("Reports found:")
	if len(reports) == 0 {
		fmt.Println("  (none)")
	}
	for _, r := range reports {
		relPath, _ := filepath.Rel(filepath.Dir(env.OrgDir), r.Path)
		if relPath == "" {
			relPath = r.Path
		}
		fmt.Printf("  %s  %-30s %s\n", r.ModTime.Format(runner.TimestampFormat), r.RoleID, relPath)
	}

	fmt.Printf("\n╔══ supervisor ══╗\n\n")
	fmt.Println(prompt)
	fmt.Printf("\n╚══ supervisor ══╝\n")
	return nil
}
