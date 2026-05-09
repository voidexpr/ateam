package cmd

import (
	"fmt"
	"time"

	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/spf13/cobra"
)

var (
	verifyExtraPrompt     string
	verifyTimeout         int
	verifyPrint           bool
	verifyDryRun          bool
	verifyCheaperModel    bool
	verifyProfile         string
	verifyAgent           string
	verifyVerbose         bool
	verifyForce           bool
	verifyDockerAutoSetup bool
	verifyContainerName   string
	verifyMaxBudgetUSD    string
	verifyModel           string
	verifyEffort          string
)

// VerifyOptions holds configuration for a verify run.
type VerifyOptions struct {
	ExtraPrompt     string
	Timeout         int
	Print           bool
	DryRun          bool
	CheaperModel    bool
	Profile         string
	Agent           string
	Verbose         bool
	Force           bool
	DockerAutoSetup bool
	ContainerName   string
	MaxBudgetUSD    string
	Model           string
	Effort          string
}

var verifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Supervisor verifies recent code changes from `ateam code`",
	Long: `Have the supervisor inspect commits made by the most recent ` + "`ateam code`" + ` run,
look for logical bugs, broken or missing tests, and risky changes, then run
the project's test suite and record findings in a verification report.

` + "`ateam code`" + ` and ` + "`ateam all`" + ` chain verify automatically; run this
command directly to re-verify, or pass ` + "`--no-verify`" + ` to skip the chained run.

Example:
  ateam verify
  ateam verify --extra-prompt "Pay extra attention to migrations"
  ateam verify --dry-run`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runVerify(VerifyOptions{
			ExtraPrompt:     verifyExtraPrompt,
			Timeout:         verifyTimeout,
			Print:           verifyPrint,
			DryRun:          verifyDryRun,
			CheaperModel:    verifyCheaperModel,
			Profile:         verifyProfile,
			Agent:           verifyAgent,
			Verbose:         verifyVerbose,
			Force:           verifyForce,
			DockerAutoSetup: verifyDockerAutoSetup,
			ContainerName:   verifyContainerName,
			MaxBudgetUSD:    verifyMaxBudgetUSD,
			Model:           verifyModel,
			Effort:          verifyEffort,
		})
	},
}

func init() {
	verifyCmd.Flags().StringVar(&verifyExtraPrompt, "extra-prompt", "", "additional instructions (text or @filepath)")
	verifyCmd.Flags().IntVar(&verifyTimeout, "timeout", 0, "timeout in minutes (overrides config)")
	verifyCmd.Flags().BoolVar(&verifyPrint, "print", false, "print verification report to stdout after completion")
	verifyCmd.Flags().BoolVar(&verifyDryRun, "dry-run", false, "print the computed prompt without running")
	addCheaperModelFlag(verifyCmd, &verifyCheaperModel)
	verifyCmd.Flags().StringVar(&verifyModel, "model", "",
		"model override; takes precedence over --cheaper-model")
	verifyCmd.Flags().StringVar(&verifyEffort, "effort", "", "reasoning effort override, passed verbatim to the agent CLI")
	addProfileFlags(verifyCmd, &verifyProfile, &verifyAgent)
	addVerboseFlag(verifyCmd, &verifyVerbose)
	addForceFlag(verifyCmd, &verifyForce)
	addDockerAutoSetupFlag(verifyCmd, &verifyDockerAutoSetup)
	addContainerNameFlag(verifyCmd, &verifyContainerName)
	addBudgetFlags(verifyCmd, &verifyMaxBudgetUSD, nil,
		"USD spend cap for the supervisor (claude-only; errors on codex)", "")
}

func runVerify(opts VerifyOptions) error {
	env, err := root.Resolve(orgFlag, projectFlag)
	if err != nil {
		return err
	}

	extraPrompt, err := prompts.ResolveOptional(opts.ExtraPrompt)
	if err != nil {
		return err
	}

	pinfo := env.NewProjectInfoParams("the supervisor", "verify")
	prompt, err := prompts.AssembleCodeVerifyPrompt(env.OrgDir, env.ProjectDir, pinfo, extraPrompt)
	if err != nil {
		return err
	}

	timeout := env.Config.Review.EffectiveTimeout(opts.Timeout)

	supervisorDir := env.SupervisorDir()

	startedAt := time.Now()

	if opts.DryRun {
		fmt.Printf("╔══ verify ══╗\n\n")
		fmt.Println(prompt)
		fmt.Printf("\n╚══ verify ══╝\n")
		return nil
	}

	fmt.Printf("Supervisor verifying recent code changes (%dm timeout)...\n", timeout)

	cr, err := resolveRunner(env, opts.Profile, opts.Agent, runner.ActionVerify, "", opts.DockerAutoSetup)
	if err != nil {
		return err
	}
	if err := applyRunnerOverrides(cr, env, RunnerOverrides{
		ContainerName: opts.ContainerName,
		CheaperModel:  opts.CheaperModel,
		Model:         opts.Model,
		Effort:        opts.Effort,
		MaxBudgetUSD:  opts.MaxBudgetUSD,
	}, runner.ActionVerify); err != nil {
		return err
	}
	setSourceWritable(cr)

	db, err := openProjectDB(env)
	if err != nil {
		return err
	}
	defer db.Close()
	cr.CallDB = db

	if !opts.Force {
		if err := checkConcurrentRunsEnv(db, env, runner.ActionVerify, nil); err != nil {
			return err
		}
	}

	runOpts := runner.RunOpts{
		RoleID:           "supervisor",
		Action:           runner.ActionVerify,
		OutputKind:       runner.OutputKindVerify,
		CanonicalDestDir: supervisorDir,
		WorkDir:          env.SourceDir,
		TimeoutMin:       timeout,
		Verbose:          opts.Verbose,
		StartedAt:        startedAt,
	}

	ctx, stop := cmdContext()
	defer stop()
	result := cr.Run(ctx, prompt, runOpts, nil)

	if result.Err != nil {
		return fmt.Errorf("verify failed: %w", result.Err)
	}

	printDone(result)
	fmt.Printf("Verification report: %s\n", env.VerifyPath())

	if opts.Print {
		fmt.Printf("\n%s\n", result.Output)
	}

	return nil
}
