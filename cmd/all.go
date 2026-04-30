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
	allVerify          bool

	// Per-stage overrides
	allReportProfile     string
	allReportAgent       string
	allSupervisorProfile string
	allSupervisorAgent   string
	allCodeProfile       string
	allCodeAgent         string
)

var allCmd = &cobra.Command{
	Use:   "all",
	Short: "Run the full pipeline: report, review, and code (optionally verify)",
	Long: `Run the full ateam pipeline sequentially: report → review → code.
Pass --verify to chain a verify phase after code.

Equivalent to:
  ateam report --roles all --print && ateam review --print && ateam code --print

Per-stage profile/agent overrides let you mix agents across the pipeline.
--supervisor-profile/--supervisor-agent apply to review, code management, and verify.

Example:
  ateam all
  ateam all --verify
  ateam all --extra-prompt "Focus on security"
  ateam all --report-agent claude-sonnet --supervisor-agent claude --code-profile docker
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
	allCmd.Flags().StringVar(&allReportProfile, "report-profile", "", "profile for report phase agents")
	allCmd.Flags().StringVar(&allReportAgent, "report-agent", "", "agent for report phase (uses 'none' container)")
	allCmd.Flags().StringVar(&allSupervisorProfile, "supervisor-profile", "", "profile for supervisor (review + code management)")
	allCmd.Flags().StringVar(&allSupervisorAgent, "supervisor-agent", "", "agent for supervisor (review + code management)")
	allCmd.Flags().StringVar(&allCodeProfile, "code-profile", "", "profile for code sub-runs (overrides --profile)")
	allCmd.Flags().StringVar(&allCodeAgent, "code-agent", "", "agent for code sub-runs (uses 'none' container)")
	allCmd.MarkFlagsMutuallyExclusive("report-profile", "report-agent")
	allCmd.MarkFlagsMutuallyExclusive("supervisor-profile", "supervisor-agent")
	allCmd.MarkFlagsMutuallyExclusive("code-profile", "code-agent")
	addCheaperModelFlag(allCmd, &allCheaperModel)
	addVerboseFlag(allCmd, &allVerbose)
	addDockerAutoSetupFlag(allCmd, &allDockerAutoSetup)
	allCmd.Flags().BoolVar(&allVerify, "verify", false, "run 'ateam verify' after the code phase completes")
}

func runAll(cmd *cobra.Command, args []string) error {
	printOutput := !allQuiet

	roles := allRoles
	if len(roles) == 0 {
		roles = []string{"all"}
	}

	// Resolve per-stage profile/agent.
	// --supervisor-* applies to review + code management.
	// --code-profile/--code-agent override --profile for sub-runs.
	codeSubRunProfile := coalesce(allCodeProfile, allProfile)
	codeSubRunAgent := allCodeAgent

	// Phase 1: Report. Print=false to skip per-role body dumps; the pool
	// table and failure summary cover what's useful inline, and the full
	// bodies live at .ateam/roles/<role>/report.md.
	fmt.Println("=== Phase 1: Report ===")
	if err := runReport(ReportOptions{
		Roles:           roles,
		ExtraPrompt:     allExtraPrompt,
		Timeout:         allTimeout,
		Parallel:        allParallel,
		Print:           false,
		CheaperModel:    allCheaperModel,
		Profile:         allReportProfile,
		Agent:           allReportAgent,
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
		Profile:         allSupervisorProfile,
		Agent:           allSupervisorAgent,
		Verbose:         allVerbose,
		Roles:           allRoles,
		DockerAutoSetup: allDockerAutoSetup,
	}); err != nil {
		return fmt.Errorf("review phase failed: %w", err)
	}

	// Phase 3: Code
	fmt.Println("\n=== Phase 3: Code ===")
	if err := runCode(CodeOptions{
		ExtraPrompt:       allExtraPrompt,
		Timeout:           allTimeout,
		Print:             printOutput,
		CheaperModel:      allCheaperModel,
		Profile:           codeSubRunProfile,
		Agent:             codeSubRunAgent,
		SupervisorProfile: allSupervisorProfile,
		SupervisorAgent:   allSupervisorAgent,
		Verbose:           allVerbose,
		DockerAutoSetup:   allDockerAutoSetup,
	}); err != nil {
		return fmt.Errorf("code phase failed: %w", err)
	}

	if allVerify {
		fmt.Println("\n=== Phase 4: Verify ===")
		if err := runVerify(VerifyOptions{
			ExtraPrompt:     allExtraPrompt,
			Timeout:         allTimeout,
			Print:           printOutput,
			CheaperModel:    allCheaperModel,
			Profile:         allSupervisorProfile,
			Agent:           allSupervisorAgent,
			Verbose:         allVerbose,
			DockerAutoSetup: allDockerAutoSetup,
		}); err != nil {
			return fmt.Errorf("verify phase failed: %w", err)
		}
	}

	return nil
}

// coalesce returns the first non-empty string.
func coalesce(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
