package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	allExtraPrompt     string
	allQuiet           bool
	allTimeout         int
	allParallel        int
	allCheaperModel    bool
	allVerbose         bool
	allRoles           []string
	allProfile         string
	allDockerAutoSetup bool
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
	allCmd.Flags().IntVar(&allParallel, "parallel", 0, "max parallel report roles (overrides config max_parallel)")
	allCmd.Flags().StringSliceVar(&allRoles, "roles", nil, "run only these roles in the report phase and limit coding tasks to them in review")
	allCmd.Flags().StringVar(&allProfile, "profile", "", "profile for code sub-runs (passed to ateam code --profile)")
	addCheaperModelFlag(allCmd, &allCheaperModel)
	addVerboseFlag(allCmd, &allVerbose)
	addDockerAutoSetupFlag(allCmd, &allDockerAutoSetup)
}

func runAll(cmd *cobra.Command, args []string) error {
	printOutput := !allQuiet

	roles := allRoles
	if len(roles) == 0 {
		roles = []string{"all"}
	}

	// Phase 1: Report
	fmt.Println("=== Phase 1: Report ===")
	if err := runReport(ReportOptions{
		Roles:           roles,
		ExtraPrompt:     allExtraPrompt,
		Timeout:         allTimeout,
		Parallel:        allParallel,
		Print:           printOutput,
		CheaperModel:    allCheaperModel,
		Verbose:         allVerbose,
		DockerAutoSetup: allDockerAutoSetup,
	}); err != nil {
		return fmt.Errorf("report phase failed: %w", err)
	}

	// Phase 2: Review
	fmt.Println("\n=== Phase 2: Review ===")
	if err := runReview(ReviewOptions{
		ExtraPrompt:     allExtraPrompt,
		Timeout:         allTimeout,
		Print:           printOutput,
		CheaperModel:    allCheaperModel,
		Verbose:         allVerbose,
		Roles:           allRoles,
		DockerAutoSetup: allDockerAutoSetup,
	}); err != nil {
		return fmt.Errorf("review phase failed: %w", err)
	}

	// Phase 3: Code
	fmt.Println("\n=== Phase 3: Code ===")
	if err := runCode(CodeOptions{
		ExtraPrompt:     allExtraPrompt,
		Timeout:         allTimeout,
		Print:           printOutput,
		CheaperModel:    allCheaperModel,
		Verbose:         allVerbose,
		Profile:         allProfile,
		DockerAutoSetup: allDockerAutoSetup,
	}); err != nil {
		return fmt.Errorf("code phase failed: %w", err)
	}

	return nil
}
