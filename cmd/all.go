package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	allExtraPrompt  string
	allQuiet        bool
	allTimeout      int
	allCheaperModel bool
	allVerbose      bool
)

var allCmd = &cobra.Command{
	Use:   "all",
	Short: "Run the full pipeline: report, review, and code",
	Long: `Run the full ateam pipeline sequentially: report → review → code.

Equivalent to:
  ateam report --roles all --print && ateam review --print && ateam code --print

Example:
  ateam all
  ateam all --extra-prompt "Focus on security"
  ateam all --quiet
  ateam all --timeout 30`,
	RunE: runAll,
}

func init() {
	allCmd.Flags().StringVar(&allExtraPrompt, "extra-prompt", "", "additional instructions passed to all phases (text or @filepath)")
	allCmd.Flags().BoolVarP(&allQuiet, "quiet", "q", false, "suppress output printing")
	allCmd.Flags().IntVar(&allTimeout, "timeout", 0, "per-phase timeout in minutes (overrides config)")
	addCheaperModelFlag(allCmd, &allCheaperModel)
	addVerboseFlag(allCmd, &allVerbose)
}

func runAll(cmd *cobra.Command, args []string) error {
	printOutput := !allQuiet

	// Phase 1: Report
	fmt.Println("=== Phase 1: Report ===")
	reportRoles = []string{"all"}
	reportPrint = printOutput
	reportExtraPrompt = allExtraPrompt
	reportTimeout = allTimeout
	reportCheaperModel = allCheaperModel
	reportVerbose = allVerbose
	reportDryRun = false
	reportIgnorePreviousReport = false
	if err := runReport(nil, nil); err != nil {
		return fmt.Errorf("report phase failed: %w", err)
	}

	// Phase 2: Review
	fmt.Println("\n=== Phase 2: Review ===")
	reviewPrint = printOutput
	reviewExtraPrompt = allExtraPrompt
	reviewTimeout = allTimeout
	reviewCheaperModel = allCheaperModel
	reviewVerbose = allVerbose
	reviewDryRun = false
	reviewCustomPrompt = ""
	if err := runReview(nil, nil); err != nil {
		return fmt.Errorf("review phase failed: %w", err)
	}

	// Phase 3: Code
	fmt.Println("\n=== Phase 3: Code ===")
	codePrint = printOutput
	codeExtraPrompt = allExtraPrompt
	codeTimeout = allTimeout
	codeCheaperModel = allCheaperModel
	codeVerbose = allVerbose
	codeDryRun = false
	codeReview = ""
	codeManagement = ""
	if err := runCode(nil, nil); err != nil {
		return fmt.Errorf("code phase failed: %w", err)
	}

	return nil
}
