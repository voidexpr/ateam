package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

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
	reviewCmd.Flags().BoolVar(&reviewPrint, "print", false, "print review to stdout after completion")
	reviewCmd.Flags().BoolVar(&reviewDryRun, "dry-run", false, "print the computed prompt and list reports without running")
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

	prompt, err := prompts.AssembleReviewPrompt(proj.AteamRoot, proj.ProjectDir, proj.SourceDir, extraPrompt, customPrompt)
	if err != nil {
		return err
	}

	if reviewDryRun {
		return printReviewDryRun(proj, prompt)
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

	if reviewPrint {
		fmt.Printf("\n%s\n", result.Output)
	}

	return nil
}

func printReviewDryRun(proj *root.ResolvedProject, prompt string) error {
	agentsDir := filepath.Join(proj.ProjectDir, "agents")
	entries, _ := os.ReadDir(agentsDir)

	type reportEntry struct {
		agent   string
		modTime time.Time
		relPath string
	}
	var reports []reportEntry
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		reportPath := filepath.Join(agentsDir, entry.Name(), prompts.FullReportFile)
		info, err := os.Stat(reportPath)
		if err != nil {
			continue
		}
		relPath, _ := filepath.Rel(filepath.Dir(proj.AteamRoot), reportPath)
		if relPath == "" {
			relPath = reportPath
		}
		reports = append(reports, reportEntry{entry.Name(), info.ModTime(), relPath})
	}
	sort.Slice(reports, func(i, j int) bool {
		return reports[i].modTime.After(reports[j].modTime)
	})

	fmt.Println("Reports found:")
	if len(reports) == 0 {
		fmt.Println("  (none)")
	}
	for _, r := range reports {
		fmt.Printf("  %s  %-30s %s\n", r.modTime.Format("2006-01-02 15:04"), r.agent, r.relPath)
	}

	fmt.Printf("\n╔══ supervisor ══╗\n\n")
	fmt.Println(prompt)
	fmt.Printf("\n╚══ supervisor ══╝\n")
	return nil
}
