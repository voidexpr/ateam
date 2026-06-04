package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ateam/internal/flow"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/ateam/internal/runtime"
	"github.com/spf13/cobra"
)

var (
	codeFlags             CommonExecFlags
	codeReview            string
	codePrint             bool
	codeDryRun            bool
	codeSupervisorProfile string
	codeSupervisorAgent   string
	codeForce             bool
	codeMaxBudgetBatch    string
)

// CodeOptions holds configuration for a code run. CommonExecFlags is embedded
// so the 13 shared fields (Profile, Model, Effort, etc.) are reachable via
// promoted-field access (e.g. opts.Profile). Code's --profile/--agent describe
// the sub-run, not the supervisor — the supervisor uses SupervisorProfile /
// SupervisorAgent below.
type CodeOptions struct {
	CommonExecFlags
	Review            string
	Print             bool
	DryRun            bool
	SupervisorProfile string
	SupervisorAgent   string
	Force             bool
	MaxBudgetBatch    string
}

var codeCmd = &cobra.Command{
	Use:   "code",
	Short: "Execute review tasks as code changes",
	Long: `Read the review document and execute prioritized tasks as code changes,
delegating each coding task to the appropriate role via ateam exec. The
command stops after the code phase — run ateam verify (or ateam run-all) to
inspect the resulting commits and run the test suite.

Operators who want to override the management body place a custom
code_management.prompt.md under .ateam/prompts/ (project anchor) or
.ateamorg/prompts/ (org anchor); the standard framing still composes
around it.

Example:
  ateam code
  ateam code --review @custom_review.md
  ateam code --dry-run
  ateam code && ateam verify                     # explicit verify follow-up
  ateam run-all                                  # full pipeline incl. verify`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runCode(CodeOptions{
			CommonExecFlags:   codeFlags,
			Review:            codeReview,
			Print:             codePrint,
			DryRun:            codeDryRun,
			SupervisorProfile: codeSupervisorProfile,
			SupervisorAgent:   codeSupervisorAgent,
			Force:             codeForce,
			MaxBudgetBatch:    codeMaxBudgetBatch,
		})
	},
}

func init() {
	codeCmd.Flags().StringVar(&codeReview, "review", "",
		"review content (text or @filepath; defaults to .ateam/supervisor/review.md)")
	registerCommonExecFlags(codeCmd, &codeFlags, commonFlagUsage{
		Timeout:       "timeout in minutes (overrides config)",
		Model:         "model override for the supervisor and every sub-run; takes precedence over --cheaper-model",
		Effort:        "reasoning effort for the supervisor and every sub-run, passed verbatim to the agent CLI",
		MaxBudgetUSD:  "USD spend cap for the supervisor and every sub-run (claude-only)",
		CustomProfile: "profile for sub-runs (passed to ateam exec --profile)",
		CustomAgent:   "agent for sub-runs (passed to ateam exec --agent)",
	})
	codeCmd.Flags().BoolVar(&codePrint, "print", false,
		"print output to stdout after completion")
	codeCmd.Flags().BoolVar(&codeDryRun, "dry-run", false,
		"print the computed prompt without running")
	codeCmd.Flags().StringVar(&codeMaxBudgetBatch, "max-budget-usd-batch", "",
		"stop spawning new sub-runs once the code batch crosses this USD")
	codeCmd.Flags().StringVar(&codeSupervisorProfile, "supervisor-profile", "", "profile for the supervisor itself")
	codeCmd.Flags().StringVar(&codeSupervisorAgent, "supervisor-agent", "", "agent for the supervisor itself")
	codeCmd.MarkFlagsMutuallyExclusive("supervisor-profile", "supervisor-agent")
	addForceFlag(codeCmd, &codeForce)
}

func runCode(opts CodeOptions) error {
	env, err := resolveEnv()
	if err != nil {
		return err
	}
	if err := requireGitRepo(env, runner.ActionCode); err != nil {
		return err
	}

	// Validate review.md exists (or operator override resolves) up-front so
	// errNoReview surfaces before flow setup. The dynamic
	// (code_mgmt_review) loads the content again at Resolve time — single
	// substitution pass per spec — but the verb owes the operator a
	// fast-fail before any prompt assembly.
	var reviewContent string
	if opts.Review == "" {
		if _, err := os.Stat(env.ReviewPath()); err != nil {
			return errNoReview(env.ReviewPath())
		}
	} else {
		reviewContent, err = prompts.ResolveValue(opts.Review)
		if err != nil {
			return err
		}
	}

	prePrompt, err := prompts.ResolveOptional(opts.PrePrompt)
	if err != nil {
		return err
	}
	postPrompt, err := prompts.ResolveOptional(opts.PostPrompt)
	if err != nil {
		return err
	}

	batch := resolveBatch("", "code")

	// Resolve sub-run profile/agent once — used here for the DinD check and
	// folded into subRunArgs so the supervisor pastes the full --profile /
	// --agent / --project / --org / etc. fragment into each `ateam exec`.
	// --agent and --profile are mutually exclusive on ateam exec.
	subRunProfile := opts.Profile
	if subRunProfile == "" && opts.Agent == "" {
		subRunProfile = env.Config.ResolveProfile(runner.ActionExec, "")
	}
	subRunArgs := buildSubRunArgs(opts, subRunProfile, env.SourceDir, orgFlag, workDirFlag)

	timeout := env.Config.Code.EffectiveTimeout(opts.Timeout)
	supervisorDir := env.SupervisorDir()

	startedAt := time.Now()

	supervisorProfileName := opts.SupervisorProfile
	if supervisorProfileName == "" && opts.SupervisorAgent == "" {
		supervisorProfileName = env.Config.ResolveSupervisorProfile(runner.ActionCode)
	}

	if err := checkDockerInDocker(env, supervisorProfileName, subRunProfile); err != nil {
		return err
	}

	// Build the supervisor runner even in dry-run so the preview goes through
	// the same TemplateVars + ResolveTemplateString machinery the live exec
	// would use. buildRunner doesn't execute anything; it just constructs an
	// AgentExecutor. Side benefit: dry-run now surfaces profile/agent
	// resolution errors early.
	cr, err := buildRunner(env, RunnerSpec{
		Profile:         supervisorProfileName,
		Agent:           opts.SupervisorAgent,
		Action:          runner.ActionCode,
		DockerAutoSetup: opts.DockerAutoSetup,
		Overrides: RunnerOverrides{
			ContainerName:     opts.ContainerName,
			CheaperModel:      opts.CheaperModel,
			Model:             opts.Model,
			Effort:            opts.Effort,
			MaxBudgetUSD:      opts.MaxBudgetUSD,
			MaxBudgetUSDBatch: opts.MaxBudgetBatch,
		},
	})
	if err != nil {
		return err
	}
	cr.SubRunArgs = subRunArgs

	bundle := NewCodeBundle(CodeBundleInput{
		Env:           env,
		ReviewContent: reviewContent,
		PrePrompt:     prePrompt,
		PostPrompt:    postPrompt,
		Batch:         batch,
		TimeoutMin:    timeout,
		Verbose:       opts.Verbose,
		Force:         opts.Force,
		Print:         opts.Print,
		StartedAt:     startedAt,
		SharedDir:     env.SharedDir(),
		SupervisorDir: supervisorDir,
		// Per-exec_id canonical dest. The runner's Replacer still
		// expands {{EXEC_ID}} inside opts.CanonicalDestDir at
		// ExecutePrepared time (that's args/path territory, not prompt
		// body), so the literal template stays here.
		CanonicalDest: filepath.Join(env.SharedDir(), "code", "{{EXEC_ID}}"),
	})

	if opts.DryRun {
		// Dry-run resolves the prompt body inline so operators see the
		// final composition before any allocation. Mode=ModePreview
		// means exec.* keys render as the AT RUNTIME sentinel pattern
		// and code_mgmt_review renders its sentinel — matching the spec
		// (line 552-557).
		rt := flow.NewRuntime(nil, env, env.WorkDir)
		if bundle.BaseVars != nil {
			rt.SetVars(bundle.BaseVars)
		}
		if bundle.Dynamics != nil {
			rt.SetDynamics(bundle.Dynamics)
		}
		text, err := bundle.Prompt.Resolve(rt)
		if err != nil {
			return err
		}
		fmt.Printf("╔══ code management ══╗\n\n")
		fmt.Println(text)
		fmt.Printf("\n╚══ code management ══╝\n")
		return nil
	}

	fmt.Printf("Code management supervisor running (%dm timeout)...\n", timeout)

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
		Action:   runner.ActionCode,
		Batch:    batch,
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
	return flow.Run(*bundle, rtEnv, rc).FirstError()
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

// buildSubRunArgs renders the {{exec.subrun_args}} fragment supervisor prompts
// paste verbatim into each `ateam exec`. Positive list — propagate every
// `ateam code` flag that's meaningful for an exec sub-run. The classified
// exclusions (supervisor-only flags, mode controls, output controls) are
// intentionally absent; keep them off this list when adding new options:
//
//	NEVER propagate:
//	  --supervisor-profile / --supervisor-agent (definitionally supervisor-only)
//	  --management / --review                   (supervisor inputs)
//	  --timeout                                 (supervisor's own clock)
//	  --dry-run / --force / --print             (mode controls)
//	  --verbose / --no-stream / --no-summary    (supervisor I/O)
//	  --quiet / --format / --progress-fd        (supervisor I/O)
//	  --agent-args                              (supervisor-specific extras)
//	  --docker-auto-setup                       (already ran for the parent;
//	                                             sub-runs reuse those containers)
//
// Agent/profile are mutually exclusive (--agent wins, matching the CLI).
// Project path is shell-quoted because it can contain spaces or shell-
// significant chars; other fields are CLI tokens without spaces in practice.
func buildSubRunArgs(opts CodeOptions, subRunProfile, sourceDir, orgPath, workDir string) string {
	var parts []string
	switch {
	case opts.Agent != "":
		parts = append(parts, "--agent", opts.Agent)
	case subRunProfile != "":
		parts = append(parts, "--profile", subRunProfile)
	}
	if opts.CheaperModel {
		parts = append(parts, "--cheaper-model")
	}
	if opts.ContainerName != "" {
		parts = append(parts, "--container-name", opts.ContainerName)
	}
	if opts.Effort != "" {
		parts = append(parts, "--effort", opts.Effort)
	}
	if opts.Model != "" {
		parts = append(parts, "--model", opts.Model)
	}
	if opts.MaxBudgetUSD != "" {
		parts = append(parts, "--max-budget-usd", opts.MaxBudgetUSD)
	}
	if opts.MaxBudgetBatch != "" {
		parts = append(parts, "--max-budget-usd-batch", opts.MaxBudgetBatch)
	}
	if sourceDir != "" {
		parts = append(parts, "--project", shellQuoteSingle(sourceDir))
	}
	if orgPath != "" {
		parts = append(parts, "--org", shellQuoteSingle(orgPath))
	}
	if workDir != "" {
		parts = append(parts, "--work-dir", shellQuoteSingle(workDir))
	}
	return strings.Join(parts, " ")
}
