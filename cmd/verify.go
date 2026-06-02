package cmd

import (
	"fmt"
	"time"

	"github.com/ateam/internal/flow"
	"github.com/ateam/internal/flow/actions"
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
` + "`ateam all`" + ` for the full pipeline which always runs verify as the
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

	// Assemble the prompt up front so --dry-run can print it without
	// spinning up the executor + DB.
	prompt, err := assembleSupervisor(env, "code_verify", "the supervisor", "verify", prePrompt, postPrompt)
	if err != nil {
		return err
	}

	// Verify runs a supervisor pass like review; reuses the review timeout helper.
	timeout := env.Config.Review.EffectiveTimeout(opts.Timeout)

	// v1 flat layout: promotion writes to .ateam/shared/verify.md (the file,
	// not a per-action subdir). Sidecars stay in runtime/<exec_id>/.
	verifyFile := env.VerifyPath()

	if opts.DryRun {
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
	startedAt := time.Now()

	bundle := flow.PromptBundle{
		Name:   "verify",
		Role:   "supervisor",
		Action: runner.ActionVerify,
		Render: func(flow.RuntimeEnv) (string, error) {
			return prompt, nil
		},
		RunOpts: func(flow.RuntimeEnv) runner.RunOpts {
			return runner.RunOpts{
				RoleID:            "supervisor",
				Action:            runner.ActionVerify,
				OutputKind:        runner.OutputKindVerify,
				CanonicalDestFile: verifyFile,
				WorkDir:           env.WorkDir,
				TimeoutMin:        timeout,
				Verbose:           opts.Verbose,
				StartedAt:         startedAt,
				QuietExecID:       true,
			}
		},
		PreExec: []flow.Action{
			actions.CheckConcurrentRuns{If: !opts.Force, Action: runner.ActionVerify},
		},
		PostExec: []flow.Action{
			actions.PrintArtifactPath{Label: "Verification report", Path: verifyFile},
			actions.PrintArtifactBody{If: opts.Print, Path: verifyFile},
		},
	}

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
	return flow.Run(bundle, rtEnv, rc).FirstError()
}
