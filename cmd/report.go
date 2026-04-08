package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

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
	reportCmd.Flags().BoolVar(&reportDryRun, "dry-run", false, "print the computed prompt for each role without running")
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

	roles := opts.Roles
	if len(roles) == 0 {
		roles = []string{"all"}
	}
	roleIDs, err := prompts.ResolveRoleList(roles, env.Config.Roles, env.ProjectDir, env.OrgDir)
	if err != nil {
		return err
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
	applyContainerNameOverride(cr, opts.ContainerName)
	applyCheaperModel(cr, opts.CheaperModel)

	taskGroup := "report-" + time.Now().Format(runner.TimestampFormat)

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
		tasks = append(tasks, runner.PoolTask{
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
		})
	}

	if len(tasks) == 0 {
		return fmt.Errorf("no valid roles to run")
	}

	if opts.DryRun {
		for i, t := range tasks {
			if i > 0 {
				fmt.Println()
			}
			fmt.Printf("╔══ %s ══╗\n\n", t.RoleID)
			fmt.Println(t.Prompt)
			fmt.Printf("\n╚══ %s ══╝\n", t.RoleID)
		}
		return nil
	}

	db, err := openProjectDB(env)
	if err != nil {
		return err
	}
	defer db.Close()
	cr.CallDB = db

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
