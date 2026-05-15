package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/ateam/internal/runtime"
	"github.com/spf13/cobra"
)

// tailAttachDelay gives the supervisor goroutine a head start before the
// tailer begins polling, so the first stream/log files exist when AddSource
// runs. 300ms is long enough for the runner to create the call-DB row and
// open the stream file, and short enough that interactive output still feels
// immediate.
const tailAttachDelay = 300 * time.Millisecond

var (
	codeReview            string
	codeManagement        string
	codeExtraPrompt       string
	codeTimeout           int
	codePrint             bool
	codeDryRun            bool
	codeCheaperModel      bool
	codeProfile           string
	codeAgent             string
	codeSupervisorProfile string
	codeSupervisorAgent   string
	codeVerbose           bool
	codeForce             bool
	codeTail              bool
	codeDockerAutoSetup   bool
	codeContainerName     string
	codeNoVerify          bool
	codeModel             string
	codeEffort            string
	codeMaxBudgetUSD      string
	codeMaxBudgetBatch    string
)

// CodeOptions holds configuration for a code run.
type CodeOptions struct {
	Review            string
	Management        string
	ExtraPrompt       string
	Timeout           int
	Print             bool
	DryRun            bool
	CheaperModel      bool
	Profile           string // sub-run profile (--profile on ateam exec)
	Agent             string // sub-run agent (--agent on ateam exec, mutually exclusive with Profile)
	SupervisorProfile string
	SupervisorAgent   string
	Verbose           bool
	Force             bool
	Tail              bool
	DockerAutoSetup   bool
	ContainerName     string
	NoVerify          bool // skip the default `ateam verify` follow-up
	Model             string
	Effort            string
	MaxBudgetUSD      string
	MaxBudgetBatch    string
}

var codeCmd = &cobra.Command{
	Use:   "code",
	Short: "Execute review tasks as code changes (followed by verify)",
	Long: `Read the review document and execute prioritized tasks as code changes,
delegating each coding task to the appropriate role via ateam exec. After the
code phase succeeds, automatically chain ateam verify to inspect the resulting
commits and run the test suite. Pass --no-verify to skip that follow-up.

Example:
  ateam code
  ateam code --no-verify                         # stop after the code phase
  ateam code --review @custom_review.md
  ateam code --management @custom_management.md
  ateam code --dry-run`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runCode(CodeOptions{
			Review:            codeReview,
			Management:        codeManagement,
			ExtraPrompt:       codeExtraPrompt,
			Timeout:           codeTimeout,
			Print:             codePrint,
			DryRun:            codeDryRun,
			CheaperModel:      codeCheaperModel,
			Profile:           codeProfile,
			Agent:             codeAgent,
			SupervisorProfile: codeSupervisorProfile,
			SupervisorAgent:   codeSupervisorAgent,
			Verbose:           codeVerbose,
			Force:             codeForce,
			Tail:              codeTail,
			DockerAutoSetup:   codeDockerAutoSetup,
			ContainerName:     codeContainerName,
			NoVerify:          codeNoVerify,
			Model:             codeModel,
			Effort:            codeEffort,
			MaxBudgetUSD:      codeMaxBudgetUSD,
			MaxBudgetBatch:    codeMaxBudgetBatch,
		})
	},
}

func init() {
	codeCmd.Flags().StringVar(&codeReview, "review", "",
		"review content (text or @filepath; defaults to .ateam/supervisor/review.md)")
	codeCmd.Flags().StringVar(&codeManagement, "management", "",
		"management prompt override (text or @filepath)")
	codeCmd.Flags().StringVar(&codeExtraPrompt, "extra-prompt", "",
		"additional instructions (text or @filepath)")
	codeCmd.Flags().IntVar(&codeTimeout, "timeout", 0,
		"timeout in minutes (overrides config)")
	codeCmd.Flags().BoolVar(&codePrint, "print", false,
		"print output to stdout after completion")
	codeCmd.Flags().BoolVar(&codeDryRun, "dry-run", false,
		"print the computed prompt without running")
	addCheaperModelFlag(codeCmd, &codeCheaperModel)
	codeCmd.Flags().StringVar(&codeProfile, "profile", "", "profile for sub-runs (passed to ateam exec --profile)")
	codeCmd.Flags().StringVar(&codeAgent, "agent", "", "agent for sub-runs (passed to ateam exec --agent)")
	codeCmd.Flags().StringVar(&codeModel, "model", "",
		"model override for the supervisor and every sub-run; takes precedence over --cheaper-model")
	codeCmd.Flags().StringVar(&codeEffort, "effort", "", "reasoning effort for the supervisor and every sub-run, passed verbatim to the agent CLI")
	addBudgetFlags(codeCmd, &codeMaxBudgetUSD, &codeMaxBudgetBatch,
		"USD spend cap for the supervisor and every sub-run (claude-only)",
		"stop spawning new sub-runs once the code batch crosses this USD")
	codeCmd.Flags().StringVar(&codeSupervisorProfile, "supervisor-profile", "", "profile for the supervisor itself")
	codeCmd.Flags().StringVar(&codeSupervisorAgent, "supervisor-agent", "", "agent for the supervisor itself")
	codeCmd.MarkFlagsMutuallyExclusive("profile", "agent")
	codeCmd.MarkFlagsMutuallyExclusive("supervisor-profile", "supervisor-agent")
	addVerboseFlag(codeCmd, &codeVerbose)
	addForceFlag(codeCmd, &codeForce)
	codeCmd.Flags().BoolVar(&codeTail, "tail", false, "stream live output from supervisor and sub-runs")
	codeCmd.Flags().BoolVar(&codeNoVerify, "no-verify", false, "skip the verify phase that normally runs after code completes")
	addDockerAutoSetupFlag(codeCmd, &codeDockerAutoSetup)
	addContainerNameFlag(codeCmd, &codeContainerName)
}

func runCode(opts CodeOptions) error {
	env, err := resolveEnv()
	if err != nil {
		return err
	}
	if err := requireGitRepo(env, "code"); err != nil {
		return err
	}

	var reviewContent string
	if opts.Review == "" {
		reviewPath := env.ReviewPath()
		data, err := os.ReadFile(reviewPath)
		if err != nil {
			return errNoReview(reviewPath)
		}
		reviewContent = string(data)
	} else {
		reviewContent, err = prompts.ResolveValue(opts.Review)
		if err != nil {
			return err
		}
	}

	customManagement, err := prompts.ResolveOptional(opts.Management)
	if err != nil {
		return err
	}

	extraPrompt, err := prompts.ResolveOptional(opts.ExtraPrompt)
	if err != nil {
		return err
	}

	batch := "code-" + time.Now().Format(runner.TimestampFormat)

	pinfo := env.NewProjectInfoParams("the supervisor", "code")
	prompt, err := prompts.AssembleCodeManagementPrompt(env.OrgDir, env.ProjectDir, env.WorkDir, pinfo, reviewContent, customManagement, extraPrompt)
	if err != nil {
		return err
	}

	// Resolve sub-run profile/agent once — used for both prompt injection and DinD check.
	// --agent and --profile are mutually exclusive on ateam exec.
	subRunProfile := opts.Profile
	if subRunProfile == "" && opts.Agent == "" {
		subRunProfile = env.Config.ResolveProfile(runner.ActionExec, "")
	}

	// Inject flags for the supervisor to pass to sub-runs.
	prompt += "\n\n# Sub-Run Flags\n\nYou MUST pass the following flags to every `ateam exec` command you execute:\n"
	prompt += "- `--batch " + batch + "` (groups all sub-execs for cost tracking)\n"
	// --project is required so sub-execs resolve the right .ateam directory
	// even when the supervisor's cwd is outside the project tree (remote-mode
	// `ateam code --project /elsewhere`). In project-local mode the value is
	// redundant but harmless. shellQuoteSingle keeps paths with spaces or
	// shell-significant chars intact when the supervisor templates them into
	// `ateam exec` commands.
	prompt += "- `--project " + shellQuoteSingle(env.SourceDir) + "`\n"
	if opts.Agent != "" {
		prompt += "- `--agent " + opts.Agent + "`\n"
	} else {
		prompt += "- `--profile " + subRunProfile + "`\n"
	}
	if opts.Model != "" {
		prompt += "- `--model " + opts.Model + "`\n"
	}
	if opts.Effort != "" {
		prompt += "- `--effort " + opts.Effort + "`\n"
	}
	if opts.MaxBudgetUSD != "" {
		prompt += "- `--max-budget-usd " + opts.MaxBudgetUSD + "`\n"
	}
	if opts.MaxBudgetBatch != "" {
		prompt += "- `--max-budget-usd-batch " + opts.MaxBudgetBatch + "`\n"
	}

	timeout := env.Config.Code.EffectiveTimeout(opts.Timeout)
	supervisorDir := env.SupervisorDir()

	startedAt := time.Now()

	if opts.DryRun {
		fmt.Printf("╔══ code management ══╗\n\n")
		fmt.Println(prompt)
		fmt.Printf("\n╚══ code management ══╝\n")
		return nil
	}

	fmt.Printf("Code management supervisor running (%dm timeout)...\n", timeout)

	supervisorProfileName := opts.SupervisorProfile
	if supervisorProfileName == "" && opts.SupervisorAgent == "" {
		supervisorProfileName = env.Config.ResolveSupervisorProfile(runner.ActionCode)
	}

	if err := checkDockerInDocker(env, supervisorProfileName, subRunProfile); err != nil {
		return err
	}

	cr, err := resolveRunner(env, supervisorProfileName, opts.SupervisorAgent, runner.ActionCode, "", opts.DockerAutoSetup)
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
	}, runner.ActionCode); err != nil {
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
		if err := checkConcurrentRunsEnv(db, env, runner.ActionCode, nil); err != nil {
			return err
		}
	}
	runOpts := runner.RunOpts{
		RoleID:           "supervisor",
		Action:           runner.ActionCode,
		OutputKind:       runner.OutputKindExecutionReport,
		CanonicalDestDir: filepath.Join(supervisorDir, "code", "{{EXEC_ID}}"),
		WorkDir:          env.WorkDir,
		TimeoutMin:       timeout,
		Verbose:          opts.Verbose,
		Batch:            batch,
		StartedAt:        startedAt,
	}

	ctx, stop := cmdContext()
	defer stop()

	var result runner.RunSummary
	if opts.Tail {
		runDone := make(chan struct{})
		go func() {
			result = cr.Run(ctx, prompt, runOpts, nil)
			close(runDone)
		}()

		time.Sleep(tailAttachDelay)

		tailer := runner.NewTailer(os.Stderr, db, isTerminal(), opts.Verbose)
		tailer.ProjectDir = env.ProjectDir
		tailer.OrgDir = env.OrgDir
		tailer.Batch = batch
		if rtCfg, err := runtime.Load(env.ProjectDir, env.OrgDir); err == nil {
			tailer.Pricing, tailer.DefaultModel = mergedPricingFromConfig(rtCfg)
		}

		if rows, err := db.CallsByBatch(batch); err == nil {
			for _, r := range rows {
				if r.StreamFile != "" {
					tailer.AddSource(r.ID, r.Role, r.Action, root.ResolveStreamPath(env.ProjectDir, env.OrgDir, r.StreamFile), r.Model)
				}
			}
		}

		tailCtx, tailCancel := context.WithCancel(ctx)
		go func() {
			<-runDone
			time.Sleep(time.Second)
			tailCancel()
		}()
		_ = tailer.Run(tailCtx)
		<-runDone
	} else {
		progress := make(chan runner.RunProgress, 64)
		var progressWg sync.WaitGroup
		progressWg.Add(1)
		go func() {
			defer progressWg.Done()
			printProgress(progress)
		}()

		result = cr.Run(ctx, prompt, runOpts, progress)

		close(progress)
		progressWg.Wait()
	}

	if result.Err != nil {
		return fmt.Errorf("code execution failed: %w", result.Err)
	}
	printCodeSessionSummary(supervisorDir, opts.Print, result.Output)
	printDone(result)

	if opts.NoVerify {
		return nil
	}

	fmt.Println()
	return runVerify(VerifyOptions{
		ExtraPrompt:     opts.ExtraPrompt,
		Timeout:         opts.Timeout,
		Print:           opts.Print,
		CheaperModel:    opts.CheaperModel,
		Profile:         opts.SupervisorProfile,
		Agent:           opts.SupervisorAgent,
		Verbose:         opts.Verbose,
		Force:           opts.Force,
		DockerAutoSetup: opts.DockerAutoSetup,
		ContainerName:   opts.ContainerName,
		Model:           opts.Model,
		Effort:          opts.Effort,
	})
}

func printCodeSessionSummary(supervisorDir string, printOutput bool, output string) {
	cwd, _ := os.Getwd()
	lastMsg := relPath(cwd, filepath.Join(supervisorDir, "code_output.md"))

	entries, _ := os.ReadDir(filepath.Join(supervisorDir, "code"))
	var sessionDir string
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].IsDir() {
			sessionDir = filepath.Join(supervisorDir, "code", entries[i].Name())
			break
		}
	}

	if sessionDir == "" {
		fmt.Printf("Last message: %s\n", lastMsg)
		if printOutput {
			fmt.Printf("\n%s\n", output)
		}
		return
	}

	reportFile := filepath.Join(sessionDir, "execution_report.md")
	if data, err := os.ReadFile(reportFile); err == nil {
		fmt.Printf("%s\n", data)
	}

	fmt.Printf("Last message: %s\n", lastMsg)

	fmt.Printf("Session: %s\n", relPath(cwd, sessionDir))
	taskEntries, _ := os.ReadDir(sessionDir)
	for _, e := range taskEntries {
		if e.IsDir() || e.Name() == "current_task.md" {
			continue
		}
		fmt.Printf("  %s\n", e.Name())
	}

	if printOutput {
		fmt.Printf("\n%s\n", output)
	}
}

// checkDockerInDocker returns an error if both the supervisor and sub-run profiles
// resolve to docker containers, since we don't support docker-in-docker yet.
func checkDockerInDocker(env *root.ResolvedEnv, supervisorProfile, subRunProfile string) error {
	if supervisorProfile == "" && subRunProfile == "" {
		return nil
	}
	rtCfg, err := runtime.Load(env.ProjectDir, env.OrgDir)
	if err != nil {
		return nil // let the runner resolution surface this error later
	}
	isDocker := func(profileName string) bool {
		if profileName == "" {
			return false
		}
		_, _, cc, err := rtCfg.ResolveProfile(profileName)
		if err != nil {
			return false
		}
		return cc != nil && cc.Type == "docker"
	}
	if isDocker(supervisorProfile) && isDocker(subRunProfile) {
		return fmt.Errorf("docker-in-docker is not supported: both --supervisor-profile %q and --profile %q use docker containers", supervisorProfile, subRunProfile)
	}
	return nil
}
