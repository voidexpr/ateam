package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ateam/internal/display"
	"github.com/ateam/internal/flow"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/spf13/cobra"
)

var (
	execRole            string
	execAction          string
	execProfile         string
	execAgent           string
	execModel           string
	execEffort          string
	execMaxBudgetUSD    string
	execMaxBudgetBatch  string
	execPrePrompt       string
	execPostPrompt      string
	execNoStream        bool
	execNoSummary       bool
	execQuiet           bool
	execAgentArgs       string
	execVerbose         bool
	execBatch           string
	execDockerAutoSetup bool
	execDryRun          bool
	execContainerName   string
	execFormat          string
	execProgressFD      int
	execRaw             bool
)

var execCmd = &cobra.Command{
	Use:   "exec [PROMPT|@FILE|-]",
	Short: "Execute an agent with a prompt",
	Long: `Execute an agent with the provided prompt. Sources, in order of precedence:
  - the positional argument: literal prompt text, "@PATH" to read a file,
    or "-" / "@-" to read stdin until EOF
  - if no argument is given AND stdin is piped/redirected, read stdin

Can run standalone (just needs .ateamorg/) or within a project context.

With --role: stores output in the role directory (role name is accepted as-is).
Without --role: runs as ad-hoc, stores output in project or org logs.

Streaming and summary are on by default. Use --quiet to suppress both,
or --no-stream / --no-summary individually.

Example:
  ateam exec "say hello"
  ateam exec "Analyze the auth module" --role project.security
  ateam exec "test" --profile cheap
  ateam exec @prompt_file.md
  ateam exec @prompt_file.md --post-prompt "focus on the auth module"
  echo "explain this code" | ateam exec
  git diff | ateam exec --role critic.engineering
  ateam exec "say hi" --model sonnet
  ateam exec "quick check" --quiet`,
	Args: cobra.MaximumNArgs(1),
	RunE: runExec,
}

func init() {
	execCmd.Flags().StringVar(&execRole, "role", "", "role to run (optional)")
	execCmd.Flags().StringVar(&execAction, "action", runner.ActionExec, "action label recorded for this run (free-form; affects ps/template vars/labels, not output promotion)")
	execCmd.Flags().StringVar(&execModel, "model", "", "model override")
	execCmd.Flags().StringVar(&execEffort, "effort", "", "reasoning effort override, passed verbatim to the agent CLI (e.g. low/medium/high)")
	addBudgetFlags(execCmd, &execMaxBudgetUSD, &execMaxBudgetBatch,
		"per-agent USD spend cap (claude-only; errors on codex)",
		"abort if --batch already exceeds this USD before starting")
	addPromptWrapFlags(execCmd, &execPrePrompt, &execPostPrompt)
	addProfileFlags(execCmd, &execProfile, &execAgent)
	execCmd.Flags().BoolVar(&execNoStream, "no-stream", false, "disable progress updates during execution")
	execCmd.Flags().BoolVar(&execNoSummary, "no-summary", false, "disable run summary after completion")
	execCmd.Flags().BoolVar(&execQuiet, "quiet", false, "disable both streaming and summary (same as --no-stream --no-summary)")
	execCmd.Flags().StringVar(&execAgentArgs, "agent-args", "", "extra args passed to the agent CLI (appended after configured args)")
	execCmd.Flags().StringVar(&execBatch, "batch", "", "group related agent_execs (e.g. all execs in one ateam code run)")
	execCmd.Flags().BoolVar(&execRaw, "raw", false, "feed the prompt to the agent byte-for-byte: skip template-engine expansion (no {{var}} substitution, no dynamics). Default expands {{exec.*}}, {{prompt.*}}, and other vars in the supplied prompt.")
	addVerboseFlag(execCmd, &execVerbose)
	addDockerAutoSetupFlag(execCmd, &execDockerAutoSetup)
	addContainerNameFlag(execCmd, &execContainerName)
	execCmd.Flags().BoolVar(&execDryRun, "dry-run", false, "print resolved command, secrets, and prompt without running")
	execCmd.Flags().StringVar(&execFormat, "format", "", "structured output format: 'jsonl' for an interleaved bundle+agent event stream on stdout. Implies --no-stream --no-summary and suppresses the agent's text output (which is preserved in the event stream's assistant events)")
	execCmd.Flags().IntVar(&execProgressFD, "progress-fd", 0, "redirect --format output to this file descriptor instead of stdout (e.g. via Popen's pass_fds when the orchestrator wants stdout for something else)")
}

func runExec(cmd *cobra.Command, args []string) error {
	promptArg, err := promptArgOrStdin(args)
	if err != nil {
		return err
	}
	prePrompt, postPrompt, err := prompts.ResolveWrap(execPrePrompt, execPostPrompt)
	if err != nil {
		return err
	}
	promptInst, err := buildArgPrompt(promptArg, prePrompt, postPrompt, execRaw)
	if err != nil {
		return err
	}

	env, err := lookupEnv()
	if err != nil {
		return fmt.Errorf("cannot find .ateamorg/: %w", err)
	}

	hasProject := env.HasProject()

	if execRole != "" && !hasProject {
		return fmt.Errorf("--role requires a project context (.ateam/ directory)")
	}

	// --role is intentionally free-form here: exec accepts any label so the
	// supervisor can tag sub-runs with task-specific names (e.g.
	// "fix_regression") without having to pre-register them in config.toml.
	// The role logs directory is created lazily by the runner the first time
	// it writes output.
	r, err := buildRunner(env, RunnerSpec{
		Profile:         execProfile,
		Agent:           execAgent,
		Action:          execAction,
		Role:            execRole,
		DockerAutoSetup: execDockerAutoSetup,
		Overrides: RunnerOverrides{
			ContainerName:     execContainerName,
			Model:             execModel,
			Effort:            execEffort,
			MaxBudgetUSD:      execMaxBudgetUSD,
			MaxBudgetUSDBatch: execMaxBudgetBatch,
		},
	})
	if err != nil {
		if !execDryRun {
			return err
		}
		fmt.Fprintf(os.Stderr, "Warning: %v\n\n", err)
		return nil
	}

	if execAgentArgs != "" {
		r.ExtraArgs = append(r.ExtraArgs, strings.Fields(execAgentArgs)...)
	}

	// Scratch mode (no project config) skips the exec-timeout default; the
	// agent's own timeout still applies.
	var timeout int
	if hasProject {
		timeout = env.Config.Exec.EffectiveTimeout(0)
	}

	// Build opts. `exec` has no canonical destination — its deliverable is the
	// stream, viewable via `ateam cat <exec_id>`.
	opts := runner.RunOpts{
		RoleID:     execRole,
		Action:     execAction,
		WorkDir:    env.WorkDir,
		Verbose:    execVerbose,
		Batch:      execBatch,
		TimeoutMin: timeout,
	}

	// Build the bundle up front so dry-run and live share one
	// composition path. ResolvePreview runs the same Prompt.Resolve the
	// live execute path runs, just in ModePreview — operators see the
	// engine-expanded body (sentinels for runtime-only exec.*).
	bundle := staticBundle("exec", execRole, execAction, promptInst, opts, env)

	if execDryRun {
		resolved, err := bundle.ResolvePreview(env, env.WorkDir)
		if err != nil {
			return err
		}
		return printExecDryRun(r, env, resolved, execRole, execAction, execBatch)
	}

	db, err := openStateDB(env)
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

	jsonOut, jsonCloser, err := openProgressFD(execFormat, execProgressFD)
	if err != nil {
		return err
	}
	defer jsonCloser.Close()

	// --format jsonl owns stdout for the event stream: silence the human
	// streaming + summary + agent-text output so the orchestrator's pipe
	// stays clean. The agent's final text is still recoverable from the
	// stream's `assistant` events and `agent_exec_end` totals.
	jsonMode := jsonOut != nil
	showStream := !execNoStream && !execQuiet && !jsonMode
	showSummary := !execNoSummary && !execQuiet && !jsonMode

	ctx, stop := cmdContext()
	defer stop()

	rtEnv := flow.RuntimeEnv{
		Executor: r,
		WorkDir:  env.WorkDir,
		Role:     execRole,
		Action:   execAction,
		Batch:    execBatch,
	}
	rc := flow.RunCtx{
		Ctx:      ctx,
		DB:       db,
		Resolved: env,
		Reporter: flow.MultiReporter{
			&flow.StdoutReporter{Stream: showStream, SuppressBundleEnd: true},
			&flow.BundleLogReporter{},
			jsonReporterOrNil(jsonOut),
		},
	}
	res := flow.RunBundle(bundle, rtEnv, rc)
	// Pre-execute failures (e.g. Prompt.Resolve errors after Prepare
	// allocated the exec row) return a StateError Result with no Summary.
	// Surface the resolver error directly instead of masking it as an
	// internal invariant failure.
	if res.Summary == nil {
		if res.Flow.State == flow.StateError && res.Flow.Err != nil {
			return res.Flow.Err
		}
		return fmt.Errorf("internal: flow.RunBundle returned no Summary")
	}
	result := *res.Summary

	if !jsonMode {
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
	}

	if showSummary {
		printExecSummary(result)
	}

	if result.Err != nil {
		return &ExitError{Code: result.ExitCode}
	}

	return nil
}

// printProgress drains a runner.RunProgress chan, writing one
// flow.PrintProgressLine per event to stderr. Used by the legacy
// chan-progress paths in cmd/auto_roles and cmd/inspect — the
// migrated flow cmds receive the same lines via flow.StdoutReporter's
// Stream-mode AgentEvent. Single source of truth for the format lives
// in flow.PrintProgressLine.
func printProgress(ch <-chan runner.RunProgress) {
	for p := range ch {
		flow.PrintProgressLine(os.Stderr, p)
	}
}

// formatInitLine + singleLine + fmtContextProgress are exported from
// internal/flow as FormatInitLine / runner.SingleLineText /
// FormatContextProgress. The test below still hits the cmd-local name
// for compatibility — alias so callers and tests work the same.
var formatInitLine = flow.FormatInitLine

func printExecDryRun(r *runner.AgentExecutor, env *root.ResolvedEnv, prompt, roleID, action, batch string) error {
	fmt.Println("╔══ dry-run ══╗")
	fmt.Println()
	printDryRunInfo(r, env, dryRunOpts{
		RoleID: roleID,
		Action: action,
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
	if r.ExecID > 0 {
		fmt.Fprintf(os.Stderr, "  ExecID:   %d\n", r.ExecID)
	}
	fmt.Fprintf(os.Stderr, "  Role:     %s\n", r.RoleID)
	fmt.Fprintf(os.Stderr, "  Duration: %s\n", display.FormatDuration(r.Duration))
	if c := display.FmtCost(r.Cost); c != "" {
		fmt.Fprintf(os.Stderr, "  Cost:     %s\n", c)
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

// openProgressFD validates --format / --progress-fd and returns the
// destination writer for JSONReporter (or nil when --format is empty).
// Defaults to os.Stdout when --format is set but --progress-fd is not —
// matches the convention of `--format X` CLIs (jq, kubectl -o json, gh
// --json). Caller passes --progress-fd N to redirect elsewhere (e.g.
// Popen pass_fds when the orchestrator wants stdout for something else).
//
// closer is always non-nil and safe to call from a defer; for the
// process's std{out,err} it's a no-op, for caller-passed fds it closes
// the underlying *os.File. Callers don't need to special-case stdio.
func openProgressFD(format string, fd int) (w io.Writer, closer io.Closer, err error) {
	if format == "" {
		if fd != 0 {
			return nil, nopCloser{}, fmt.Errorf("--progress-fd requires --format")
		}
		return nil, nopCloser{}, nil
	}
	if format != "jsonl" {
		return nil, nopCloser{}, fmt.Errorf("unknown --format %q (supported: jsonl)", format)
	}
	switch {
	case fd < 0:
		return nil, nopCloser{}, fmt.Errorf("--progress-fd must be non-negative")
	case fd == 0 || fd == 1:
		return os.Stdout, nopCloser{}, nil
	case fd == 2:
		return os.Stderr, nopCloser{}, nil
	default:
		f := os.NewFile(uintptr(fd), fmt.Sprintf("progress-fd-%d", fd))
		if f == nil {
			return nil, nopCloser{}, fmt.Errorf("--progress-fd=%d is not a valid file descriptor", fd)
		}
		return f, f, nil
	}
}

// nopCloser is the std{out,err} closer — the process owns those fds for
// its entire lifetime, so Close is intentionally a no-op.
type nopCloser struct{}

func (nopCloser) Close() error { return nil }

// jsonReporterOrNil returns a *flow.JSONReporter when w is non-nil, else
// nil — which MultiReporter silently skips. Keeps the Reporter-chain
// declaration site free of conditionals.
func jsonReporterOrNil(w io.Writer) flow.Reporter {
	if w == nil {
		return nil
	}
	return &flow.JSONReporter{W: w}
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
