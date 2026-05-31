package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/display"
	"github.com/ateam/internal/flow"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/spf13/cobra"
)

var (
	reportRoles                []string
	reportExtraPrompt          string
	reportPrePrompt            string
	reportPostPrompt           string
	reportTimeout              int
	reportParallel             int
	reportPrint                bool
	reportDryRun               bool
	reportIgnorePreviousReport bool
	reportCheaperModel         bool
	reportProfile              string
	reportAgent                string
	reportVerbose              bool
	reportForce                bool
	reportReview               bool
	reportDockerAutoSetup      bool
	reportContainerName        string
	reportRerunFailed          bool
	reportModel                string
	reportEffort               string
	reportMaxBudgetUSD         string
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
	Roles                []string
	ExtraPrompt          string
	PrePrompt            string
	PostPrompt           string
	Timeout              int
	Parallel             int
	Print                bool
	DryRun               bool
	IgnorePreviousReport bool
	CheaperModel         bool
	Profile              string
	Agent                string
	Verbose              bool
	Force                bool
	Review               bool
	DockerAutoSetup      bool
	ContainerName        string
	RerunFailed          bool
	Model                string
	Effort               string
	MaxBudgetUSD         string
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
  ateam report --roles code.structure --extra-prompt "Focus on the auth module"
  ateam report --extra-prompt @notes.md`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runReport(ReportOptions{
			Roles:                reportRoles,
			ExtraPrompt:          reportExtraPrompt,
			PrePrompt:            reportPrePrompt,
			PostPrompt:           reportPostPrompt,
			Timeout:              reportTimeout,
			Parallel:             reportParallel,
			Print:                reportPrint,
			DryRun:               reportDryRun,
			IgnorePreviousReport: reportIgnorePreviousReport,
			CheaperModel:         reportCheaperModel,
			Profile:              reportProfile,
			Agent:                reportAgent,
			Verbose:              reportVerbose,
			Force:                reportForce,
			Review:               reportReview,
			DockerAutoSetup:      reportDockerAutoSetup,
			ContainerName:        reportContainerName,
			RerunFailed:          reportRerunFailed,
			Model:                reportModel,
			Effort:               reportEffort,
			MaxBudgetUSD:         reportMaxBudgetUSD,
			MaxBudgetBatch:       reportMaxBudgetBatch,
			AutoRoles:            reportAutoRoles,
			PlanOnly:             reportPlanOnly,
		})
	},
}

func init() {
	reportCmd.Flags().StringSliceVar(&reportRoles, "roles", nil, prompts.RoleFlagUsage()+" (default: all enabled roles)")
	reportCmd.Flags().StringVar(&reportExtraPrompt, "extra-prompt", "", "additional instructions (text or @filepath); appended after extras, before the outer --post-prompt wrap")
	reportCmd.Flags().StringVar(&reportPrePrompt, "pre-prompt", "", "text wrapped at the very front of the assembled prompt, before anchor-discovered content (text or @filepath)")
	reportCmd.Flags().StringVar(&reportPostPrompt, "post-prompt", "", "text wrapped at the very end of the assembled prompt, after every other section (text or @filepath)")
	reportCmd.Flags().IntVar(&reportTimeout, "timeout", 0, "timeout in minutes per role (overrides config)")
	reportCmd.Flags().IntVar(&reportParallel, "parallel", 0, "max parallel roles (overrides config max_parallel)")
	reportCmd.Flags().BoolVar(&reportPrint, "print", false, "print reports to stdout after completion")
	reportCmd.Flags().BoolVar(&reportReview, "review", false, "run review automatically after reports complete")
	reportCmd.Flags().BoolVar(&reportDryRun, "dry-run", false, "print resolved commands for each role without running")
	reportCmd.Flags().BoolVar(&reportRerunFailed, "rerun-failed", false, "re-run only roles that failed in the last report round")
	reportCmd.Flags().BoolVar(&reportIgnorePreviousReport, "ignore-previous-report", false, "do not include the role's previous report in the prompt")
	addCheaperModelFlag(reportCmd, &reportCheaperModel)
	reportCmd.Flags().StringVar(&reportModel, "model", "",
		"model override; takes precedence over --cheaper-model")
	reportCmd.Flags().StringVar(&reportEffort, "effort", "", "reasoning effort override, passed verbatim to the agent CLI")
	addBudgetFlags(reportCmd, &reportMaxBudgetUSD, &reportMaxBudgetBatch,
		"per-role USD spend cap (claude-only; warns on codex)",
		"stop dispatching new roles once batch cost crosses this USD")
	addProfileFlags(reportCmd, &reportProfile, &reportAgent)
	addVerboseFlag(reportCmd, &reportVerbose)
	addForceFlag(reportCmd, &reportForce)
	addDockerAutoSetupFlag(reportCmd, &reportDockerAutoSetup)
	addContainerNameFlag(reportCmd, &reportContainerName)
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

	extraPrompt, err := prompts.ResolveOptional(opts.ExtraPrompt)
	if err != nil {
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

	cr, err := resolveRunner(env, opts.Profile, opts.Agent, runner.ActionReport, "", opts.DockerAutoSetup)
	if err != nil {
		return err
	}
	if err := applyRunnerOverrides(cr, env, RunnerOverrides{
		ContainerName:     opts.ContainerName,
		CheaperModel:      opts.CheaperModel,
		Model:             opts.Model,
		Effort:            opts.Effort,
		MaxBudgetUSD:      opts.MaxBudgetUSD,
		MaxBudgetUSDBatch: opts.MaxBudgetBatch,
	}, runner.ActionReport); err != nil {
		return err
	}

	batch := "report-" + time.Now().Format(display.TimestampFormat)
	cliOverridesProfile := opts.Profile != "" || opts.Agent != ""
	defaultProfile := env.Config.ResolveProfile(runner.ActionReport, "")

	overrides := RunnerOverrides{
		ContainerName:     opts.ContainerName,
		CheaperModel:      opts.CheaperModel,
		Model:             opts.Model,
		Effort:            opts.Effort,
		MaxBudgetUSD:      opts.MaxBudgetUSD,
		MaxBudgetUSDBatch: opts.MaxBudgetBatch,
	}

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

		prompt, err := assembleRoleReportV1(env, roleID, "role "+roleID, extraPrompt, prePrompt, postPrompt, opts.IgnorePreviousReport)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: skipping %s — %v\n", roleID, err)
			continue
		}

		roleRunner := cr
		if !cliOverridesProfile {
			if roleProfile := env.Config.ResolveProfile(runner.ActionReport, roleID); roleProfile != defaultProfile {
				rr, err := resolveRunner(env, roleProfile, "", runner.ActionReport, roleID, opts.DockerAutoSetup)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Warning: cannot resolve profile %q for %s, using default — %v\n", roleProfile, roleID, err)
				} else if err := applyRunnerOverrides(rr, env, overrides, runner.ActionReport); err != nil {
					return err
				} else {
					roleRunner = rr
				}
			}
		}

		runOpts := runner.RunOpts{
			RoleID:            roleID,
			Action:            runner.ActionReport,
			OutputKind:        runner.OutputKindReport,
			PromptName:        roleID, // → primary output `<roleID>.md`
			CanonicalDestFile: env.RoleReportPath(roleID),
			WorkDir:           env.WorkDir,
			TimeoutMin:        timeout,
			Verbose:           opts.Verbose,
			Batch:             batch,
		}

		bundle := flow.PromptBundle{
			Name:   roleID,
			Role:   roleID,
			Action: runner.ActionReport,
			Render: func(flow.RuntimeEnv) (string, error) {
				return prompt, nil
			},
			RunOpts: func(flow.RuntimeEnv) runner.RunOpts {
				return runOpts
			},
		}
		rbs = append(rbs, roleBundle{roleID: roleID, bundle: bundle, runner: roleRunner})
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
		Reporter: tr,
	}
	flow.Run(roles, rtEnv, rc)
	runErr := tr.Close()

	succeeded, failed, skipped := tr.Counts()

	// --print prints per-role bodies in submission (roleIDs) order so the
	// output is deterministic regardless of which role finishes first.
	// Done after tr.Close so the bodies land below the torn-down table
	// region, never interleaved with progress redraws.
	//
	// Only roles that succeeded in THIS run are printed: printArtifact
	// falls back to reading the on-disk report when given an empty
	// fallback, so iterating failed/skipped roles would surface stale
	// reports from prior runs under the current run's header.
	if opts.Print && succeeded > 0 {
		outputByRole := map[string]string{}
		for _, s := range tr.Results() {
			if s.Err == nil && !s.IsError && s.RoleID != "" {
				outputByRole[s.RoleID] = s.Output
			}
		}
		for _, rb := range rbs {
			roleID := rb.roleID
			output, ok := outputByRole[roleID]
			if !ok {
				continue
			}
			path := env.RoleReportPath(roleID)
			fmt.Printf("\n══════ %s ══════\n", roleID)
			printArtifact(path, output)
		}
	}

	// All-success rule for --review: don't auto-trigger when any role
	// failed or was skipped (e.g. PreDispatch budget cap). Reviewing a
	// partial set would synthesize findings against stale reports for the
	// missing roles.
	if opts.Review && failed == 0 && skipped == 0 && succeeded > 0 {
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

// reviewOptionsFromReport carries the user's report-time overrides into the
// auto-triggered review step so `report --review --roles X --profile Y` does
// not silently revert the review to defaults. Zero-valued fields stay zero so
// review's own defaulting (config, role-level profiles) still applies. The
// enabled-only gate for --roles is handled in prompts.ReviewSelector.Filter.
func reviewOptionsFromReport(opts ReportOptions) ReviewOptions {
	return ReviewOptions{
		ExtraPrompt:     opts.ExtraPrompt,
		Timeout:         opts.Timeout,
		CheaperModel:    opts.CheaperModel,
		Profile:         opts.Profile,
		Agent:           opts.Agent,
		Verbose:         opts.Verbose,
		Force:           opts.Force,
		Roles:           opts.Roles,
		DockerAutoSetup: opts.DockerAutoSetup,
		ContainerName:   opts.ContainerName,
		Model:           opts.Model,
		Effort:          opts.Effort,
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
