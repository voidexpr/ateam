package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/display"
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

	var tasks []runner.PoolExec
	for _, roleID := range roleIDs {
		prompt, err := assembleRoleReportV1(env, roleID, "role "+roleID, extraPrompt, prePrompt, postPrompt, opts.IgnorePreviousReport)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: skipping %s — %v\n", roleID, err)
			continue
		}
		task := runner.PoolExec{
			Prompt: prompt,
			RunOpts: runner.RunOpts{
				RoleID:            roleID,
				Action:            runner.ActionReport,
				OutputKind:        runner.OutputKindReport,
				PromptName:        roleID, // → primary output `<roleID>.md`
				CanonicalDestFile: env.RoleReportPath(roleID),
				WorkDir:           env.WorkDir,
				TimeoutMin:        timeout,
				Verbose:           opts.Verbose,
				Batch:             batch,
			},
		}

		if !cliOverridesProfile {
			roleProfile := env.Config.ResolveProfile(runner.ActionReport, roleID)
			if roleProfile != defaultProfile {
				roleRunner, err := resolveRunner(env, roleProfile, "", runner.ActionReport, roleID, opts.DockerAutoSetup)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Warning: cannot resolve profile %q for %s, using default — %v\n", roleProfile, roleID, err)
				} else if err := applyRunnerOverrides(roleRunner, env, RunnerOverrides{
					ContainerName:     opts.ContainerName,
					CheaperModel:      opts.CheaperModel,
					Model:             opts.Model,
					Effort:            opts.Effort,
					MaxBudgetUSD:      opts.MaxBudgetUSD,
					MaxBudgetUSDBatch: opts.MaxBudgetBatch,
				}, runner.ActionReport); err != nil {
					return err
				} else {
					task.AgentExecutor = roleRunner
				}
			}
		}

		tasks = append(tasks, task)
	}

	if len(tasks) == 0 {
		return fmt.Errorf("no valid roles to run")
	}

	if opts.DryRun {
		fmt.Printf("Roles: %s\n\n", strings.Join(roleIDs, ", "))
		for i, t := range tasks {
			if i > 0 {
				fmt.Println()
			}
			fmt.Printf("╔══ %s ══╗\n\n", t.RoleID)
			dryRunRunner := cr
			if t.AgentExecutor != nil {
				dryRunRunner = t.AgentExecutor
			}
			printDryRunInfo(dryRunRunner, env, dryRunOpts{
				RoleID: t.RoleID,
				Action: runner.ActionReport,
				Batch:  batch,
			})
			fmt.Printf("\n╚══ %s ══╝\n", t.RoleID)
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
	for i := range tasks {
		if tasks[i].AgentExecutor != nil {
			tasks[i].AgentExecutor.CallDB = db
		}
	}

	if !opts.Force {
		if err := checkConcurrentRunsEnv(db, env, runner.ActionReport, roleIDs); err != nil {
			return err
		}
	}

	maxParallel := env.Config.Report.EffectiveMaxParallel(opts.Parallel)

	fmt.Printf("Running %d role(s) (max %d parallel, %dm timeout)...\n\n",
		len(tasks), maxParallel, timeout)

	preDispatch, err := batchBudgetPrecheck(db, env.ProjectID(), batch, opts.MaxBudgetBatch)
	if err != nil {
		return err
	}

	ctx, stop := cmdContext()
	defer stop()

	results, runErr := runPool(ctx, cr, tasks, maxParallel, poolDisplayOpts{
		out:         os.Stdout,
		agentName:   cr.Agent.Name(),
		itemLabel:   "role(s)",
		preDispatch: preDispatch,
		onDone: func(r runner.RunSummary, cwd string) string {
			// History file is written directly by the agent (or by the runner's
			// fallback path when the agent did not call Write); on success the
			// runner already promoted it to report.md, so no post-run archive
			// step is needed.
			return relPath(cwd, env.RoleReportPath(r.RoleID))
		},
	})

	var succeeded int
	for _, r := range results {
		if r.Err == nil {
			succeeded++
		}
	}

	if opts.Print && succeeded > 0 {
		for _, r := range results {
			if r.Err != nil {
				continue
			}
			fmt.Printf("\n══════ %s ══════\n", r.RoleID)
			printArtifact(env.RoleReportPath(r.RoleID), r.Output)
		}
	}

	if runErr != nil {
		return runErr
	}

	if opts.Review && succeeded > 0 {
		fmt.Println()
		return runReview(reviewOptionsFromReport(opts))
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
