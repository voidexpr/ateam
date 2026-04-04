package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/spf13/cobra"
)

var (
	runManyLabels           []string
	runManyTaskGroup        string
	runManyMaxParallel      int
	runManyNoProgress       bool
	runManyCommonPromptFirst string
	runManyCommonPromptLast  string
	runManyProfile          string
	runManyAgent            string
	runManyModel            string
	runManyWorkDir          string
	runManyTimeout          int
	runManyVerbose          bool
	runManyForce            bool
	runManyDryRun           bool
	runManyPrint            bool
	runManyDockerAutoSetup  bool
)

var runManyCmd = &cobra.Command{
	Use:   "run-many PROMPT_OR_@FILE...",
	Short: "Run multiple agents in parallel",
	Long: `Run multiple agents in parallel, each with its own prompt.

Each positional argument is a prompt (text or @filepath). All tasks share a
single runner instance and task group for unified cost tracking.

Example:
  ateam run-many "analyze auth module" "analyze payment module"
  ateam run-many @task1.md @task2.md @task3.md --labels auth,payment,users
  ateam run-many "task A" "task B" --max-parallel 1 --common-prompt-first @context.md`,
	Args: cobra.MinimumNArgs(1),
	RunE: runRunMany,
}

func init() {
	runManyCmd.Flags().StringSliceVar(&runManyLabels, "labels", nil, "names for each task (comma-separated, must match prompt count)")
	runManyCmd.Flags().StringVar(&runManyTaskGroup, "task-group", "", "group related calls (default: run-many-TIMESTAMP)")
	runManyCmd.Flags().IntVar(&runManyMaxParallel, "max-parallel", 3, "max parallel tasks")
	runManyCmd.Flags().BoolVar(&runManyNoProgress, "no-progress", false, "suppress ANSI progress table")
	runManyCmd.Flags().StringVar(&runManyCommonPromptFirst, "common-prompt-first", "", "text or @filepath to prepend to each prompt")
	runManyCmd.Flags().StringVar(&runManyCommonPromptLast, "common-prompt-last", "", "text or @filepath to append to each prompt")
	addProfileFlags(runManyCmd, &runManyProfile, &runManyAgent)
	runManyCmd.Flags().StringVar(&runManyModel, "model", "", "model override")
	runManyCmd.Flags().StringVar(&runManyWorkDir, "work-dir", "", "working directory (defaults to project source dir or cwd)")
	runManyCmd.Flags().IntVar(&runManyTimeout, "timeout", 0, "timeout in minutes per task")
	addVerboseFlag(runManyCmd, &runManyVerbose)
	addForceFlag(runManyCmd, &runManyForce)
	runManyCmd.Flags().BoolVar(&runManyDryRun, "dry-run", false, "print computed prompts without running")
	runManyCmd.Flags().BoolVar(&runManyPrint, "print", false, "print task outputs to stdout after completion")
	addDockerAutoSetupFlag(runManyCmd, &runManyDockerAutoSetup)
}

func runRunMany(cmd *cobra.Command, args []string) error {
	resolvedPrompts := make([]string, len(args))
	for i, arg := range args {
		p, err := prompts.ResolveValue(arg)
		if err != nil {
			return fmt.Errorf("cannot resolve prompt %d: %w", i+1, err)
		}
		resolvedPrompts[i] = p
	}

	commonFirst, err := prompts.ResolveOptional(runManyCommonPromptFirst)
	if err != nil {
		return fmt.Errorf("cannot resolve common-prompt-first: %w", err)
	}
	commonLast, err := prompts.ResolveOptional(runManyCommonPromptLast)
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

	labels := runManyLabels
	if len(labels) > 0 {
		if len(labels) != len(resolvedPrompts) {
			return fmt.Errorf("--labels count (%d) must match prompt count (%d)", len(labels), len(resolvedPrompts))
		}
	} else {
		labels = make([]string, len(resolvedPrompts))
		for i := range labels {
			labels[i] = fmt.Sprintf("task-%d", i+1)
		}
	}

	if runManyDryRun {
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

	env, err := root.Lookup()
	if err != nil {
		return fmt.Errorf("cannot find .ateamorg/: %w", err)
	}
	hasProject := env.ProjectDir != "" && env.Config != nil

	workDir := ""
	if runManyWorkDir != "" {
		abs, err := filepath.Abs(runManyWorkDir)
		if err != nil {
			return fmt.Errorf("cannot resolve work-dir: %w", err)
		}
		workDir = abs
	} else if hasProject {
		workDir = env.SourceDir
	}

	var r *runner.Runner
	if hasProject {
		r, err = resolveRunner(env, runManyProfile, runManyAgent, runner.ActionRunMany, "", runManyDockerAutoSetup)
	} else {
		profile := runManyProfile
		if profile == "" && runManyAgent == "" {
			profile = "default"
		}
		r, err = resolveRunnerMinimal(env.OrgDir, profile, runManyAgent)
	}
	if err != nil {
		return err
	}
	setSourceWritable(r)

	if runManyModel != "" {
		if ca, ok := r.Agent.(*agent.ClaudeAgent); ok {
			ca.Model = runManyModel
		}
	}

	db := openProjectDB(env)
	if db != nil {
		defer db.Close()
		r.CallDB = db
	}

	taskGroup := runManyTaskGroup
	if taskGroup == "" {
		taskGroup = "run-many-" + time.Now().Format(runner.TimestampFormat)
	}

	if !runManyForce {
		if err := checkConcurrentRuns(db, "", runner.ActionRunMany, nil); err != nil {
			return err
		}
	}

	baseLogsDir := env.OrgDir
	if hasProject {
		baseLogsDir = env.ProjectDir
	}

	tasks := make([]runner.PoolTask, len(resolvedPrompts))
	for i, prompt := range resolvedPrompts {
		tasks[i] = runner.PoolTask{
			Prompt: prompt,
			RunOpts: runner.RunOpts{
				RoleID:    labels[i],
				Action:    runner.ActionRunMany,
				LogsDir:   filepath.Join(baseLogsDir, "logs", "run-many", labels[i]),
				WorkDir:   workDir,
				TimeoutMin: runManyTimeout,
				Verbose:   runManyVerbose,
				TaskGroup: taskGroup,
				PromptName: "run_many_prompt.md",
			},
		}
	}

	maxParallel := runManyMaxParallel
	if maxParallel <= 0 {
		maxParallel = 3
	}

	start := time.Now()
	fmt.Fprintf(os.Stderr, "Running %d task(s) (max %d parallel)...\n\n", len(tasks), maxParallel)

	cwd, _ := os.Getwd()
	agentName := r.Agent.Name()

	useTable := isTerminal() && !runManyNoProgress
	var statusRows []poolStatusRow
	var labelIndex map[string]int
	var renderedRows int

	if useTable {
		statusRows, labelIndex = newPoolStatusRows(labels)
		renderedRows = printPoolStatuses(statusRows)
	}

	completedCh := make(chan runner.RunSummary, len(tasks))
	progressCh := make(chan runner.RunProgress, 64)
	var succeeded, failed int
	var results []runner.RunSummary
	var statusMu sync.Mutex

	ctx, stop := cmdContext()
	defer stop()

	go func() {
		runner.RunPool(ctx, r, tasks, maxParallel, progressCh, completedCh)
		close(progressCh)
	}()

	var resizeDone sync.WaitGroup
	if useTable {
		resizeCh, stopResize := subscribeWindowResize()
		if resizeCh != nil {
			resizeDone.Add(1)
			go func() {
				defer resizeDone.Done()
				for range resizeCh {
					statusMu.Lock()
					renderedRows = reprintPoolStatuses(statusRows, renderedRows)
					statusMu.Unlock()
				}
			}()
		}
		defer func() {
			stopResize()
			resizeDone.Wait()
		}()
	}

	var progressDone sync.WaitGroup
	progressDone.Add(1)
	go func() {
		defer progressDone.Done()
		if useTable {
			var lastRedraw time.Time
			for p := range progressCh {
				idx, ok := labelIndex[p.RoleID]
				if !ok {
					continue
				}
				statusMu.Lock()
				statusRows[idx] = nextPoolStatusRow(statusRows[idx], p)
				if time.Since(lastRedraw) >= 500*time.Millisecond {
					renderedRows = reprintPoolStatuses(statusRows, renderedRows)
					lastRedraw = time.Now()
				}
				statusMu.Unlock()
			}
		} else {
			printProgress(progressCh)
		}
	}()

	for result := range completedCh {
		if useTable {
			statusMu.Lock()
			idx := labelIndex[result.RoleID]
			if result.Err != nil {
				statusRows[idx] = errorPoolStatusRow(statusRows[idx], result, cwd)
				failed++
			} else {
				statusRows[idx] = donePoolStatusRow(statusRows[idx], result, "")
				succeeded++
			}
			renderedRows = reprintPoolStatuses(statusRows, renderedRows)
			statusMu.Unlock()
		} else {
			if result.Err != nil {
				failed++
			} else {
				succeeded++
			}
		}
		if !runManyPrint {
			result.Output = ""
		}
		results = append(results, result)
	}
	progressDone.Wait()

	if useTable {
		statusMu.Lock()
		finalRows := clonePoolStatusRows(statusRows)
		if ctx.Err() == nil {
			renderedRows = reprintPoolStatuses(finalRows, renderedRows)
		}
		statusMu.Unlock()

		if ctx.Err() != nil {
			fmt.Println()
			printPlainPoolStatuses(finalRows)
		}
	}

	fmt.Fprintf(os.Stderr, "\n%d succeeded, %d failed (%s)\n", succeeded, failed, runner.FormatDuration(time.Since(start)))

	if failed > 0 {
		for _, result := range results {
			if result.Err == nil {
				continue
			}
			tail := runner.StreamTailError(result.StreamFilePath, agentName, 5)
			if tail == "" {
				continue
			}
			fmt.Fprintf(os.Stderr, "\n  %s:\n", result.RoleID)
			for _, line := range strings.Split(tail, "\n") {
				fmt.Fprintf(os.Stderr, "        %s\n", line)
			}
		}
	}

	// Print outputs in submission order
	if runManyPrint && succeeded > 0 {
		// Build output map keyed by label
		outputByLabel := make(map[string]string, len(results))
		for _, result := range results {
			if result.Err == nil {
				outputByLabel[result.RoleID] = result.Output
			}
		}
		multiTask := len(tasks) > 1
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

	if failed > 0 {
		return fmt.Errorf("%d task(s) failed", failed)
	}

	return nil
}
