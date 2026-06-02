package cmd

import (
	"fmt"

	"github.com/ateam/internal/flow"
	"github.com/ateam/internal/flow/actions"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/runner"
	"github.com/spf13/cobra"
)

var (
	autoSetupProfile    string
	autoSetupAgent      string
	autoSetupVerbose    bool
	autoSetupDryRun     bool
	autoSetupTimeout    int
	autoSetupPrePrompt  string
	autoSetupPostPrompt string
)

var autoSetupCmd = &cobra.Command{
	Use:   "auto-setup",
	Short: "Auto-configure ateam for the current project",
	Long: `Run the supervisor to analyze the project, configure roles,
and recommend settings.

Requires an initialized project (.ateam/ directory).

Example:
  ateam auto-setup
  ateam auto-setup --dry-run
  ateam auto-setup --profile docker`,
	Args: cobra.NoArgs,
	RunE: runAutoSetup,
}

func init() {
	addProfileFlags(autoSetupCmd, &autoSetupProfile, &autoSetupAgent)
	addVerboseFlag(autoSetupCmd, &autoSetupVerbose)
	autoSetupCmd.Flags().BoolVar(&autoSetupDryRun, "dry-run", false, "print the prompt without running")
	autoSetupCmd.Flags().IntVar(&autoSetupTimeout, "timeout", 0, "timeout in minutes (overrides config)")
	addPromptWrapFlags(autoSetupCmd, &autoSetupPrePrompt, &autoSetupPostPrompt)
}

func runAutoSetup(cmd *cobra.Command, args []string) error {
	env, err := resolveEnv()
	if err != nil {
		return err
	}

	prePrompt, err := prompts.ResolveOptional(autoSetupPrePrompt)
	if err != nil {
		return err
	}
	postPrompt, err := prompts.ResolveOptional(autoSetupPostPrompt)
	if err != nil {
		return err
	}

	prompt, err := assembleSupervisor(env, "auto_setup", "the supervisor", "auto-setup", prePrompt, postPrompt)
	if err != nil {
		return err
	}

	if autoSetupDryRun {
		fmt.Printf("╔══ auto-setup ══╗\n\n")
		fmt.Println(prompt)
		fmt.Printf("\n╚══ auto-setup ══╝\n")
		return nil
	}

	// Auto-setup runs a supervisor pass like review/verify; reuses the review timeout helper.
	timeout := env.Config.Review.EffectiveTimeout(autoSetupTimeout)

	fmt.Printf("Auto-setup running (%dm timeout)...\n", timeout)

	cr, err := buildRunner(env, RunnerSpec{
		Profile: autoSetupProfile,
		Agent:   autoSetupAgent,
		Action:  runner.ActionExec,
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

	// The agent writes the overview directly to the v1 flat location
	// .ateam/shared/auto_setup.md; no runtime/canonical promotion is needed
	// here. Writing the v1 path (rather than the pre-v1 setup_overview.md or
	// the pre-flat shared/auto_setup/auto_setup.md) avoids re-triggering
	// layout migration on the next ateam command.
	bundle := flow.PromptBundle{
		Name:   "auto-setup",
		Role:   "supervisor",
		Action: runner.ActionExec,
		Render: func(flow.RuntimeEnv) (string, error) {
			return prompt, nil
		},
		RunOpts: func(flow.RuntimeEnv) runner.RunOpts {
			return runner.RunOpts{
				RoleID:     "supervisor",
				Action:     runner.ActionExec,
				WorkDir:    env.WorkDir,
				TimeoutMin: timeout,
				Verbose:    autoSetupVerbose,
			}
		},
		PostExec: []flow.Action{
			// No artifact file path — the agent's final message is the
			// overview's source-of-truth here (unlike review/verify which
			// rely on the file). Stream-only print mirrors the original
			// "if result.Output != \"\", print it" branch.
			actions.PrintArtifactBody{If: true, Path: ""},
		},
	}
	rtEnv := flow.RuntimeEnv{
		Executor: cr,
		WorkDir:  env.WorkDir,
		Role:     "supervisor",
		Action:   runner.ActionExec,
	}
	rc := flow.RunCtx{
		Ctx:      ctx,
		DB:       db,
		Resolved: env,
		Reporter: flow.MultiReporter{
			&flow.StdoutReporter{Stream: true},
			&flow.BundleLogReporter{},
		},
	}
	return flow.Run(bundle, rtEnv, rc).FirstError()
}
