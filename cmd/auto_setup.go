package cmd

import (
	"fmt"
	"os"
	"path/filepath"
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

	cr, err := resolveRunner(env, autoSetupProfile, autoSetupAgent, runner.ActionRun, "", false)
	if err != nil {
		return err
	}
	setSourceWritable(cr)

	db := openProjectDB(env)
	if db != nil {
		defer db.Close()
		cr.CallDB = db
	}

	supervisorDir := env.SupervisorDir()
	historyDir := env.ReviewHistoryDir()
	if err := os.MkdirAll(historyDir, 0755); err != nil {
		return fmt.Errorf("cannot create history directory: %w", err)
	}

	opts := runner.RunOpts{
		RoleID:               "supervisor",
		Action:               runner.ActionRun,
		LogsDir:              env.SupervisorLogsDir(),
		WorkDir:              env.SourceDir,
		TimeoutMin:           timeout,
		HistoryDir:           historyDir,
		PromptName:           "auto_setup_prompt.md",
		Verbose:              autoSetupVerbose,
		LastMessageFilePath:  filepath.Join(supervisorDir, "auto_setup_output.md"),
		ErrorMessageFilePath: filepath.Join(supervisorDir, "auto_setup_error.md"),
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
