package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	allExtraPrompt     string
	allPrePrompt       string
	allPostPrompt      string
	allQuiet           bool
	allTimeout         int
	allParallel        int
	allCheaperModel    bool
	allVerbose         bool
	allRoles           []string
	allAll             bool
	allMaxAge          string
	allProfile         string
	allDockerAutoSetup bool
	allContainerName   string
	allModel           string
	allEffort          string
	allMaxBudgetUSD    string
	allMaxBudgetBatch  string
	allAutoRoles       bool
	allPlanOnly        bool

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
	Short: "Run the full pipeline: report, review, code, verify",
	Long: `Run the full ateam pipeline sequentially: report → review → code → verify.
All four phases always run — to stop earlier, invoke the individual
commands instead (e.g. ateam report && ateam review && ateam code).

Equivalent to:
  ateam report --print && ateam review --print && ateam code --print && ateam verify

--roles applies to both report and review (and never affects the code phase).
--all and --max-age only affect the review phase: report still runs only on
enabled roles to avoid producing stale data for roles you've disabled.

Per-stage profile/agent overrides let you mix agents across the pipeline.
--supervisor-profile/--supervisor-agent apply to review, code management, and verify.

Example:
  ateam all
  ateam all --roles security,deps              # report + review only those roles
  ateam all --all                              # include disabled roles
  ateam all --max-age 2h                       # review skips reports older than 2h
  ateam all --extra-prompt "Focus on security"
  ateam all --report-agent claude-sonnet --supervisor-agent claude --code-profile docker
  ateam all --timeout 30`,
	RunE: runAll,
}

func init() {
	allCmd.Flags().StringVar(&allExtraPrompt, "extra-prompt", "", "additional instructions passed to all phases (text or @filepath)")
	allCmd.Flags().StringVar(&allPrePrompt, "pre-prompt", "", "text wrapped at the very front of every phase's assembled prompt (text or @filepath)")
	allCmd.Flags().StringVar(&allPostPrompt, "post-prompt", "", "text wrapped at the very end of every phase's assembled prompt (text or @filepath)")
	allCmd.Flags().BoolVarP(&allQuiet, "quiet", "q", false, "suppress output printing")
	allCmd.Flags().IntVar(&allTimeout, "timeout", 0, "per-phase timeout in minutes (overrides config)")
	allCmd.Flags().IntVar(&allParallel, "parallel", 0, "max parallel report roles (overrides config max_parallel)")
	allCmd.Flags().StringSliceVar(&allRoles, "roles", nil, "limit report and review to these roles' reports (default: all enabled roles)")
	allCmd.Flags().BoolVar(&allAll, "all", false, "include reports from roles disabled in config.toml")
	allCmd.Flags().StringVar(&allMaxAge, "max-age", "", "drop reports older than this in the review phase (e.g. 2h, 30m, 1d)")
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
	allCmd.Flags().StringVar(&allModel, "model", "",
		"model override applied to every phase; takes precedence over --cheaper-model")
	allCmd.Flags().StringVar(&allEffort, "effort", "", "reasoning effort override applied to every phase, passed verbatim to the agent CLI")
	addBudgetFlags(allCmd, &allMaxBudgetUSD, &allMaxBudgetBatch,
		"per-agent USD spend cap applied to every phase (claude-only; warns on codex)",
		"stop dispatching new agents once batch cost crosses this USD (report and code phases)")
	addVerboseFlag(allCmd, &allVerbose)
	addDockerAutoSetupFlag(allCmd, &allDockerAutoSetup)
	addContainerNameFlag(allCmd, &allContainerName)
	addAutoRolesFlags(allCmd, &allAutoRoles, &allPlanOnly)
}

func runAll(cmd *cobra.Command, args []string) error {
	printOutput := !allQuiet

	if allAutoRoles {
		if len(allRoles) > 0 {
			return fmt.Errorf("--auto-roles and --roles are mutually exclusive")
		}
	} else if allPlanOnly {
		return fmt.Errorf("--plan-only requires --auto-roles")
	}

	if allAutoRoles {
		env, err := resolveEnv()
		if err != nil {
			return err
		}
		// Planner runs as a supervisor pass, so reuse --supervisor-profile/-agent.
		roles, done, err := runAutoRoles(env, allSupervisorProfile, allSupervisorAgent, allVerbose, allPlanOnly, allDockerAutoSetup)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		allRoles = roles
	}

	maxAge, err := parseMaxAge(allMaxAge)
	if err != nil {
		return err
	}

	// Resolve per-stage profile/agent.
	// --supervisor-* applies to review + code management.
	// --code-profile/--code-agent override --profile for sub-runs.
	codeSubRunProfile := coalesce(allCodeProfile, allProfile)
	codeSubRunAgent := allCodeAgent

	// commonBase carries the 13 ateam-all flag values that are identical
	// across every phase. Per-phase profile/agent (and MaxBudgetUSD edge
	// cases) override the embedded copy below.
	commonBase := CommonExecFlags{
		ExtraPrompt:     allExtraPrompt,
		PrePrompt:       allPrePrompt,
		PostPrompt:      allPostPrompt,
		Timeout:         allTimeout,
		CheaperModel:    allCheaperModel,
		Verbose:         allVerbose,
		DockerAutoSetup: allDockerAutoSetup,
		ContainerName:   allContainerName,
		Model:           allModel,
		Effort:          allEffort,
		MaxBudgetUSD:    allMaxBudgetUSD,
	}

	// Phase 1: Report. Always produces fresh reports for the selected roles —
	// --all is intentionally NOT threaded here: producing reports for disabled
	// roles defeats the purpose of disabling them. Use --roles to target a
	// specific disabled role on demand.
	//   - empty --roles: every enabled role
	//   - explicit --roles A,B: those exact roles
	// Print=false: per-role bodies live at .ateam/roles/<role>/report.md.
	fmt.Println("=== Phase 1: Report ===")
	reportCommon := commonBase
	reportCommon.Profile = allReportProfile
	reportCommon.Agent = allReportAgent
	if err := runReport(ReportOptions{
		CommonExecFlags: reportCommon,
		Roles:           allRoles,
		Parallel:        allParallel,
		Print:           false,
		MaxBudgetBatch:  allMaxBudgetBatch,
	}); err != nil {
		return fmt.Errorf("report phase failed: %w", err)
	}

	// Phase 2: Review — same role-selection as Phase 1 plus --all (include
	// disabled roles' stale reports) and --max-age (freshness window). --roles
	// does NOT constrain coding-task assignment in Phase 3 (feature dropped).
	fmt.Println("\n=== Phase 2: Review ===")
	supervisorCommon := commonBase
	supervisorCommon.Profile = allSupervisorProfile
	supervisorCommon.Agent = allSupervisorAgent
	if err := runReview(ReviewOptions{
		CommonExecFlags: supervisorCommon,
		Print:           printOutput,
		Roles:           allRoles,
		IncludeDisabled: allAll,
		MaxAge:          maxAge,
	}); err != nil {
		return fmt.Errorf("review phase failed: %w", err)
	}

	// Phase 3: Code. runCode no longer chains verify on its own; Phase 4
	// below is the single verify run for the pipeline.
	fmt.Println("\n=== Phase 3: Code ===")
	codeCommon := commonBase
	codeCommon.Profile = codeSubRunProfile
	codeCommon.Agent = codeSubRunAgent
	if err := runCode(CodeOptions{
		CommonExecFlags:   codeCommon,
		Print:             printOutput,
		SupervisorProfile: allSupervisorProfile,
		SupervisorAgent:   allSupervisorAgent,
		MaxBudgetBatch:    allMaxBudgetBatch,
	}); err != nil {
		return fmt.Errorf("code phase failed: %w", err)
	}

	// Phase 4: Verify — supervisor inspects commits made in Phase 3 and
	// runs the test suite. Always runs; the pipeline isn't complete without
	// it. Users who want to iterate without verify should invoke the phases
	// individually instead of `ateam all`.
	fmt.Println("\n=== Phase 4: Verify ===")
	if err := runVerify(VerifyOptions{
		CommonExecFlags: supervisorCommon,
		Print:           printOutput,
	}); err != nil {
		return fmt.Errorf("verify phase failed: %w", err)
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
