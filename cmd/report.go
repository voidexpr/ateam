package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/spf13/cobra"
)

var (
	reportRoles                []string
	reportExtraPrompt          string
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
)

// ReportOptions holds configuration for a report run.
type ReportOptions struct {
	Roles                []string
	ExtraPrompt          string
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
}

var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "Run roles to produce analysis reports",
	Long: `Run one or more roles in parallel to analyze the project source code
and produce markdown reports. Defaults to all enabled roles.

Works from any project directory — discovers the .ateamorg/ and .ateam/ structure.

Example:
  ateam report
  ateam report --roles testing_basic,security
  ateam report --roles refactor_small --extra-prompt "Focus on the auth module"
  ateam report --extra-prompt @notes.md`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runReport(ReportOptions{
			Roles:                reportRoles,
			ExtraPrompt:          reportExtraPrompt,
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
		})
	},
}

func init() {
	reportCmd.Flags().StringSliceVar(&reportRoles, "roles", nil, prompts.RoleFlagUsage()+" (default: all)")
	reportCmd.Flags().StringVar(&reportExtraPrompt, "extra-prompt", "", "additional instructions (text or @filepath)")
	reportCmd.Flags().IntVar(&reportTimeout, "timeout", 0, "timeout in minutes per role (overrides config)")
	reportCmd.Flags().IntVar(&reportParallel, "parallel", 0, "max parallel roles (overrides config max_parallel)")
	reportCmd.Flags().BoolVar(&reportPrint, "print", false, "print reports to stdout after completion")
	reportCmd.Flags().BoolVar(&reportReview, "review", false, "run review automatically after reports complete")
	reportCmd.Flags().BoolVar(&reportDryRun, "dry-run", false, "print resolved commands for each role without running")
	reportCmd.Flags().BoolVar(&reportRerunFailed, "rerun-failed", false, "re-run only roles that failed in the last report round")
	reportCmd.Flags().BoolVar(&reportIgnorePreviousReport, "ignore-previous-report", false, "do not include the role's previous report in the prompt")
	addCheaperModelFlag(reportCmd, &reportCheaperModel)
	addProfileFlags(reportCmd, &reportProfile, &reportAgent)
	addVerboseFlag(reportCmd, &reportVerbose)
	addForceFlag(reportCmd, &reportForce)
	addDockerAutoSetupFlag(reportCmd, &reportDockerAutoSetup)
	addContainerNameFlag(reportCmd, &reportContainerName)
}

func runReport(opts ReportOptions) error {
	env, err := root.Resolve(orgFlag, projectFlag)
	if err != nil {
		return err
	}

	// Resolve role list — either from --rerun-failed (DB) or --roles flag.
	var roleIDs []string
	var db *calldb.CallDB

	if opts.RerunFailed {
		if len(opts.Roles) > 0 {
			return fmt.Errorf("--rerun-failed and --roles are mutually exclusive")
		}
		db, err = requireProjectDB(env)
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

	timeout := env.Config.Report.EffectiveTimeout(opts.Timeout)

	cr, err := resolveRunner(env, opts.Profile, opts.Agent, runner.ActionReport, "", opts.DockerAutoSetup)
	if err != nil {
		return err
	}
	if err := applyContainerName(cr, env, opts.ContainerName); err != nil {
		return err
	}
	applyCheaperModel(cr, opts.CheaperModel)

	taskGroup := "report-" + time.Now().Format(runner.TimestampFormat)

	cliOverridesProfile := opts.Profile != "" || opts.Agent != ""
	defaultProfile := env.Config.ResolveProfile(runner.ActionReport, "")

	basePinfo := env.NewProjectInfoParams("", "report")
	var tasks []runner.PoolTask
	for _, roleID := range roleIDs {
		pinfo := basePinfo
		pinfo.Role = "role " + roleID
		prompt, err := prompts.AssembleRolePrompt(env.OrgDir, env.ProjectDir, roleID, env.SourceDir, extraPrompt, pinfo, opts.IgnorePreviousReport)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: skipping %s — %v\n", roleID, err)
			continue
		}
		roleDir := env.RoleDir(roleID)
		task := runner.PoolTask{
			Prompt: prompt,
			RunOpts: runner.RunOpts{
				RoleID:               roleID,
				Action:               runner.ActionReport,
				LogsDir:              env.RoleLogsDir(roleID),
				LastMessageFilePath:  env.RoleReportPath(roleID),
				ErrorMessageFilePath: filepath.Join(roleDir, prompts.ReportErrorFile),
				WorkDir:              env.SourceDir,
				TimeoutMin:           timeout,
				HistoryDir:           env.RoleHistoryDir(roleID),
				PromptName:           "report_prompt.md",
				Verbose:              opts.Verbose,
				TaskGroup:            taskGroup,
			},
		}

		if !cliOverridesProfile {
			roleProfile := env.Config.ResolveProfile(runner.ActionReport, roleID)
			if roleProfile != defaultProfile {
				roleRunner, err := resolveRunner(env, roleProfile, "", runner.ActionReport, roleID, opts.DockerAutoSetup)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Warning: cannot resolve profile %q for %s, using default — %v\n", roleProfile, roleID, err)
				} else if err := applyContainerName(roleRunner, env, opts.ContainerName); err != nil {
					return err
				} else {
					applyCheaperModel(roleRunner, opts.CheaperModel)
					task.Runner = roleRunner
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
			if t.Runner != nil {
				dryRunRunner = t.Runner
			}
			printDryRunInfo(dryRunRunner, env, dryRunOpts{
				RoleID:    t.RoleID,
				Action:    runner.ActionReport,
				TaskGroup: taskGroup,
			})
			fmt.Printf("\n╚══ %s ══╝\n", t.RoleID)
		}
		return nil
	}

	// Open DB if not already opened by --rerun-failed.
	if db == nil {
		db, err = openProjectDB(env)
		if err != nil {
			return err
		}
		defer db.Close()
	}
	cr.CallDB = db
	for i := range tasks {
		if tasks[i].Runner != nil {
			tasks[i].Runner.CallDB = db
		}
	}

	if !opts.Force {
		if err := checkConcurrentRuns(db, "", runner.ActionReport, roleIDs); err != nil {
			return err
		}
	}

	maxParallel := env.Config.Report.EffectiveMaxParallel(opts.Parallel)

	fmt.Printf("Running %d role(s) (max %d parallel, %dm timeout)...\n\n",
		len(tasks), maxParallel, timeout)

	ctx, stop := cmdContext()
	defer stop()

	results, runErr := runPool(ctx, cr, tasks, maxParallel, poolDisplayOpts{
		out:       os.Stdout,
		agentName: cr.Agent.Name(),
		itemLabel: "role(s)",
		onDone: func(r runner.RunSummary, cwd string) string {
			reportPath := env.RoleReportPath(r.RoleID)
			if err := runner.ArchiveFile(reportPath, env.RoleHistoryDir(r.RoleID), prompts.ReportFile, r.StartedAt); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not archive report for %s: %v\n", r.RoleID, err)
			}
			return relPath(cwd, reportPath)
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
			fmt.Printf("\n══════ %s ══════\n\n%s\n", r.RoleID, r.Output)
		}
	}

	if runErr != nil {
		return runErr
	}

	if opts.Review && succeeded > 0 {
		fmt.Println()
		return runReview(ReviewOptions{})
	}

	if succeeded > 0 {
		fmt.Printf("\nRun 'ateam review' to have the supervisor synthesize findings.\n")
	}

	return nil
}

// lastReportRolesByStatus returns (succeeded, failed) role lists from the
// latest report task group.
func lastReportRolesByStatus(db *calldb.CallDB, projectID string) (succeeded, failed []string, err error) {
	tg, err := db.LatestTaskGroup(projectID, "report-")
	if err != nil {
		return nil, nil, fmt.Errorf("cannot query latest report: %w", err)
	}
	if tg == "" {
		return nil, nil, fmt.Errorf("no previous report found")
	}

	runs, err := db.RecentRuns(calldb.RecentFilter{TaskGroup: tg, Limit: -1})
	if err != nil {
		return nil, nil, fmt.Errorf("cannot query runs for %s: %w", tg, err)
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
