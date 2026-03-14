package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ateam-poc/internal/prompts"
	"github.com/ateam-poc/internal/root"
	"github.com/ateam-poc/internal/runner"
	"github.com/spf13/cobra"
)

var (
	codeReview       string
	codeManagement   string
	codeExtraPrompt  string
	codeTimeout      int
	codePrint        bool
	codeDryRun       bool
	codeCheaperModel bool
	codeProfile      string
	codeAgent        string
	codeVerbose      bool
)

var codeCmd = &cobra.Command{
	Use:   "code",
	Short: "Execute review tasks as code changes",
	Long: `Read the review document and execute prioritized tasks as code changes,
delegating each task to the appropriate role via ateam run.

Example:
  ateam code
  ateam code --review @custom_review.md
  ateam code --management @custom_management.md
  ateam code --dry-run`,
	RunE: runCode,
}

func init() {
	codeCmd.Flags().StringVar(&codeReview, "review", "",
		"review content (text or @filepath; defaults to .ateam/supervisor/review.md)")
	codeCmd.Flags().StringVar(&codeManagement, "management", "",
		"management prompt override (text or @filepath)")
	codeCmd.Flags().StringVar(&codeExtraPrompt, "extra-prompt", "",
		"additional instructions (text or @filepath)")
	codeCmd.Flags().IntVar(&codeTimeout, "timeout", 0,
		"timeout in minutes (overrides config)")
	codeCmd.Flags().BoolVar(&codePrint, "print", false,
		"print output to stdout after completion")
	codeCmd.Flags().BoolVar(&codeDryRun, "dry-run", false,
		"print the computed prompt without running")
	addCheaperModelFlag(codeCmd, &codeCheaperModel)
	addProfileFlags(codeCmd, &codeProfile, &codeAgent)
	addVerboseFlag(codeCmd, &codeVerbose)
}

func runCode(cmd *cobra.Command, args []string) error {
	env, err := root.Resolve(orgFlag, projectFlag)
	if err != nil {
		return err
	}

	var reviewContent string
	if codeReview == "" {
		reviewPath := env.ReviewPath()
		data, err := os.ReadFile(reviewPath)
		if err != nil {
			return fmt.Errorf("no review found at %s; run 'ateam review' first", reviewPath)
		}
		reviewContent = string(data)
	} else {
		reviewContent, err = prompts.ResolveValue(codeReview)
		if err != nil {
			return err
		}
	}

	customManagement, err := prompts.ResolveOptional(codeManagement)
	if err != nil {
		return err
	}

	extraPrompt, err := prompts.ResolveOptional(codeExtraPrompt)
	if err != nil {
		return err
	}

	taskGroup := "code-" + time.Now().Format(runner.TimestampFormat)

	pinfo := env.NewProjectInfoParams("the supervisor")
	prompt, err := prompts.AssembleCodeManagementPrompt(env.OrgDir, env.ProjectDir, env.SourceDir, pinfo, reviewContent, customManagement, extraPrompt)
	if err != nil {
		return err
	}

	// Inject task_group instruction for the supervisor so it passes it to sub-runs.
	prompt += "\n\n# Task Group\n\nYou MUST pass `--task-group " + taskGroup + "` to every `ateam run` command you execute. This groups all sub-tasks for cost tracking.\n"

	if codeDryRun {
		fmt.Printf("╔══ code management ══╗\n\n")
		fmt.Println(prompt)
		fmt.Printf("\n╚══ code management ══╝\n")
		return nil
	}

	timeout := env.Config.Code.EffectiveTimeout(codeTimeout)
	historyDir := env.ReviewHistoryDir()

	if err := os.MkdirAll(historyDir, 0755); err != nil {
		return fmt.Errorf("cannot create history directory: %w", err)
	}

	fmt.Printf("Code management supervisor running (%dm timeout)...\n", timeout)

	supervisorDir := filepath.Join(env.ProjectDir, "supervisor")

	cr, err := resolveRunner(env, codeProfile, codeAgent, runner.ActionCode, "")
	if err != nil {
		return err
	}
	applyCheaperModel(cr, codeCheaperModel)

	db := openCallDB(env.OrgDir)
	if db != nil {
		defer db.Close()
		cr.CallDB = db
	}

	opts := runner.RunOpts{
		RoleID:               "supervisor",
		Action:               runner.ActionCode,
		LogsDir:              env.SupervisorLogsDir(),
		LastMessageFilePath:  filepath.Join(supervisorDir, "code_output.md"),
		ErrorMessageFilePath: filepath.Join(supervisorDir, "code_error.md"),
		WorkDir:              env.SourceDir,
		TimeoutMin:           timeout,
		HistoryDir:           historyDir,
		PromptName:           "code_management_prompt.md",
		Verbose:              codeVerbose,
		TaskGroup:            taskGroup,
	}

	progress := make(chan runner.RunProgress, 64)
	var progressWg sync.WaitGroup
	progressWg.Add(1)
	go func() {
		defer progressWg.Done()
		printProgress(progress)
	}()

	ctx := context.Background()
	result := cr.Run(ctx, prompt, opts, progress)

	close(progress)
	progressWg.Wait()

	if result.Err != nil {
		return fmt.Errorf("code execution failed: %w", result.Err)
	}

	printDone(result)
	fmt.Printf("Output: %s\n", filepath.Join(supervisorDir, "code_output.md"))

	if codePrint {
		fmt.Printf("\n%s\n", result.Output)
	}

	return nil
}
