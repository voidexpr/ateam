package cmd

import (
	"fmt"
	"os"

	"github.com/ateam/internal/flow"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/runner"
	"github.com/spf13/cobra"
)

var (
	parallelLabels            []string
	parallelBatch             string
	parallelMaxParallel       int
	parallelNoProgress        bool
	parallelCommonPromptFirst string
	parallelCommonPromptLast  string
	parallelProfile           string
	parallelAgent             string
	parallelModel             string
	parallelEffort            string
	parallelMaxBudgetUSD      string
	parallelMaxBudgetBatch    string
	parallelTimeout           int
	parallelVerbose           bool
	parallelForce             bool
	parallelDryRun            bool
	parallelPrint             bool
	parallelDockerAutoSetup   bool
	parallelContainerName     string
	parallelRaw               bool
)

var parallelCmd = &cobra.Command{
	Use:   "parallel PROMPT_OR_@FILE...",
	Short: "Run multiple agents in parallel",
	Long: `Run multiple agents in parallel, each with its own prompt.

Each positional argument is a prompt (text or @filepath). All tasks share a
single runner instance and batch for unified cost tracking.

` + progressColumnsHelp("task") + `

Example:
  ateam parallel "analyze auth module" "analyze payment module"
  ateam parallel @task1.md @task2.md @task3.md --labels auth,payment,users
  ateam parallel "task A" "task B" --max-parallel 1 --common-prompt-first @context.md`,
	Args: cobra.MinimumNArgs(1),
	RunE: runParallel,
}

func init() {
	parallelCmd.Flags().StringSliceVar(&parallelLabels, "labels", nil, "names for each prompt (comma-separated, must match prompt count)")
	parallelCmd.Flags().StringVar(&parallelBatch, "batch", "", "group related agent_execs (default: parallel-TIMESTAMP)")
	parallelCmd.Flags().IntVar(&parallelMaxParallel, "max-parallel", 3, "max parallel agents execution")
	parallelCmd.Flags().BoolVar(&parallelNoProgress, "no-progress", false, "suppress ANSI progress table")
	// --pre-prompt / --post-prompt are the canonical names shared with
	// every other prompt-taking cmd; --common-prompt-first / --common-prompt-last
	// are kept as deprecated aliases backed by the same vars so existing
	// invocations don't break.
	parallelCmd.Flags().StringVar(&parallelCommonPromptFirst, "pre-prompt", "", UsagePrePrompt+" (applied to each task's prompt)")
	parallelCmd.Flags().StringVar(&parallelCommonPromptLast, "post-prompt", "", UsagePostPrompt+" (applied to each task's prompt)")
	parallelCmd.Flags().StringVar(&parallelCommonPromptFirst, "common-prompt-first", "", "deprecated alias for --pre-prompt")
	parallelCmd.Flags().StringVar(&parallelCommonPromptLast, "common-prompt-last", "", "deprecated alias for --post-prompt")
	_ = parallelCmd.Flags().MarkDeprecated("common-prompt-first", "use --pre-prompt instead")
	_ = parallelCmd.Flags().MarkDeprecated("common-prompt-last", "use --post-prompt instead")
	addProfileFlags(parallelCmd, &parallelProfile, &parallelAgent)
	parallelCmd.Flags().StringVar(&parallelModel, "model", "", "model override")
	parallelCmd.Flags().StringVar(&parallelEffort, "effort", "", "reasoning effort override, passed verbatim to the agent CLI")
	addBudgetFlags(parallelCmd, &parallelMaxBudgetUSD, &parallelMaxBudgetBatch,
		"per-agent USD spend cap (claude-only; warns on codex)",
		"stop dispatching new agents once batch cost crosses this USD")
	parallelCmd.Flags().IntVar(&parallelTimeout, "timeout", 0, "timeout in minutes per agent execution")
	addVerboseFlag(parallelCmd, &parallelVerbose)
	addForceFlag(parallelCmd, &parallelForce)
	parallelCmd.Flags().BoolVar(&parallelDryRun, "dry-run", false, "print computed prompts without running")
	parallelCmd.Flags().BoolVar(&parallelPrint, "print", false, "print task outputs to stdout after completion")
	addDockerAutoSetupFlag(parallelCmd, &parallelDockerAutoSetup)
	addContainerNameFlag(parallelCmd, &parallelContainerName)
	parallelCmd.Flags().BoolVar(&parallelRaw, "raw", false, "feed each prompt to the agent byte-for-byte: skip template-engine expansion (no {{var}} substitution, no dynamics). Default expands {{exec.*}}, {{prompt.*}}, and other vars.")
}

func runParallel(cmd *cobra.Command, args []string) error {
	commonFirst, err := prompts.ResolveOptional(parallelCommonPromptFirst)
	if err != nil {
		return fmt.Errorf("cannot resolve common-prompt-first: %w", err)
	}
	commonLast, err := prompts.ResolveOptional(parallelCommonPromptLast)
	if err != nil {
		return fmt.Errorf("cannot resolve common-prompt-last: %w", err)
	}

	// Each arg gets its own dispatch: `@foo.prompt.md` becomes a
	// PromptFile (sibling fragments compose around it), other forms
	// become PromptText (or RawTextPrompt with --raw). commonFirst /
	// commonLast act as parallel's per-step --pre-prompt / --post-prompt.
	promptInsts := make([]prompts.Prompt, len(args))
	for i, arg := range args {
		inst, err := buildArgPrompt(arg, commonFirst, commonLast, parallelRaw)
		if err != nil {
			return fmt.Errorf("prompt %d: %w", i+1, err)
		}
		promptInsts[i] = inst
	}

	labels := parallelLabels
	if len(labels) > 0 {
		if len(labels) != len(promptInsts) {
			return fmt.Errorf("--labels count (%d) must match prompt count (%d)", len(labels), len(promptInsts))
		}
	} else {
		labels = make([]string, len(promptInsts))
		for i := range labels {
			labels[i] = fmt.Sprintf("agent-%d", i+1)
		}
	}

	env, err := lookupEnv()
	if err != nil {
		return fmt.Errorf("cannot find .ateamorg/: %w", err)
	}

	if parallelDryRun {
		// Route every dry-run prompt through ResolvePreview so
		// {{prompt.name}}, exec.* sentinels, and dynamics expand the
		// same way they would at exec time — no separate composition
		// path for dry-run.
		for i, p := range promptInsts {
			b := staticBundle(labels[i], labels[i], runner.ActionParallel, p, runner.RunOpts{}, env)
			resolved, err := b.ResolvePreview(env, env.WorkDir)
			if err != nil {
				return fmt.Errorf("dry-run resolve %s: %w", labels[i], err)
			}
			if i > 0 {
				fmt.Println()
			}
			fmt.Printf("╔══ %s ══╗\n\n", labels[i])
			fmt.Println(resolved)
			fmt.Printf("\n╚══ %s ══╝\n", labels[i])
		}
		return nil
	}
	r, err := buildRunner(env, RunnerSpec{
		Profile:         parallelProfile,
		Agent:           parallelAgent,
		Action:          runner.ActionParallel,
		DockerAutoSetup: parallelDockerAutoSetup,
		Overrides: RunnerOverrides{
			ContainerName:     parallelContainerName,
			Model:             parallelModel,
			Effort:            parallelEffort,
			MaxBudgetUSD:      parallelMaxBudgetUSD,
			MaxBudgetUSDBatch: parallelMaxBudgetBatch,
		},
	})
	if err != nil {
		return err
	}

	db, err := openStateDB(env)
	if err != nil {
		return err
	}
	defer db.Close()
	r.CallDB = db

	if !parallelForce {
		if err := checkConcurrentRunsEnv(db, env, runner.ActionParallel, nil); err != nil {
			return err
		}
	}

	batch := resolveBatch(parallelBatch, "parallel")

	maxParallel := parallelMaxParallel
	if maxParallel <= 0 {
		maxParallel = 3
	}

	fmt.Fprintf(os.Stderr, "Running %d agent(s) in batch %s (max %d parallel)...\n\n", len(promptInsts), batch, maxParallel)

	preDispatch, err := batchBudgetPrecheck(db, env.ProjectID(), batch, parallelMaxBudgetBatch)
	if err != nil {
		return err
	}

	ctx, stop := cmdContext()
	defer stop()

	// One PromptBundle per submitted prompt; all share the same runner.
	// Labels become RoleID, free-form by design — no root.EnsureRoles call
	// here, same as cmd/exec.go. The runner creates per-exec_id log dirs
	// lazily, so role labels can be ad-hoc ("agent-1", task slugs, etc.).
	steps := make([]flow.Step, len(promptInsts))
	for i, prompt := range promptInsts {
		steps[i] = staticBundle(labels[i], labels[i], runner.ActionParallel, prompt, runner.RunOpts{
			RoleID:      labels[i],
			Action:      runner.ActionParallel,
			WorkDir:     env.WorkDir,
			TimeoutMin:  parallelTimeout,
			Verbose:     parallelVerbose,
			Batch:       batch,
			QuietExecID: true,
		}, env)
	}

	tr := newTableReporter(tableReporterOpts{
		out:       os.Stderr,
		labels:    labels,
		agentName: r.Agent.Name(),
		itemLabel: "agent(s)",
		quiet:     !isTerminal() || parallelNoProgress,
	})

	agents := flow.Parallel{
		Name:        "agents",
		Steps:       steps,
		Workers:     maxParallel,
		PreDispatch: preDispatch,
	}
	rtEnv := flow.RuntimeEnv{
		Executor: r,
		WorkDir:  env.WorkDir,
		Action:   runner.ActionParallel,
		Batch:    batch,
	}
	rc := flow.RunCtx{
		Ctx:      ctx,
		DB:       db,
		Resolved: env,
		Reporter: flow.MultiReporter{tr, &flow.BundleLogReporter{}},
	}
	flow.Run(agents, rtEnv, rc)
	runErr := tr.Close()

	// Print outputs in submission order (labels[] is authoritative).
	if parallelPrint {
		outputByLabel := make(map[string]string)
		for _, summary := range tr.Results() {
			if summary.Err == nil && !summary.IsError {
				outputByLabel[summary.RoleID] = summary.Output
			}
		}
		multiTask := len(promptInsts) > 1
		for _, label := range labels {
			output, ok := outputByLabel[label]
			if !ok {
				continue
			}
			if multiTask {
				fmt.Printf("\n══════ %s ══════\n\n", label)
			}
			fmt.Print(output)
			if output != "" && output[len(output)-1] != '\n' {
				fmt.Println()
			}
		}
	}

	return runErr
}
