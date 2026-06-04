package cmd

import (
	"fmt"
	"time"

	"github.com/ateam/internal/flow"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/runner"
	"github.com/spf13/cobra"
)

var (
	verifyFlags  CommonExecFlags
	verifyPrint  bool
	verifyDryRun bool
	verifyForce  bool
)

// VerifyOptions holds configuration for a verify run.
type VerifyOptions struct {
	CommonExecFlags
	Print  bool
	DryRun bool
	Force  bool
}

var verifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Supervisor verifies recent code changes from `ateam code`",
	Long: `Have the supervisor inspect commits made by the most recent ` + "`ateam code`" + ` run,
look for logical bugs, broken or missing tests, and risky changes, then run
the project's test suite and record findings in a verification report.

Run after ` + "`ateam code`" + ` to inspect the resulting commits, or use
` + "`ateam run-all`" + ` for the full pipeline which always runs verify as the
final phase.

Example:
  ateam verify
  ateam verify --post-prompt "Pay extra attention to migrations"
  ateam verify --dry-run`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runVerify(VerifyOptions{
			CommonExecFlags: verifyFlags,
			Print:           verifyPrint,
			DryRun:          verifyDryRun,
			Force:           verifyForce,
		})
	},
}

func init() {
	registerCommonExecFlags(verifyCmd, &verifyFlags, commonFlagUsage{
		Timeout:      "timeout in minutes (overrides config)",
		Model:        "model override; takes precedence over --cheaper-model",
		Effort:       "reasoning effort override, passed verbatim to the agent CLI",
		MaxBudgetUSD: "USD spend cap for the supervisor (claude-only; errors on codex)",
	})
	verifyCmd.Flags().BoolVar(&verifyPrint, "print", false, "print verification report to stdout after completion")
	verifyCmd.Flags().BoolVar(&verifyDryRun, "dry-run", false, "print the computed prompt without running")
	addForceFlag(verifyCmd, &verifyForce)
}

func runVerify(opts VerifyOptions) error {
	env, err := resolveEnv()
	if err != nil {
		return err
	}
	if err := requireGitRepo(env, runner.ActionVerify); err != nil {
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

	timeout := env.Config.Verify.EffectiveTimeout(opts.Timeout)
	startedAt := time.Now()
	bundle := NewVerifyBundle(VerifyBundleInput{
		Env:        env,
		PrePrompt:  prePrompt,
		PostPrompt: postPrompt,
		TimeoutMin: timeout,
		Verbose:    opts.Verbose,
		Force:      opts.Force,
		Print:      opts.Print,
		StartedAt:  startedAt,
	})

	if opts.DryRun {
		prompt, err := bundle.ResolvePreview(env, env.WorkDir)
		if err != nil {
			return err
		}
		fmt.Printf("╔══ verify ══╗\n\n")
		fmt.Println(prompt)
		fmt.Printf("\n╚══ verify ══╝\n")
		return nil
	}

	fmt.Printf("Supervisor verifying recent code changes (%dm timeout)...\n", timeout)

	cr, err := buildRunner(env, RunnerSpec{
		Profile:         opts.Profile,
		Agent:           opts.Agent,
		Action:          runner.ActionVerify,
		DockerAutoSetup: opts.DockerAutoSetup,
		Overrides: RunnerOverrides{
			ContainerName: opts.ContainerName,
			CheaperModel:  opts.CheaperModel,
			Model:         opts.Model,
			Effort:        opts.Effort,
			MaxBudgetUSD:  opts.MaxBudgetUSD,
		},
	})
	if err != nil {
		return err
	}

	db, err := openStateDB(env)
	if err != nil {
		return err
	}
	defer db.Close()
	cr.CallDB = db

	ctx, stop := cmdContext()
	defer stop()

	rtEnv := flow.RuntimeEnv{
		Executor: cr,
		WorkDir:  env.WorkDir,
		Role:     "supervisor",
		Action:   runner.ActionVerify,
	}
	rc := flow.RunCtx{
		Ctx:      ctx,
		DB:       db,
		Resolved: env,
		Reporter: flow.MultiReporter{
			&flow.StdoutReporter{},
			&flow.BundleLogReporter{},
		},
	}
	return flow.Run(*bundle, rtEnv, rc).FirstError()
}
