package cmd

import (
	"fmt"
	"sync"

	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/spf13/cobra"
)

var (
	autoSetupProfile string
	autoSetupAgent   string
	autoSetupVerbose bool
	autoSetupDryRun  bool
	autoSetupTimeout int
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
}

func runAutoSetup(cmd *cobra.Command, args []string) error {
	env, err := root.Resolve(orgFlag, projectFlag)
	if err != nil {
		return err
	}

	pinfo := env.NewProjectInfoParams("the supervisor", "auto-setup")
	prompt, err := prompts.AssembleAutoSetupPrompt(env.OrgDir, env.ProjectDir, pinfo)
	if err != nil {
		return err
	}

	if autoSetupDryRun {
		fmt.Printf("╔══ auto-setup ══╗\n\n")
		fmt.Println(prompt)
		fmt.Printf("\n╚══ auto-setup ══╝\n")
		return nil
	}

	timeout := env.Config.Review.EffectiveTimeout(autoSetupTimeout)

	fmt.Printf("Auto-setup running (%dm timeout)...\n", timeout)

	cr, err := resolveRunner(env, autoSetupProfile, autoSetupAgent, runner.ActionExec, "", false)
	if err != nil {
		return err
	}
	setSourceWritable(cr)

	db, err := openProjectDB(env)
	if err != nil {
		return err
	}
	defer db.Close()
	cr.CallDB = db

	// auto_setup_prompt.md still hardcodes `.ateam/setup_overview.md` instead
	// of using {{OUTPUT_FILE}}; the agent writes there directly. No runtime/
	// canonical promotion is configured here. See plan for the eventual fix.
	opts := runner.RunOpts{
		RoleID:     "supervisor",
		Action:     runner.ActionExec,
		WorkDir:    env.SourceDir,
		TimeoutMin: timeout,
		Verbose:    autoSetupVerbose,
	}

	progress := make(chan runner.RunProgress, 64)
	var progressWg sync.WaitGroup
	progressWg.Add(1)
	go func() {
		defer progressWg.Done()
		printProgress(progress)
	}()

	ctx, stop := cmdContext()
	defer stop()
	result := cr.Run(ctx, prompt, opts, progress)

	close(progress)
	progressWg.Wait()

	if result.Err != nil {
		return fmt.Errorf("auto-setup failed: %w", result.Err)
	}

	printDone(result)

	if result.Output != "" {
		fmt.Printf("\n%s\n", result.Output)
	}

	return nil
}
