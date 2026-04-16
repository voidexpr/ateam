package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ateam/internal/display"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
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
	runContainerName   string
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
	addContainerNameFlag(runCmd, &runContainerName)
	runCmd.Flags().BoolVar(&runDryRun, "dry-run", false, "print resolved command, secrets, and prompt without running")
}

func runRun(cmd *cobra.Command, args []string) error {
	promptText, err := prompts.ResolveValue(args[0])
	if err != nil {
		return fmt.Errorf("cannot resolve prompt: %w", err)
	}

	// Try to resolve project context (optional for ateam run)
	env, err := root.Lookup(orgFlag, projectFlag)
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

	applyContainerName(r, env, runContainerName)
	setSourceWritable(r)

	// Apply --agent-args
	if runAgentArgs != "" {
		r.ExtraArgs = append(r.ExtraArgs, strings.Fields(runAgentArgs)...)
	}

	// Apply model override
	if runModel != "" {
		r.Agent.SetModel(runModel)
	}

	// Dry-run: print everything and exit
	if runDryRun {
		return printRunDryRun(r, env, promptText, runRole, runTaskGroup)
	}

	// Open call tracking DB (requires project context).
	if hasProject {
		db, err := openProjectDB(env)
		if err != nil {
			return err
		}
		defer db.Close()
		r.CallDB = db
	}

	// Determine logs dir — always under .ateam/ when project exists.
	var logsDir string
	if runRole != "" {
		logsDir = env.RoleLogsDir(runRole)
	} else if hasProject {
		logsDir = filepath.Join(env.ProjectDir, "logs", "run")
	} else {
		logsDir = filepath.Join(env.OrgDir, "logs", "run")
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
			ctxInfo := fmtContextProgress(p.ContextTokens, p.ContextWindow)
			if p.ToolInput != "" {
				fmt.Fprintf(os.Stderr, "[%s] tool: %s %s (%d total, %s%s)\n", p.RoleID, p.ToolName, singleLine(p.ToolInput), p.ToolCount, ts, ctxInfo)
			} else {
				fmt.Fprintf(os.Stderr, "[%s] tool: %s (%d total, %s%s)\n", p.RoleID, p.ToolName, p.ToolCount, ts, ctxInfo)
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

func fmtContextProgress(contextTokens, contextWindow int) string {
	if contextTokens <= 0 {
		return ""
	}
	ctxStr := display.FmtTokens(int64(contextTokens))
	if contextWindow > 0 {
		pct := contextTokens * 100 / contextWindow
		return fmt.Sprintf(", ctx: %s/%d%%", ctxStr, pct)
	}
	return fmt.Sprintf(", ctx: %s", ctxStr)
}

func printRunDryRun(r *runner.Runner, env *root.ResolvedEnv, prompt, roleID, taskGroup string) error {
	fmt.Println("╔══ dry-run ══╗")
	fmt.Println()
	printDryRunInfo(r, env, dryRunOpts{
		RoleID:    roleID,
		Action:    runner.ActionRun,
		TaskGroup: taskGroup,
		Prompt:    prompt,
	})
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
	if r.PeakContextTokens > 0 {
		peak := display.FmtTokens(int64(r.PeakContextTokens))
		if r.ContextWindow > 0 {
			window := display.FmtTokens(int64(r.ContextWindow))
			pct := r.PeakContextTokens * 100 / r.ContextWindow
			fmt.Fprintf(os.Stderr, "  Context:  %s / %s (%d%%)\n", peak, window, pct)
		} else {
			fmt.Fprintf(os.Stderr, "  Context:  %s\n", peak)
		}
	}
	if r.ExitCode != 0 {
		fmt.Fprintf(os.Stderr, "  Exit:     %d\n", r.ExitCode)
	}
	if r.Err != nil {
		fmt.Fprintf(os.Stderr, "  Error:    %v\n", r.Err)
	}
}
