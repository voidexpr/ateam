package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/ateam/internal/display"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/spf13/cobra"
)

var (
	execRole            string
	execProfile         string
	execAgent           string
	execModel           string
	execEffort          string
	execMaxBudgetUSD    string
	execMaxBudgetBatch  string
	execExtraPrompt     string
	execNoStream        bool
	execNoSummary       bool
	execQuiet           bool
	execAgentArgs       string
	execVerbose         bool
	execBatch           string
	execDockerAutoSetup bool
	execDryRun          bool
	execContainerName   string
)

var execCmd = &cobra.Command{
	Use:   "exec [PROMPT|@FILE|-]",
	Short: "Execute an agent with a prompt",
	Long: `Execute an agent with the provided prompt. Sources, in order of precedence:
  - the positional argument: literal prompt text, "@PATH" to read a file,
    or "-" / "@-" to read stdin until EOF
  - if no argument is given AND stdin is piped/redirected, read stdin

Can run standalone (just needs .ateamorg/) or within a project context.

With --role: validates the role exists and stores output in role directory.
Without --role: runs as ad-hoc, stores output in project or org logs.

Streaming and summary are on by default. Use --quiet to suppress both,
or --no-stream / --no-summary individually.

Example:
  ateam exec "say hello"
  ateam exec "Analyze the auth module" --role project.security
  ateam exec "test" --profile cheap
  ateam exec @prompt_file.md
  ateam exec @prompt_file.md --extra-prompt "focus on the auth module"
  echo "explain this code" | ateam exec
  git diff | ateam exec --role critic.engineering
  ateam exec "say hi" --model sonnet
  ateam exec "quick check" --quiet`,
	Args: cobra.MaximumNArgs(1),
	RunE: runExec,
}

func init() {
	execCmd.Flags().StringVar(&execRole, "role", "", "role to run (optional)")
	execCmd.Flags().StringVar(&execModel, "model", "", "model override")
	execCmd.Flags().StringVar(&execEffort, "effort", "", "reasoning effort override, passed verbatim to the agent CLI (e.g. low/medium/high)")
	addBudgetFlags(execCmd, &execMaxBudgetUSD, &execMaxBudgetBatch,
		"per-agent USD spend cap (claude-only; errors on codex)",
		"abort if --batch already exceeds this USD before starting")
	execCmd.Flags().StringVar(&execExtraPrompt, "extra-prompt", "", "additional instructions appended after the main prompt (text or @filepath)")
	addProfileFlags(execCmd, &execProfile, &execAgent)
	execCmd.Flags().BoolVar(&execNoStream, "no-stream", false, "disable progress updates during execution")
	execCmd.Flags().BoolVar(&execNoSummary, "no-summary", false, "disable run summary after completion")
	execCmd.Flags().BoolVar(&execQuiet, "quiet", false, "disable both streaming and summary (same as --no-stream --no-summary)")
	execCmd.Flags().StringVar(&execAgentArgs, "agent-args", "", "extra args passed to the agent CLI (appended after configured args)")
	execCmd.Flags().StringVar(&execBatch, "batch", "", "group related agent_execs (e.g. all execs in one ateam code run)")
	addVerboseFlag(execCmd, &execVerbose)
	addDockerAutoSetupFlag(execCmd, &execDockerAutoSetup)
	addContainerNameFlag(execCmd, &execContainerName)
	execCmd.Flags().BoolVar(&execDryRun, "dry-run", false, "print resolved command, secrets, and prompt without running")
}

func runExec(cmd *cobra.Command, args []string) error {
	promptArg, err := promptArgOrStdin(args)
	if err != nil {
		return err
	}
	promptText, err := prompts.ResolveValue(promptArg)
	if err != nil {
		return fmt.Errorf("cannot resolve prompt: %w", err)
	}
	extraPrompt, err := prompts.ResolveOptional(execExtraPrompt)
	if err != nil {
		return fmt.Errorf("cannot resolve --extra-prompt: %w", err)
	}
	if extraPrompt != "" {
		promptText += "\n\n---\n\n# Additional Instructions\n\n" + extraPrompt
	}

	env, err := lookupEnv()
	if err != nil {
		return fmt.Errorf("cannot find .ateamorg/: %w", err)
	}

	hasProject := env.ProjectDir != "" && env.Config != nil

	if execRole != "" && !hasProject {
		return fmt.Errorf("--role requires a project context (.ateam/ directory)")
	}

	if execRole != "" {
		if !prompts.IsValidRole(execRole, env.Config.Roles, env.ProjectDir, env.OrgDir) {
			return fmt.Errorf("unknown role: %s\nValid roles: %s", execRole, strings.Join(prompts.AllKnownRoleIDs(env.Config.Roles, env.ProjectDir, env.OrgDir), ", "))
		}
		if err := root.EnsureRoles(env.ProjectDir, []string{execRole}); err != nil {
			return err
		}
	}
	var r *runner.Runner
	if hasProject {
		r, err = resolveRunner(env, execProfile, execAgent, runner.ActionExec, execRole, execDockerAutoSetup)
	} else {
		profile := execProfile
		if profile == "" && execAgent == "" {
			profile = "default"
		}
		r, err = resolveRunnerMinimal(env.OrgDir, profile, execAgent)
	}
	if err != nil {
		if !execDryRun {
			return err
		}
		fmt.Fprintf(os.Stderr, "Warning: %v\n\n", err)
		if r == nil {
			return nil
		}
	}

	if err := applyRunnerOverrides(r, env, RunnerOverrides{
		ContainerName:     execContainerName,
		Model:             execModel,
		Effort:            execEffort,
		MaxBudgetUSD:      execMaxBudgetUSD,
		MaxBudgetUSDBatch: execMaxBudgetBatch,
	}, runner.ActionExec); err != nil {
		return err
	}
	setSourceWritable(r)

	if execAgentArgs != "" {
		r.ExtraArgs = append(r.ExtraArgs, strings.Fields(execAgentArgs)...)
	}

	if execDryRun {
		return printExecDryRun(r, env, promptText, execRole, execBatch)
	}

	if !hasProject {
		return fmt.Errorf("ateam project required: no .ateam/ found")
	}
	db, err := openProjectDB(env)
	if err != nil {
		return err
	}
	defer db.Close()
	r.CallDB = db

	preCheck, err := batchBudgetPrecheck(db, env.ProjectID(), execBatch, execMaxBudgetBatch)
	if err != nil {
		return err
	}
	if preCheck != nil {
		if err := preCheck(); err != nil {
			return err
		}
	}

	timeout := env.Config.Run.EffectiveTimeout(0)

	// Build opts. `exec` has no canonical destination — its deliverable is the
	// stream, viewable via `ateam cat <exec_id>`.
	opts := runner.RunOpts{
		RoleID:     execRole,
		Action:     runner.ActionExec,
		WorkDir:    env.WorkDir,
		Verbose:    execVerbose,
		Batch:      execBatch,
		TimeoutMin: timeout,
	}

	showStream := !execNoStream && !execQuiet
	showSummary := !execNoSummary && !execQuiet

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

	if f, err := os.Open(result.StderrFilePath); err == nil {
		_, _ = io.Copy(os.Stderr, f)
		f.Close()
	}

	if result.Output != "" {
		fmt.Print(result.Output)
		if result.Output[len(result.Output)-1] != '\n' {
			fmt.Println()
		}
	}

	if showSummary {
		printExecSummary(result)
	}

	if result.Err != nil {
		return &ExitError{Code: result.ExitCode}
	}

	return nil
}

func printProgress(ch <-chan runner.RunProgress) {
	for p := range ch {
		ts := display.FormatDuration(p.Elapsed)
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
		case runner.PhaseStall:
			fmt.Fprintf(os.Stderr, "[%s] stall: %s (%s)\n", p.RoleID, p.Content, ts)
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

func printExecDryRun(r *runner.Runner, env *root.ResolvedEnv, prompt, roleID, batch string) error {
	fmt.Println("╔══ dry-run ══╗")
	fmt.Println()
	printDryRunInfo(r, env, dryRunOpts{
		RoleID: roleID,
		Action: runner.ActionExec,
		Batch:  batch,
		Prompt: prompt,
	})
	fmt.Println()
	fmt.Println("╚══ dry-run ══╝")
	return nil
}

func printExecSummary(r runner.RunSummary) {
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "--- Summary ---\n")
	fmt.Fprintf(os.Stderr, "  Role:     %s\n", r.RoleID)
	fmt.Fprintf(os.Stderr, "  Duration: %s\n", display.FormatDuration(r.Duration))
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

// promptArgOrStdin returns the prompt argument to feed prompts.ResolveValue:
// the explicit positional argument when given, or "-" (read stdin) when stdin
// is piped/redirected, or an error when neither is available.
func promptArgOrStdin(args []string) (string, error) {
	if len(args) == 1 {
		return args[0], nil
	}
	if stdinIsPiped() {
		return "-", nil
	}
	return "", fmt.Errorf("no prompt provided: pass a prompt, @file, or pipe via stdin (run `ateam exec --help`)")
}
