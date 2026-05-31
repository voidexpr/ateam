package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/ateam/internal/display"
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
	parallelCmd.Flags().StringVar(&parallelCommonPromptFirst, "common-prompt-first", "", "text or @filepath to prepend to each prompt")
	parallelCmd.Flags().StringVar(&parallelCommonPromptLast, "common-prompt-last", "", "text or @filepath to append to each prompt")
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
}

func runParallel(cmd *cobra.Command, args []string) error {
	resolvedPrompts := make([]string, len(args))
	for i, arg := range args {
		p, err := prompts.ResolveValue(arg)
		if err != nil {
			return fmt.Errorf("cannot resolve prompt %d: %w", i+1, err)
		}
		resolvedPrompts[i] = p
	}

	commonFirst, err := prompts.ResolveOptional(parallelCommonPromptFirst)
	if err != nil {
		return fmt.Errorf("cannot resolve common-prompt-first: %w", err)
	}
	commonLast, err := prompts.ResolveOptional(parallelCommonPromptLast)
	if err != nil {
		return fmt.Errorf("cannot resolve common-prompt-last: %w", err)
	}

	for i, p := range resolvedPrompts {
		if commonFirst != "" {
			p = commonFirst + "\n\n" + p
		}
		if commonLast != "" {
			p = p + "\n\n" + commonLast
		}
		resolvedPrompts[i] = p
	}

	labels := parallelLabels
	if len(labels) > 0 {
		if len(labels) != len(resolvedPrompts) {
			return fmt.Errorf("--labels count (%d) must match prompt count (%d)", len(labels), len(resolvedPrompts))
		}
	} else {
		labels = make([]string, len(resolvedPrompts))
		for i := range labels {
			labels[i] = fmt.Sprintf("agent-%d", i+1)
		}
	}

	if parallelDryRun {
		for i, p := range resolvedPrompts {
			if i > 0 {
				fmt.Println()
			}
			fmt.Printf("╔══ %s ══╗\n\n", labels[i])
			fmt.Println(p)
			fmt.Printf("\n╚══ %s ══╝\n", labels[i])
		}
		return nil
	}

	env, err := lookupEnv()
	if err != nil {
		return fmt.Errorf("cannot find .ateamorg/: %w", err)
	}
	hasProject := env.ProjectDir != "" && env.Config != nil
	var r *runner.AgentExecutor
	if hasProject {
		r, err = resolveRunner(env, parallelProfile, parallelAgent, runner.ActionParallel, "", parallelDockerAutoSetup)
	} else {
		profile := parallelProfile
		if profile == "" && parallelAgent == "" {
			profile = "default"
		}
		r, err = resolveRunnerMinimal(env.OrgDir, profile, parallelAgent)
	}
	if err != nil {
		return err
	}
	if err := applyRunnerOverrides(r, env, RunnerOverrides{
		ContainerName:     parallelContainerName,
		Model:             parallelModel,
		Effort:            parallelEffort,
		MaxBudgetUSD:      parallelMaxBudgetUSD,
		MaxBudgetUSDBatch: parallelMaxBudgetBatch,
	}, runner.ActionParallel); err != nil {
		return err
	}
	setSourceWritable(r)

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

	batch := parallelBatch
	if batch == "" {
		batch = "parallel-" + time.Now().Format(display.TimestampFormat)
	}

	maxParallel := parallelMaxParallel
	if maxParallel <= 0 {
		maxParallel = 3
	}

	fmt.Fprintf(os.Stderr, "Running %d agent(s) in batch %s (max %d parallel)...\n\n", len(resolvedPrompts), batch, maxParallel)

	preDispatch, err := batchBudgetPrecheck(db, env.ProjectID(), batch, parallelMaxBudgetBatch)
	if err != nil {
		return err
	}

	ctx, stop := cmdContext()
	defer stop()

	// One PromptBundle per submitted prompt; all share the same runner.
	steps := make([]flow.Step, len(resolvedPrompts))
	for i, prompt := range resolvedPrompts {
		i, prompt := i, prompt
		steps[i] = flow.PromptBundle{
			Name:   labels[i],
			Role:   labels[i],
			Action: runner.ActionParallel,
			Render: func(flow.RuntimeEnv) (string, error) {
				return prompt, nil
			},
			RunOpts: func(flow.RuntimeEnv) runner.RunOpts {
				return runner.RunOpts{
					RoleID:     labels[i],
					Action:     runner.ActionParallel,
					WorkDir:    env.WorkDir,
					TimeoutMin: parallelTimeout,
					Verbose:    parallelVerbose,
					Batch:      batch,
				}
			},
		}
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
		Reporter: tr,
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
		multiTask := len(resolvedPrompts) > 1
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
