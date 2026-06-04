package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/flow"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/spf13/cobra"
)

var (
	reportFlags                CommonExecFlags
	reportRoles                []string
	reportParallel             int
	reportPrint                bool
	reportDryRun               bool
	reportIgnorePreviousReport bool
	reportForce                bool
	reportReview               bool
	reportRerunFailed          bool
	reportMaxBudgetBatch       string
	reportAutoRoles            bool
	reportPlanOnly             bool
)

// ReportOptions holds configuration for a report run.
//
// There is intentionally no IncludeDisabled / --all option: producing fresh
// reports for disabled roles defeats the point of disabling them. Users who
// want to report a specific disabled role pass it explicitly via --roles.
type ReportOptions struct {
	CommonExecFlags
	Roles                []string
	Parallel             int
	Print                bool
	DryRun               bool
	IgnorePreviousReport bool
	Force                bool
	Review               bool
	RerunFailed          bool
	MaxBudgetBatch       string
	AutoRoles            bool
	PlanOnly             bool
}

var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "Run roles to produce analysis reports",
	Long: `Run one or more roles in parallel to analyze the project source code
and produce markdown reports. Defaults to all enabled roles.

Works from any project directory — discovers the .ateamorg/ and .ateam/ structure.

` + progressColumnsHelp("role") + `

Example:
  ateam report
  ateam report --roles test.gaps,project.security
  ateam report --roles code.structure --post-prompt "Focus on the auth module"
  ateam report --post-prompt @notes.md`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runReport(ReportOptions{
			CommonExecFlags:      reportFlags,
			Roles:                reportRoles,
			Parallel:             reportParallel,
			Print:                reportPrint,
			DryRun:               reportDryRun,
			IgnorePreviousReport: reportIgnorePreviousReport,
			Force:                reportForce,
			Review:               reportReview,
			RerunFailed:          reportRerunFailed,
			MaxBudgetBatch:       reportMaxBudgetBatch,
			AutoRoles:            reportAutoRoles,
			PlanOnly:             reportPlanOnly,
		})
	},
}

func init() {
	reportCmd.Flags().StringSliceVar(&reportRoles, "roles", nil, prompts.RoleFlagUsage()+" (default: all enabled roles)")
	registerCommonExecFlags(reportCmd, &reportFlags, commonFlagUsage{
		Timeout:      "timeout in minutes per role (overrides config)",
		Model:        "model override; takes precedence over --cheaper-model",
		Effort:       "reasoning effort override, passed verbatim to the agent CLI",
		MaxBudgetUSD: "per-role USD spend cap (claude-only; warns on codex)",
	})
	reportCmd.Flags().IntVar(&reportParallel, "parallel", 0, "max parallel roles (overrides config max_parallel)")
	reportCmd.Flags().BoolVar(&reportPrint, "print", false, "print reports to stdout after completion")
	reportCmd.Flags().BoolVar(&reportReview, "review", false, "run review automatically after reports complete")
	reportCmd.Flags().BoolVar(&reportDryRun, "dry-run", false, "print resolved commands for each role without running")
	reportCmd.Flags().BoolVar(&reportRerunFailed, "rerun-failed", false, "re-run only roles that failed in the last report round")
	reportCmd.Flags().BoolVar(&reportIgnorePreviousReport, "ignore-previous-report", false, "do not include the role's previous report in the prompt")
	reportCmd.Flags().StringVar(&reportMaxBudgetBatch, "max-budget-usd-batch", "",
		"stop dispatching new roles once batch cost crosses this USD")
	addForceFlag(reportCmd, &reportForce)
	addAutoRolesFlags(reportCmd, &reportAutoRoles, &reportPlanOnly)
}

func runReport(opts ReportOptions) error {
	env, err := resolveEnv()
	if err != nil {
		return err
	}
	if err := requireGitRepo(env, runner.ActionReport); err != nil {
		return err
	}

	if opts.AutoRoles {
		if len(opts.Roles) > 0 {
			return fmt.Errorf("--auto-roles and --roles are mutually exclusive")
		}
		if opts.RerunFailed {
			return fmt.Errorf("--auto-roles and --rerun-failed are mutually exclusive")
		}
	} else if opts.PlanOnly {
		return fmt.Errorf("--plan-only requires --auto-roles")
	}

	// Resolve role list — either from --auto-roles (planner agent),
	// --rerun-failed (DB), or --roles flag.
	var roleIDs []string
	var db *calldb.CallDB

	if opts.AutoRoles {
		roles, done, err := runAutoRoles(env, opts.Profile, opts.Agent, opts.Verbose, opts.PlanOnly, opts.DockerAutoSetup)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		roleIDs = roles
	} else if opts.RerunFailed {
		if len(opts.Roles) > 0 {
			return fmt.Errorf("--rerun-failed and --roles are mutually exclusive")
		}
		db, err = requireStateDB(env)
		if err != nil {
			if opts.DryRun {
				fmt.Println("No previous report found (no project database).")
				return nil
			}
			return err
		}
		defer db.Close()

		var succeeded []string
		succeeded, roleIDs, err = lastReportRolesByStatus(db, env.ProjectID())
		if err != nil {
			return err
		}
		if len(succeeded) > 0 {
			fmt.Printf("Roles with successful reports: %s\n", strings.Join(succeeded, ", "))
		}
		if len(roleIDs) == 0 {
			fmt.Println("No failed roles in the last report round.")
			return nil
		}
		fmt.Printf("Roles that failed to re-run:   %s\n\n", strings.Join(roleIDs, ", "))
	} else {
		roles := opts.Roles
		if len(roles) == 0 {
			roles = []string{"all"}
		}
		roleIDs, err = prompts.ResolveRoleList(roles, env.Config.Roles, env.ProjectDir, env.OrgDir)
		if err != nil {
			return err
		}
	}

	if err := root.EnsureRoles(env.ProjectDir, roleIDs); err != nil {
		return err
	}

	prePrompt, err := prompts.ResolveOptional(opts.PrePrompt)
	if err != nil {
		return err
	}
	postPrompt, err := prompts.ResolveOptional(opts.PostPrompt)
	if err != nil {
		return err
	}

	timeout := env.Config.Report.EffectiveTimeout(opts.Timeout)

	overrides := RunnerOverrides{
		ContainerName:     opts.ContainerName,
		CheaperModel:      opts.CheaperModel,
		Model:             opts.Model,
		Effort:            opts.Effort,
		MaxBudgetUSD:      opts.MaxBudgetUSD,
		MaxBudgetUSDBatch: opts.MaxBudgetBatch,
	}
	cr, err := buildRunner(env, RunnerSpec{
		Profile:         opts.Profile,
		Agent:           opts.Agent,
		Action:          runner.ActionReport,
		DockerAutoSetup: opts.DockerAutoSetup,
		Overrides:       overrides,
	})
	if err != nil {
		return err
	}

	batch := resolveBatch("", "report")
	cliOverridesProfile := opts.Profile != "" || opts.Agent != ""
	defaultProfile := env.Config.ResolveProfile(runner.ActionReport, "")

	// Build per-role bundles. Each bundle's Env carries a (possibly
	// role-specific) Executor — the outer Parallel doesn't see roles or
	// profiles. Render errors here surface as warnings + a skipped role,
	// matching the prior behavior.
	type roleBundle struct {
		roleID string
		bundle flow.PromptBundle
		runner *runner.AgentExecutor
	}
	var rbs []roleBundle
	for _, roleID := range roleIDs {
		roleID := roleID

		// Validate the role's prompt exists by attempting a preview
		// resolve — surfaces "no role main" errors before we spin up
		// an executor (matches legacy assembleRoleReport behavior).
		bundle := NewReportBundle(ReportBundleInput{
			Env:                env,
			RoleID:             roleID,
			PrePrompt:          prePrompt,
			PostPrompt:         postPrompt,
			SkipPreviousReport: opts.IgnorePreviousReport,
			TimeoutMin:         timeout,
			Verbose:            opts.Verbose,
			Batch:              batch,
		})
		if _, err := bundle.ResolvePreview(env, env.WorkDir); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: skipping %s — %v\n", roleID, err)
			continue
		}

		roleRunner := cr
		if !cliOverridesProfile {
			if roleProfile := env.Config.ResolveProfile(runner.ActionReport, roleID); roleProfile != defaultProfile {
				rr, err := buildRunner(env, RunnerSpec{
					Profile:         roleProfile,
					Action:          runner.ActionReport,
					Role:            roleID,
					DockerAutoSetup: opts.DockerAutoSetup,
					Overrides:       overrides,
				})
				if err != nil {
					fmt.Fprintf(os.Stderr, "Warning: cannot resolve profile %q for %s, using default — %v\n", roleProfile, roleID, err)
				} else {
					roleRunner = rr
				}
			}
		}

		rbs = append(rbs, roleBundle{roleID: roleID, bundle: *bundle, runner: roleRunner})
	}

	if len(rbs) == 0 {
		return fmt.Errorf("no valid roles to run")
	}

	if opts.DryRun {
		fmt.Printf("Roles: %s\n\n", strings.Join(roleIDs, ", "))
		for i, rb := range rbs {
			if i > 0 {
				fmt.Println()
			}
			fmt.Printf("╔══ %s ══╗\n\n", rb.roleID)
			printDryRunInfo(rb.runner, env, dryRunOpts{
				RoleID: rb.roleID,
				Action: runner.ActionReport,
				Batch:  batch,
			})
			fmt.Printf("\n╚══ %s ══╝\n", rb.roleID)
		}
		return nil
	}

	// Open DB if not already opened by --rerun-failed.
	if db == nil {
		db, err = openStateDB(env)
		if err != nil {
			return err
		}
		defer db.Close()
	}
	cr.CallDB = db
	for i := range rbs {
		rbs[i].runner.CallDB = db
	}

	if !opts.Force {
		if err := checkConcurrentRunsEnv(db, env, runner.ActionReport, roleIDs); err != nil {
			return err
		}
	}

	maxParallel := env.Config.Report.EffectiveMaxParallel(opts.Parallel)

	fmt.Printf("Running %d role(s) (max %d parallel, %dm timeout)...\n\n",
		len(rbs), maxParallel, timeout)

	preDispatch, err := batchBudgetPrecheck(db, env.ProjectID(), batch, opts.MaxBudgetBatch)
	if err != nil {
		return err
	}

	ctx, stop := cmdContext()
	defer stop()

	// Bake each bundle's Executor into its Env override; collect Steps
	// and labels for the reporter.
	steps := make([]flow.Step, 0, len(rbs))
	labels := make([]string, 0, len(rbs))
	for i := range rbs {
		rb := &rbs[i]
		roleEnv := flow.RuntimeEnv{
			Executor: rb.runner,
			WorkDir:  env.WorkDir,
			Role:     rb.roleID,
			Action:   runner.ActionReport,
			Batch:    batch,
		}
		rb.bundle.Env = &roleEnv
		steps = append(steps, rb.bundle)
		labels = append(labels, rb.roleID)
	}

	tr := newTableReporter(tableReporterOpts{
		out:       os.Stdout,
		labels:    labels,
		agentName: cr.Agent.Name(),
		itemLabel: "role(s)",
		onDone: func(s runner.RunSummary, cwd string) string {
			return relPath(cwd, env.RoleReportPath(s.RoleID))
		},
	})

	roles := flow.Parallel{
		Name:        "roles",
		Steps:       steps,
		Workers:     maxParallel,
		PreDispatch: preDispatch,
	}
	rtEnv := flow.RuntimeEnv{
		WorkDir: env.WorkDir,
		Action:  runner.ActionReport,
		Batch:   batch,
	}
	rc := flow.RunCtx{
		Ctx:      ctx,
		DB:       db,
		Resolved: env,
		Reporter: flow.MultiReporter{tr, &flow.BundleLogReporter{}},
	}
	flow.Run(roles, rtEnv, rc)
	runErr := tr.Close()

	succeeded, failed, skipped := tr.Counts()

	if opts.Print && succeeded > 0 {
		printRoleIDs := make([]string, 0, len(rbs))
		for _, rb := range rbs {
			printRoleIDs = append(printRoleIDs, rb.roleID)
		}
		printReportBodies(printRoleIDs, tr.Results(), env)
	}

	if shouldAutoReview(opts.Review, failed, skipped, succeeded) {
		fmt.Println()
		return runReview(reviewOptionsFromReport(opts))
	}

	if runErr != nil {
		return runErr
	}

	if succeeded > 0 {
		fmt.Printf("\nRun 'ateam review' to have the supervisor synthesize findings.\n")
	}
	return nil
}

// printReportBodies prints each role's report body to stdout in roleIDs order,
// filtering out roles that did not succeed in the current run.
//
// Only roles present in `results` with Err==nil and IsError==false are printed:
// printArtifact falls back to reading the on-disk report when given an empty
// fallback, so iterating failed or skipped roles would surface stale reports
// from prior runs under the current run's header.
func printReportBodies(roleIDs []string, results []runner.RunSummary, env *root.ResolvedEnv) {
	outputByRole := map[string]string{}
	for _, s := range results {
		if s.Err == nil && !s.IsError && s.RoleID != "" {
			outputByRole[s.RoleID] = s.Output
		}
	}
	for _, roleID := range roleIDs {
		output, ok := outputByRole[roleID]
		if !ok {
			continue
		}
		path := env.RoleReportPath(roleID)
		fmt.Printf("\n══════ %s ══════\n", roleID)
		printArtifact(path, output)
	}
}

// shouldAutoReview is the all-success gate for --review: don't auto-trigger
// when any role failed or was skipped (e.g. PreDispatch budget cap). Reviewing
// a partial set would synthesize findings against stale reports for the
// missing roles.
func shouldAutoReview(reviewOpt bool, failed, skipped, succeeded int) bool {
	return reviewOpt && failed == 0 && skipped == 0 && succeeded > 0
}

// reviewOptionsFromReport carries the user's report-time overrides into the
// auto-triggered review step so `report --review --roles X --profile Y` does
// not silently revert the review to defaults. Zero-valued fields stay zero so
// review's own defaulting (config, role-level profiles) still applies. The
// enabled-only gate for --roles is handled in prompts.ReviewSelector.Filter.
func reviewOptionsFromReport(opts ReportOptions) ReviewOptions {
	return ReviewOptions{
		CommonExecFlags: CommonExecFlags{
			Timeout:         opts.Timeout,
			CheaperModel:    opts.CheaperModel,
			Profile:         opts.Profile,
			Agent:           opts.Agent,
			Verbose:         opts.Verbose,
			DockerAutoSetup: opts.DockerAutoSetup,
			ContainerName:   opts.ContainerName,
			Model:           opts.Model,
			Effort:          opts.Effort,
		},
		Force: opts.Force,
		Roles: opts.Roles,
	}
}

// lastReportRolesByStatus returns (succeeded, failed) role lists from the
// latest report batch.
func lastReportRolesByStatus(db *calldb.CallDB, projectID string) (succeeded, failed []string, err error) {
	batch, err := db.LatestBatch(projectID, "report-")
	if err != nil {
		return nil, nil, fmt.Errorf("cannot query latest report: %w", err)
	}
	if batch == "" {
		return nil, nil, fmt.Errorf("no previous report found")
	}

	runs, err := db.RecentRuns(calldb.RecentFilter{Batch: batch, Limit: -1})
	if err != nil {
		return nil, nil, fmt.Errorf("cannot query runs for %s: %w", batch, err)
	}

	seen := make(map[string]bool)
	for _, r := range runs {
		if seen[r.Role] {
			continue
		}
		seen[r.Role] = true
		if r.IsError {
			failed = append(failed, r.Role)
		} else {
			succeeded = append(succeeded, r.Role)
		}
	}
	return succeeded, failed, nil
}
