package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ateam-poc/internal/agent"
	"github.com/ateam-poc/internal/prompts"
	"github.com/ateam-poc/internal/root"
	"github.com/ateam-poc/internal/runner"
	"github.com/ateam-poc/internal/runtime"
	"github.com/spf13/cobra"
)

var (
	runRole    string
	runProfile string
	runModel   string
	runStream  bool
	runWorkDir string
	runSummary bool
)

var runCmd = &cobra.Command{
	Use:   "run PROMPT_OR_@FILE",
	Short: "Run an agent with a prompt",
	Long: `Run an agent with the provided prompt text or file.
Can run standalone (just needs .ateamorg/) or within a project context.

With --role: validates the role exists and stores output in role directory.
Without --role: runs as ad-hoc, stores output in project or org logs.

Example:
  ateam run "say hello"
  ateam run "Analyze the auth module" --role security
  ateam run @prompt.md --role testing_basic --stream
  ateam run "test" --profile test
  ateam run "say hi" --model sonnet`,
	Args: cobra.ExactArgs(1),
	RunE: runRun,
}

func init() {
	runCmd.Flags().StringVar(&runRole, "role", "", "role to run (optional)")
	runCmd.Flags().StringVar(&runProfile, "profile", "", "runtime profile to use (overrides config resolution)")
	runCmd.Flags().StringVar(&runModel, "model", "", "model override")
	runCmd.Flags().BoolVar(&runStream, "stream", false, "show progress updates during execution")
	runCmd.Flags().StringVar(&runWorkDir, "work-dir", "", "working directory (defaults to project source dir or cwd)")
	runCmd.Flags().BoolVar(&runSummary, "summary", false, "print run summary after completion")
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
		if !prompts.IsValidRole(runRole, env.Config.Roles) {
			return fmt.Errorf("unknown role: %s\nValid roles: %s", runRole, strings.Join(prompts.AllKnownRoleIDs(env.Config.Roles), ", "))
		}
		if err := root.EnsureRoles(env.ProjectDir, env.StateDir, []string{runRole}); err != nil {
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

	// Load runtime config and resolve profile
	rtCfg, err := runtime.Load(env.ProjectDir, env.OrgDir)
	if err != nil {
		return fmt.Errorf("cannot load runtime.hcl: %w", err)
	}

	profileName := runProfile
	if profileName == "" {
		if hasProject {
			profileName = env.Config.ResolveProfile(runner.ActionRun, runRole)
		} else {
			profileName = "default"
		}
	}

	_, ac, _, err := rtCfg.ResolveProfile(profileName)
	if err != nil {
		return err
	}

	ag := buildAgent(ac)

	// Apply model override
	if runModel != "" {
		if ca, ok := ag.(*agent.ClaudeAgent); ok {
			ca.Model = runModel
		}
	}

	// Determine logs dir
	var logsDir string
	if runRole != "" {
		logsDir = env.RoleLogsDir(runRole)
	} else if hasProject {
		logsDir = env.SupervisorLogsDir()
	} else {
		logsDir = filepath.Join(env.OrgDir, "logs", "adhoc")
	}

	// Build runner
	r := &runner.Runner{
		Agent:  ag,
		OrgDir: env.OrgDir,
	}
	if hasProject {
		r.LogFile = env.RunnerLogPath()
		r.ProjectDir = env.ProjectDir
		r.ExtraWriteDirs = []string{env.OrgDir}
	}

	// Build opts
	opts := runner.RunOpts{
		RoleID: runRole,
		Action: runner.ActionRun,
		LogsDir: logsDir,
		WorkDir: workDir,
	}

	if runRole != "" {
		roleDir := filepath.Join(env.ProjectDir, "roles", runRole)
		opts.LastMessageFilePath = filepath.Join(roleDir, "last_run_output.md")
		opts.ErrorMessageFilePath = filepath.Join(roleDir, "last_run_error.md")
		opts.PromptName = "run_prompt.md"
		opts.HistoryDir = env.RoleHistoryDir(runRole)
	}

	var progress chan runner.RunProgress
	var progressWg sync.WaitGroup
	if runStream {
		progress = make(chan runner.RunProgress, 64)
		progressWg.Add(1)
		go func() {
			defer progressWg.Done()
			printProgress(progress)
		}()
	}

	ctx := context.Background()
	result := r.Run(ctx, promptText, opts, progress)

	if progress != nil {
		close(progress)
		progressWg.Wait()
	}

	// Stream stderr to our stderr.
	if f, err := os.Open(result.StderrFilePath); err == nil {
		io.Copy(os.Stderr, f)
		f.Close()
	}

	// Print the last message to stdout.
	if result.Output != "" {
		fmt.Print(result.Output)
		if result.Output[len(result.Output)-1] != '\n' {
			fmt.Println()
		}
	}

	if runSummary {
		printRunSummary(result)
	}

	if result.Err != nil {
		os.Exit(result.ExitCode)
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
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", "")
	return strings.TrimSpace(s)
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
