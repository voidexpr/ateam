package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/ateam/internal/display"
	"github.com/ateam/internal/flow"
	"github.com/ateam/internal/flow/actions"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/ateam/internal/runtime"
	"github.com/spf13/cobra"
)

var (
	codeReview            string
	codeManagement        string
	codeExtraPrompt       string
	codePrePrompt         string
	codePostPrompt        string
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
	codeDockerAutoSetup   bool
	codeContainerName     string
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
	PrePrompt         string
	PostPrompt        string
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
	DockerAutoSetup   bool
	ContainerName     string
	Model             string
	Effort            string
	MaxBudgetUSD      string
	MaxBudgetBatch    string
}

var codeCmd = &cobra.Command{
	Use:   "code",
	Short: "Execute review tasks as code changes",
	Long: `Read the review document and execute prioritized tasks as code changes,
delegating each coding task to the appropriate role via ateam exec. The
command stops after the code phase — run ateam verify (or ateam all) to
inspect the resulting commits and run the test suite.

Example:
  ateam code
  ateam code --review @custom_review.md
  ateam code --management @custom_management.md
  ateam code --dry-run
  ateam code && ateam verify                     # explicit verify follow-up
  ateam all                                      # full pipeline incl. verify`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runCode(CodeOptions{
			Review:            codeReview,
			Management:        codeManagement,
			ExtraPrompt:       codeExtraPrompt,
			PrePrompt:         codePrePrompt,
			PostPrompt:        codePostPrompt,
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
			DockerAutoSetup:   codeDockerAutoSetup,
			ContainerName:     codeContainerName,
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
		"additional instructions (text or @filepath); appended after Review, before Sub-Run Flags")
	codeCmd.Flags().StringVar(&codePrePrompt, "pre-prompt", "",
		"text wrapped at the very front of the supervisor prompt (text or @filepath)")
	codeCmd.Flags().StringVar(&codePostPrompt, "post-prompt", "",
		"text wrapped at the very end of the supervisor prompt, after Sub-Run Flags (text or @filepath)")
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
	addDockerAutoSetupFlag(codeCmd, &codeDockerAutoSetup)
	addContainerNameFlag(codeCmd, &codeContainerName)
}

func runCode(opts CodeOptions) error {
	env, err := resolveEnv()
	if err != nil {
		return err
	}
	if err := requireGitRepo(env, runner.ActionCode); err != nil {
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
	prePrompt, err := prompts.ResolveOptional(opts.PrePrompt)
	if err != nil {
		return err
	}
	postPrompt, err := prompts.ResolveOptional(opts.PostPrompt)
	if err != nil {
		return err
	}

	batch := "code-" + time.Now().Format(display.TimestampFormat)

	// Resolve sub-run profile/agent once — used for both prompt injection and DinD check.
	// --agent and --profile are mutually exclusive on ateam exec.
	subRunProfile := opts.Profile
	if subRunProfile == "" && opts.Agent == "" {
		subRunProfile = env.Config.ResolveProfile(runner.ActionExec, "")
	}

	// --project is required so sub-execs resolve the right .ateam directory
	// even when the supervisor's cwd is outside the project tree (remote-mode
	// `ateam code --project /elsewhere`). In project-local mode the value is
	// redundant but harmless. shellQuoteSingle keeps paths with spaces or
	// shell-significant chars intact when the supervisor templates them into
	// `ateam exec` commands.
	subRunFlags := SubRunFlags{
		Batch:          batch,
		ProjectDir:     shellQuoteSingle(env.SourceDir),
		Agent:          opts.Agent,
		Profile:        subRunProfile,
		Model:          opts.Model,
		Effort:         opts.Effort,
		MaxBudgetUSD:   opts.MaxBudgetUSD,
		MaxBudgetBatch: opts.MaxBudgetBatch,
	}

	// Both default and --prompt paths now go through assembleCodeManagementV1;
	// the override (customManagement) flows into the assembler's
	// ReplaceRoleMain option so framing fragments compose either way.
	prompt, err := assembleCodeManagementV1(env, "the supervisor", reviewContent, subRunFlags, extraPrompt, customManagement, prePrompt, postPrompt)
	if err != nil {
		return err
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

	db, err := openStateDB(env)
	if err != nil {
		return err
	}
	defer db.Close()
	cr.CallDB = db

	ctx, stop := cmdContext()
	defer stop()

	bundle := flow.PromptBundle{
		Name:   "code",
		Role:   "supervisor",
		Action: runner.ActionCode,
		Render: func(flow.RuntimeEnv) (string, error) {
			return prompt, nil
		},
		RunOpts: func(flow.RuntimeEnv) runner.RunOpts {
			return runner.RunOpts{
				RoleID:           "supervisor",
				Action:           runner.ActionCode,
				OutputKind:       runner.OutputKindExecutionReport,
				CanonicalDestDir: filepath.Join(env.SharedDir(), "code", "{{EXEC_ID}}"),
				WorkDir:          env.WorkDir,
				TimeoutMin:       timeout,
				Verbose:          opts.Verbose,
				Batch:            batch,
				StartedAt:        startedAt,
			}
		},
		PreExec: []flow.Action{
			actions.CheckConcurrentRuns{If: !opts.Force, Action: runner.ActionCode},
		},
		PostExec: []flow.Action{
			printCodeSessionAction{
				SharedDir:     env.SharedDir(),
				SupervisorDir: supervisorDir,
				Print:         opts.Print,
			},
		},
	}
	rtEnv := flow.RuntimeEnv{
		Executor: cr,
		WorkDir:  env.WorkDir,
		Role:     "supervisor",
		Action:   runner.ActionCode,
		Batch:    batch,
	}
	rc := flow.RunCtx{
		Ctx:      ctx,
		DB:       db,
		Resolved: env,
		Reporter: &flow.StdoutReporter{Stream: true},
	}
	return flow.Run(bundle, rtEnv, rc).FirstError()
}

// printCodeSessionAction is a code-specific Post action that emits the
// per-session summary (execution_report.md contents, session dir, file
// list). Lives here rather than in internal/flow/actions because it
// only makes sense for `ateam code` — other bundles have flat single-
// file artifacts that PrintArtifactPath/PrintArtifactBody handle.
type printCodeSessionAction struct {
	SharedDir     string
	SupervisorDir string
	Print         bool
}

func (a printCodeSessionAction) Run(_ flow.RunCtx, _ flow.RuntimeEnv, res *flow.Result) flow.Flow {
	if res == nil || res.Summary == nil {
		return flow.Flow{State: flow.StateContinue}
	}
	printCodeSessionSummary(a.SharedDir, a.SupervisorDir, res.Summary.ExecID, a.Print, res.Summary.Output)
	return flow.Flow{State: flow.StateContinue}
}

func printCodeSessionSummary(sharedDir, supervisorDir string, execID int64, printOutput bool, output string) {
	cwd, _ := os.Getwd()
	lastMsg := relPath(cwd, filepath.Join(supervisorDir, "code_output.md"))

	// New runs write to shared/code/<id>/; auto-migration moves any
	// pre-Step-4 supervisor/code/<id>/ trees ahead of this read.
	var sessionDir string
	if execID > 0 {
		candidate := filepath.Join(sharedDir, "code", strconv.FormatInt(execID, 10))
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			sessionDir = candidate
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
