package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/container"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/ateam/internal/runtime"
	"github.com/ateam/internal/secret"
	"github.com/spf13/cobra"
)

var (
	runRole            string
	runProfile         string
	runAgent           string
	runModel           string
	runNoStream        bool
	runWorkDir         string
	runNoSummary       bool
	runQuiet           bool
	runAgentArgs       string
	runVerbose         bool
	runTaskGroup       string
	runDockerAutoSetup bool
	runDryRun          bool
)

var runCmd = &cobra.Command{
	Use:   "run PROMPT_OR_@FILE",
	Short: "Run an agent with a prompt",
	Long: `Run an agent with the provided prompt text or file.
Can run standalone (just needs .ateamorg/) or within a project context.

With --role: validates the role exists and stores output in role directory.
Without --role: runs as ad-hoc, stores output in project or org logs.

Streaming and summary are on by default. Use --quiet to suppress both,
or --no-stream / --no-summary individually.

Example:
  ateam run "say hello"
  ateam run "Analyze the auth module" --role security
  ateam run "test" --profile cheap
  ateam run "say hi" --model sonnet
  ateam run "quick check" --quiet`,
	Args: cobra.ExactArgs(1),
	RunE: runRun,
}

func init() {
	runCmd.Flags().StringVar(&runRole, "role", "", "role to run (optional)")
	runCmd.Flags().StringVar(&runModel, "model", "", "model override")
	addProfileFlags(runCmd, &runProfile, &runAgent)
	runCmd.Flags().BoolVar(&runNoStream, "no-stream", false, "disable progress updates during execution")
	runCmd.Flags().BoolVar(&runNoSummary, "no-summary", false, "disable run summary after completion")
	runCmd.Flags().BoolVar(&runQuiet, "quiet", false, "disable both streaming and summary (same as --no-stream --no-summary)")
	runCmd.Flags().StringVar(&runWorkDir, "work-dir", "", "working directory (defaults to project source dir or cwd)")
	runCmd.Flags().StringVar(&runAgentArgs, "agent-args", "", "extra args passed to the agent CLI (appended after configured args)")
	runCmd.Flags().StringVar(&runTaskGroup, "task-group", "", "group related calls (e.g. all tasks in one ateam code run)")
	addVerboseFlag(runCmd, &runVerbose)
	addDockerAutoSetupFlag(runCmd, &runDockerAutoSetup)
	runCmd.Flags().BoolVar(&runDryRun, "dry-run", false, "print resolved command, secrets, and prompt without running")
}

func runRun(cmd *cobra.Command, args []string) error {
	promptText, err := prompts.ResolveValue(args[0])
	if err != nil {
		return fmt.Errorf("cannot resolve prompt: %w", err)
	}

	// Try to resolve project context (optional for ateam run)
	env, err := root.Lookup()
	if err != nil {
		return fmt.Errorf("cannot find .ateamorg/: %w", err)
	}

	hasProject := env.ProjectDir != "" && env.Config != nil

	// If role specified, require project context
	if runRole != "" && !hasProject {
		return fmt.Errorf("--role requires a project context (.ateam/ directory)")
	}

	// Validate role if specified
	if runRole != "" {
		if !prompts.IsValidRole(runRole, env.Config.Roles, env.ProjectDir, env.OrgDir) {
			return fmt.Errorf("unknown role: %s\nValid roles: %s", runRole, strings.Join(prompts.AllKnownRoleIDs(env.Config.Roles, env.ProjectDir, env.OrgDir), ", "))
		}
		if err := root.EnsureRoles(env.ProjectDir, []string{runRole}); err != nil {
			return err
		}
	}

	// Resolve working directory
	workDir := ""
	if runWorkDir != "" {
		abs, err := filepath.Abs(runWorkDir)
		if err != nil {
			return fmt.Errorf("cannot resolve work-dir: %w", err)
		}
		workDir = abs
	} else if hasProject {
		workDir = env.SourceDir
	}

	// Resolve runner from flags or config
	var r *runner.Runner
	if hasProject {
		r, err = resolveRunner(env, runProfile, runAgent, runner.ActionRun, runRole, runDockerAutoSetup)
	} else {
		// No project context — use flags or "default" profile
		profile := runProfile
		if profile == "" && runAgent == "" {
			profile = "default"
		}
		r, err = resolveRunnerMinimal(env.OrgDir, profile, runAgent)
	}
	if err != nil {
		if !runDryRun {
			return err
		}
		// In dry-run mode, show the error but continue with what we can resolve
		fmt.Fprintf(os.Stderr, "Warning: %v\n\n", err)
		if r == nil {
			return nil
		}
	}

	setSourceWritable(r)

	// Open call tracking DB.
	db := openProjectDB(env)
	if db != nil {
		defer db.Close()
		r.CallDB = db
	}

	// Apply --agent-args
	if runAgentArgs != "" {
		r.ExtraArgs = append(r.ExtraArgs, strings.Fields(runAgentArgs)...)
	}

	// Apply model override
	if runModel != "" {
		if ca, ok := r.Agent.(*agent.ClaudeAgent); ok {
			ca.Model = runModel
		}
	}

	// Dry-run: print everything and exit
	if runDryRun {
		return printRunDryRun(r, env, promptText, runRole, runTaskGroup)
	}

	// Determine logs dir
	var logsDir string
	if runRole != "" {
		logsDir = env.RoleLogsDir(runRole)
	} else {
		baseDir := env.OrgDir
		if hasProject {
			baseDir = env.ProjectDir
		}
		logsDir = filepath.Join(baseDir, "logs", "run")
	}

	// Build opts
	opts := runner.RunOpts{
		RoleID:    runRole,
		Action:    runner.ActionRun,
		LogsDir:   logsDir,
		WorkDir:   workDir,
		Verbose:   runVerbose,
		TaskGroup: runTaskGroup,
	}

	opts.PromptName = "run_prompt.md"
	ts := time.Now().Format(runner.TimestampFormat)
	if runRole != "" {
		roleDir := env.RoleDir(runRole)
		opts.LastMessageFilePath = filepath.Join(roleDir, "history", ts+".run_output.md")
		opts.ErrorMessageFilePath = filepath.Join(roleDir, "history", ts+".run_error.md")
		opts.HistoryDir = env.RoleHistoryDir(runRole)
	} else {
		opts.LastMessageFilePath = filepath.Join(logsDir, ts+"_run_output.md")
		opts.ErrorMessageFilePath = filepath.Join(logsDir, ts+"_run_error.md")
		opts.HistoryDir = logsDir
	}

	showStream := !runNoStream && !runQuiet
	showSummary := !runNoSummary && !runQuiet

	var progress chan runner.RunProgress
	var progressWg sync.WaitGroup
	if showStream {
		progress = make(chan runner.RunProgress, 64)
		progressWg.Add(1)
		go func() {
			defer progressWg.Done()
			printProgress(progress)
		}()
	}

	ctx, stop := cmdContext()
	defer stop()
	result := r.Run(ctx, promptText, opts, progress)

	if progress != nil {
		close(progress)
		progressWg.Wait()
	}

	// Stream stderr to our stderr.
	if f, err := os.Open(result.StderrFilePath); err == nil {
		_, _ = io.Copy(os.Stderr, f)
		f.Close()
	}

	// Print the last message to stdout.
	if result.Output != "" {
		fmt.Print(result.Output)
		if result.Output[len(result.Output)-1] != '\n' {
			fmt.Println()
		}
	}

	if showSummary {
		printRunSummary(result)
	}

	if result.Err != nil {
		return &ExitError{Code: result.ExitCode}
	}

	return nil
}

func printProgress(ch <-chan runner.RunProgress) {
	for p := range ch {
		ts := runner.FormatDuration(p.Elapsed)
		switch p.Phase {
		case runner.PhaseInit:
			fmt.Fprintf(os.Stderr, "[%s] initializing...\n", p.RoleID)
		case runner.PhaseThinking:
			if p.Content != "" {
				fmt.Fprintf(os.Stderr, "[%s] %s (%s)\n", p.RoleID, singleLine(p.Content), ts)
			} else {
				fmt.Fprintf(os.Stderr, "[%s] thinking... (%s)\n", p.RoleID, ts)
			}
		case runner.PhaseTool:
			if p.ToolInput != "" {
				fmt.Fprintf(os.Stderr, "[%s] tool: %s %s (%d total, %s)\n", p.RoleID, p.ToolName, singleLine(p.ToolInput), p.ToolCount, ts)
			} else {
				fmt.Fprintf(os.Stderr, "[%s] tool: %s (%d total, %s)\n", p.RoleID, p.ToolName, p.ToolCount, ts)
			}
		case runner.PhaseToolResult:
			if p.Content != "" {
				fmt.Fprintf(os.Stderr, "[%s] result: %s (%s)\n", p.RoleID, singleLine(p.Content), ts)
			}
		case runner.PhaseDone:
			fmt.Fprintf(os.Stderr, "[%s] done (%s)\n", p.RoleID, ts)
		case runner.PhaseError:
			fmt.Fprintf(os.Stderr, "[%s] error (%s)\n", p.RoleID, ts)
		}
	}
}

func singleLine(s string) string {
	return runner.SingleLineText(s)
}

func printRunDryRun(r *runner.Runner, env *root.ResolvedEnv, prompt, roleID, taskGroup string) error {
	fmt.Println("╔══ dry-run ══╗")
	fmt.Println()

	// Resolve template variables for display
	agentName := r.Agent.Name()
	var model string
	if mp, ok := r.Agent.(agent.ModelProvider); ok {
		model = agent.NormalizeModel(mp.ModelName())
	}
	tmplVars := runner.BuildTemplateVars(r, runner.RunOpts{
		RoleID:    roleID,
		Action:    runner.ActionRun,
		TaskGroup: taskGroup,
	}, time.Now(), 0, agentName, model)
	resolvedAgent := runner.ResolveAgentForDryRun(r.Agent, tmplVars)
	resolvedExtraArgs := runner.ResolveTemplateArgs(r.ExtraArgs, tmplVars)

	// Agent and profile
	fmt.Printf("Agent:     %s\n", agentName)
	if r.Profile != "" {
		fmt.Printf("Profile:   %s\n", r.Profile)
	}
	if r.ContainerType != "" && r.ContainerType != "none" {
		name := r.ContainerType
		if r.ContainerName != "" {
			name += " (" + runner.ResolveTemplateString(r.ContainerName, tmplVars) + ")"
		}
		fmt.Printf("Container: %s\n", name)
	}
	fmt.Println()

	// Build the full low-level args (including --settings if sandbox is active)
	fullArgs := make([]string, len(resolvedExtraArgs))
	copy(fullArgs, resolvedExtraArgs)

	skipSandbox := runner.IsInContainer() && !r.SandboxInsideContainer
	if r.SandboxSettings != "" && !skipSandbox {
		settingsPath := "<logs>/<timestamp>_settings.json"
		fullArgs = append(fullArgs, "--settings", settingsPath)
	}

	// Agent command (with resolved templates and settings)
	cmd, args := resolvedAgent.DebugCommandArgs(fullArgs)
	fmt.Printf("Command:\n  %s %s\n", cmd, strings.Join(args, " "))
	fmt.Println()

	// CLAUDE_CONFIG_DIR
	configDir := runner.ExpandHome(runner.ResolveTemplateString(r.ConfigDir, tmplVars))
	if configDir != "" {
		var configPath string
		if filepath.IsAbs(configDir) {
			configPath = configDir
		} else if r.ProjectDir != "" {
			configPath = filepath.Join(r.ProjectDir, configDir)
		} else {
			configPath = configDir
		}
		fmt.Printf("CLAUDE_CONFIG_DIR: %s\n\n", configPath)
	}

	// Docker command (if container)
	if r.Container != nil {
		opts := container.RunOpts{WorkDir: r.SourceDir}
		dockerCmd := r.Container.DebugCommand(opts)
		if dockerCmd != "" {
			fmt.Printf("Docker:\n  %s\n", dockerCmd)
			fmt.Println()
		}
	}

	// Secret resolution
	rtCfg, _ := runtime.Load(env.ProjectDir, env.OrgDir)
	if rtCfg != nil {
		var ac *runtime.AgentConfig
		var forwardEnv []string
		profileName := r.Profile
		if strings.HasPrefix(profileName, "a:") {
			an := profileName[2:]
			if a, ok := rtCfg.Agents[an]; ok {
				ac = &a
			}
		} else if profileName != "" {
			if _, a, cc, err := rtCfg.ResolveProfile(profileName); err == nil {
				ac = a
				if cc != nil {
					forwardEnv = cc.ForwardEnv
				}
			}
		}
		if ac != nil {
			resolver := secretResolver(env, secret.DefaultBackend())
			details := secret.ResolveAllRequired(ac, forwardEnv, resolver)
			if len(details) > 0 {
				fmt.Println("Secrets:")
				for _, d := range details {
					if d.Found {
						fmt.Printf("  %-30s ✓ %s (%s, %s)\n", d.Name, d.Masked, d.Source, d.Backend)
					} else {
						fmt.Printf("  %-30s ✗ not found\n", d.Name)
					}
				}
				fmt.Println()
			}
		}
	}

	// Sandbox settings
	if r.SandboxSettings != "" && !skipSandbox {
		fmt.Println("Sandbox: configured (use --verbose for full JSON)")
	} else if r.SandboxSettings != "" && skipSandbox {
		fmt.Println("Sandbox: skipped (inside container)")
	}

	// Prompt
	fmt.Println("Prompt:")
	if len(prompt) > 500 {
		fmt.Printf("  %s...\n  (%d chars total)\n", prompt[:500], len(prompt))
	} else {
		fmt.Printf("  %s\n", prompt)
	}
	fmt.Println()
	fmt.Println("╚══ dry-run ══╝")
	return nil
}

func printRunSummary(r runner.RunSummary) {
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "--- Summary ---\n")
	fmt.Fprintf(os.Stderr, "  Role:     %s\n", r.RoleID)
	fmt.Fprintf(os.Stderr, "  Duration: %s\n", runner.FormatDuration(r.Duration))
	if r.Cost > 0 {
		fmt.Fprintf(os.Stderr, "  Cost:     $%.2f\n", r.Cost)
	}
	if r.Turns > 0 {
		fmt.Fprintf(os.Stderr, "  Turns:    %d\n", r.Turns)
	}
	if r.InputTokens > 0 {
		fmt.Fprintf(os.Stderr, "  Input:    %d tokens\n", r.InputTokens)
	}
	if r.OutputTokens > 0 {
		fmt.Fprintf(os.Stderr, "  Output:   %d tokens\n", r.OutputTokens)
	}
	if r.ExitCode != 0 {
		fmt.Fprintf(os.Stderr, "  Exit:     %d\n", r.ExitCode)
	}
	if r.Err != nil {
		fmt.Fprintf(os.Stderr, "  Error:    %v\n", r.Err)
	}
}
