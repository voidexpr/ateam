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
}

var verifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Supervisor verifies recent code changes from `ateam code`",
	Long: `Have the supervisor inspect commits made by the most recent ` + "`ateam code`" + ` run,
look for logical bugs, broken or missing tests, and risky changes, then run
the project's test suite and record findings in a verification report.

Run after ` + "`ateam code`" + ` (or use ` + "`ateam code --verify`" + ` /
` + "`ateam all --verify`" + ` to chain it automatically).

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
		})
	},
}

func init() {
	verifyCmd.Flags().StringVar(&verifyExtraPrompt, "extra-prompt", "", "additional instructions (text or @filepath)")
	verifyCmd.Flags().IntVar(&verifyTimeout, "timeout", 0, "timeout in minutes (overrides config)")
	verifyCmd.Flags().BoolVar(&verifyPrint, "print", false, "print verification report to stdout after completion")
	verifyCmd.Flags().BoolVar(&verifyDryRun, "dry-run", false, "print the computed prompt without running")
	addCheaperModelFlag(verifyCmd, &verifyCheaperModel)
	addProfileFlags(verifyCmd, &verifyProfile, &verifyAgent)
	addVerboseFlag(verifyCmd, &verifyVerbose)
	addForceFlag(verifyCmd, &verifyForce)
	addDockerAutoSetupFlag(verifyCmd, &verifyDockerAutoSetup)
	addContainerNameFlag(verifyCmd, &verifyContainerName)
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

	verifyFile := env.VerifyPath()
	supervisorDir := env.SupervisorDir()
	historyDir := env.ReviewHistoryDir()

	startedAt := time.Now()
	prompt, outputFile := prepareOutputFile(prompt, historyDir, "verify.md", startedAt)

	if opts.DryRun {
		fmt.Printf("╔══ verify ══╗\n\n")
		fmt.Println(prompt)
		fmt.Printf("\n╚══ verify ══╝\n")
		return nil
	}

	if err := os.MkdirAll(historyDir, 0755); err != nil {
		return fmt.Errorf("cannot create supervisor history directory: %w", err)
	}

	fmt.Printf("Supervisor verifying recent code changes (%dm timeout)...\n", timeout)

	cr, err := resolveRunner(env, opts.Profile, opts.Agent, runner.ActionVerify, "", opts.DockerAutoSetup)
	if err != nil {
		return err
	}
	if err := applyContainerName(cr, env, opts.ContainerName); err != nil {
		return err
	}
	setSourceWritable(cr)
	applyCheaperModel(cr, opts.CheaperModel)

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
		RoleID:               "supervisor",
		Action:               runner.ActionVerify,
		LogsDir:              env.SupervisorLogsDir(),
		LastMessageFilePath:  verifyFile,
		OutputFilePath:       outputFile,
		ErrorMessageFilePath: filepath.Join(supervisorDir, "verify_error.md"),
		WorkDir:              env.SourceDir,
		TimeoutMin:           timeout,
		HistoryDir:           historyDir,
		PromptName:           prompts.CodeVerifyPromptFile,
		Verbose:              opts.Verbose,
		StartedAt:            startedAt,
	}

	ctx, stop := cmdContext()
	defer stop()
	result := cr.Run(ctx, prompt, runOpts, nil)

	if result.Err != nil {
		return fmt.Errorf("verify failed: %w", result.Err)
	}

	printDone(result)
	fmt.Printf("Verification report: %s\n", verifyFile)

	if opts.Print {
		fmt.Printf("\n%s\n", result.Output)
	}

	return nil
}
