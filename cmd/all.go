package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	runAllPrePrompt       string
	runAllPostPrompt      string
	runAllQuiet           bool
	runAllTimeout         int
	runAllParallel        int
	runAllCheaperModel    bool
	runAllVerbose         bool
	runAllRoles           []string
	runAllAll             bool
	runAllMaxAge          string
	runAllProfile         string
	runAllDockerAutoSetup bool
	runAllContainerName   string
	runAllModel           string
	runAllEffort          string
	runAllMaxBudgetUSD    string
	runAllMaxBudgetBatch  string
	runAllAutoRoles       bool
	runAllPlanOnly        bool

	// Per-stage overrides
	runAllReportProfile     string
	runAllReportAgent       string
	runAllSupervisorProfile string
	runAllSupervisorAgent   string
	runAllCodeProfile       string
	runAllCodeAgent         string
)

var runAllCmd = &cobra.Command{
	Use:   "run-all",
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
  ateam run-all
  ateam run-all --roles security,deps              # report + review only those roles
  ateam run-all --all                              # include disabled roles
  ateam run-all --max-age 2h                       # review skips reports older than 2h
  ateam run-all --post-prompt "Focus on security"
  ateam run-all --report-agent claude-sonnet --supervisor-agent claude --code-profile docker
  ateam run-all --timeout 30`,
	RunE: runAll,
}

func init() {
	addPromptWrapFlags(runAllCmd, &runAllPrePrompt, &runAllPostPrompt)
	runAllCmd.Flags().BoolVarP(&runAllQuiet, "quiet", "q", false, "suppress output printing")
	runAllCmd.Flags().IntVar(&runAllTimeout, "timeout", 0, "per-phase timeout in minutes (overrides config)")
	runAllCmd.Flags().IntVar(&runAllParallel, "parallel", 0, "max parallel report roles (overrides config max_parallel)")
	runAllCmd.Flags().StringSliceVar(&runAllRoles, "roles", nil, "limit report and review to these roles' reports (default: all enabled roles)")
	runAllCmd.Flags().BoolVar(&runAllAll, "all", false, "include reports from roles disabled in config.toml")
	runAllCmd.Flags().StringVar(&runAllMaxAge, "max-age", "", "drop reports older than this in the review phase (e.g. 2h, 30m, 1d)")
	runAllCmd.Flags().StringVar(&runAllProfile, "profile", "", "profile for code sub-runs (passed to ateam code --profile)")
	runAllCmd.Flags().StringVar(&runAllReportProfile, "report-profile", "", "profile for report phase agents")
	runAllCmd.Flags().StringVar(&runAllReportAgent, "report-agent", "", "agent for report phase (uses 'none' container)")
	runAllCmd.Flags().StringVar(&runAllSupervisorProfile, "supervisor-profile", "", "profile for supervisor (review + code management)")
	runAllCmd.Flags().StringVar(&runAllSupervisorAgent, "supervisor-agent", "", "agent for supervisor (review + code management)")
	runAllCmd.Flags().StringVar(&runAllCodeProfile, "code-profile", "", "profile for code sub-runs (overrides --profile)")
	runAllCmd.Flags().StringVar(&runAllCodeAgent, "code-agent", "", "agent for code sub-runs (uses 'none' container)")
	runAllCmd.MarkFlagsMutuallyExclusive("report-profile", "report-agent")
	runAllCmd.MarkFlagsMutuallyExclusive("supervisor-profile", "supervisor-agent")
	runAllCmd.MarkFlagsMutuallyExclusive("code-profile", "code-agent")
	addCheaperModelFlag(runAllCmd, &runAllCheaperModel)
	runAllCmd.Flags().StringVar(&runAllModel, "model", "",
		"model override applied to every phase; takes precedence over --cheaper-model")
	runAllCmd.Flags().StringVar(&runAllEffort, "effort", "", "reasoning effort override applied to every phase, passed verbatim to the agent CLI")
	addBudgetFlags(runAllCmd, &runAllMaxBudgetUSD, &runAllMaxBudgetBatch,
		"per-agent USD spend cap applied to every phase (claude-only; warns on codex)",
		"stop dispatching new agents once batch cost crosses this USD (report and code phases)")
	addVerboseFlag(runAllCmd, &runAllVerbose)
	addDockerAutoSetupFlag(runAllCmd, &runAllDockerAutoSetup)
	addContainerNameFlag(runAllCmd, &runAllContainerName)
	addAutoRolesFlags(runAllCmd, &runAllAutoRoles, &runAllPlanOnly)
}

func runAll(cmd *cobra.Command, args []string) error {
	printOutput := !runAllQuiet

	if runAllAutoRoles {
		if len(runAllRoles) > 0 {
			return fmt.Errorf("--auto-roles and --roles are mutually exclusive")
		}
	} else if runAllPlanOnly {
		return fmt.Errorf("--plan-only requires --auto-roles")
	}

	if runAllAutoRoles {
		env, err := resolveEnv()
		if err != nil {
			return err
		}
		// Planner runs as a supervisor pass, so reuse --supervisor-profile/-agent.
		roles, done, err := runAutoRoles(env, runAllSupervisorProfile, runAllSupervisorAgent, runAllVerbose, runAllPlanOnly, runAllDockerAutoSetup)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		runAllRoles = roles
	}

	maxAge, err := parseMaxAge(runAllMaxAge)
	if err != nil {
		return err
	}

	// Resolve per-stage profile/agent.
	// --supervisor-* applies to review + code management.
	// --code-profile/--code-agent override --profile for sub-runs.
	codeSubRunProfile := coalesce(runAllCodeProfile, runAllProfile)
	codeSubRunAgent := runAllCodeAgent

	// commonBase carries the ateam-all flag values that are identical
	// across every phase. Per-phase profile/agent (and MaxBudgetUSD edge
	// cases) override the embedded copy below.
	commonBase := CommonExecFlags{
		PrePrompt:       runAllPrePrompt,
		PostPrompt:      runAllPostPrompt,
		Timeout:         runAllTimeout,
		CheaperModel:    runAllCheaperModel,
		Verbose:         runAllVerbose,
		DockerAutoSetup: runAllDockerAutoSetup,
		ContainerName:   runAllContainerName,
		Model:           runAllModel,
		Effort:          runAllEffort,
		MaxBudgetUSD:    runAllMaxBudgetUSD,
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
	reportCommon.Profile = runAllReportProfile
	reportCommon.Agent = runAllReportAgent
	if err := runReport(ReportOptions{
		CommonExecFlags: reportCommon,
		Roles:           runAllRoles,
		Parallel:        runAllParallel,
		Print:           false,
		MaxBudgetBatch:  runAllMaxBudgetBatch,
	}); err != nil {
		return fmt.Errorf("report phase failed: %w", err)
	}

	// Phase 2: Review — same role-selection as Phase 1 plus --all (include
	// disabled roles' stale reports) and --max-age (freshness window). --roles
	// does NOT constrain coding-task assignment in Phase 3 (feature dropped).
	fmt.Println("\n=== Phase 2: Review ===")
	supervisorCommon := commonBase
	supervisorCommon.Profile = runAllSupervisorProfile
	supervisorCommon.Agent = runAllSupervisorAgent
	if err := runReview(ReviewOptions{
		CommonExecFlags: supervisorCommon,
		Print:           printOutput,
		Roles:           runAllRoles,
		IncludeDisabled: runAllAll,
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
		SupervisorProfile: runAllSupervisorProfile,
		SupervisorAgent:   runAllSupervisorAgent,
		MaxBudgetBatch:    runAllMaxBudgetBatch,
	}); err != nil {
		return fmt.Errorf("code phase failed: %w", err)
	}

	// Phase 4: Verify — supervisor inspects commits made in Phase 3 and
	// runs the test suite. Always runs; the pipeline isn't complete without
	// it. Users who want to iterate without verify should invoke the phases
	// individually instead of `ateam run-all`.
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
