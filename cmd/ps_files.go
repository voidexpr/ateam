package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/spf13/cobra"
)

var (
	psFilesTaskGroup  string
	psFilesLastRun    bool
	psFilesLastReport bool
	psFilesLastReview bool
	psFilesLastCode   bool
	psFilesAutoDebug       bool
	psFilesAutoDebugPrompt bool
	psFilesProfile         string
	psFilesAgent           string
)

var psFilesCmd = &cobra.Command{
	Use:   "ps-files [ID...]",
	Short: "Show log files for agent runs",
	Long: `Display the ps summary and log files for one or more runs.

Select runs by ID, task group, or shorthand flags.

Example:
  ateam ps-files 42
  ateam ps-files 42 43
  ateam ps-files --last-run
  ateam ps-files --last-report
  ateam ps-files --last-report --auto-debug`,
	RunE: runPsFiles,
}

func init() {
	psFilesCmd.Flags().StringVar(&psFilesTaskGroup, "task-group", "", "select all runs in a task group")
	psFilesCmd.Flags().BoolVar(&psFilesLastRun, "last-run", false, "select the most recent run")
	psFilesCmd.Flags().BoolVar(&psFilesLastReport, "last-report", false, "select all tasks from the last report batch")
	psFilesCmd.Flags().BoolVar(&psFilesLastReview, "last-review", false, "select the last review run")
	psFilesCmd.Flags().BoolVar(&psFilesLastCode, "last-code", false, "select all tasks from the last code session")
	psFilesCmd.Flags().BoolVar(&psFilesAutoDebug, "auto-debug", false, "launch an agent to investigate the selected runs")
	psFilesCmd.Flags().BoolVar(&psFilesAutoDebugPrompt, "auto-debug-prompt", false, "print the auto-debug prompt without executing")
	addProfileFlags(psFilesCmd, &psFilesProfile, &psFilesAgent)
}

func runPsFiles(cmd *cobra.Command, args []string) error {
	env, err := root.Lookup()
	if err != nil {
		return fmt.Errorf("cannot find project: %w", err)
	}

	db := openProjectDB(env)
	if db == nil {
		return fmt.Errorf("cannot open call database")
	}
	defer db.Close()

	rows, err := resolveRunSelection(db, env, args)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return fmt.Errorf("no runs found")
	}

	printRunsTable(rows)

	cwd, _ := os.Getwd()
	var allFiles []string

	for _, r := range rows {
		files := logFilesForRun(env, r)
		if len(files) == 0 {
			continue
		}

		started := fmtStartedAt(r.StartedAt)
		fmt.Printf("\n=== [ID:%d] %s/%s %s ===\n", r.ID, r.Role, r.Action, started)

		for _, f := range files {
			info, statErr := os.Stat(f)
			size := ""
			if statErr == nil {
				size = fmtFileSize(info.Size())
			}
			rel := relPath(cwd, f)
			fmt.Printf("  %-8s %s\n", size, rel)
			allFiles = append(allFiles, rel)
		}
	}

	if psFilesAutoDebug || psFilesAutoDebugPrompt {
		debugContext := buildDebugContext(rows, allFiles)
		pinfo := env.NewProjectInfoParams("task debugger")
		prompt, err := prompts.AssembleTaskDebugPrompt(env.OrgDir, env.ProjectDir, debugContext, pinfo)
		if err != nil {
			return err
		}
		if psFilesAutoDebugPrompt {
			fmt.Println(prompt)
			return nil
		}
		fmt.Printf("\n--- Auto-debug ---\n")
		return launchAutoDebug(env, prompt)
	}

	return nil
}

func resolveRunSelection(db *calldb.CallDB, env *root.ResolvedEnv, args []string) ([]calldb.RecentRow, error) {
	if len(args) > 0 {
		ids, err := parseIDArgs(args)
		if err != nil {
			return nil, err
		}
		return recentRowsByIDs(db, ids)
	}

	if psFilesTaskGroup != "" {
		return db.RecentRuns(calldb.RecentFilter{TaskGroup: psFilesTaskGroup})
	}

	if psFilesLastReport {
		tg, err := db.LatestTaskGroup("", "report-")
		if err != nil {
			return nil, err
		}
		if tg == "" {
			return nil, fmt.Errorf("no report runs found")
		}
		return db.RecentRuns(calldb.RecentFilter{TaskGroup: tg})
	}

	if psFilesLastCode {
		tg, err := db.LatestTaskGroup("", "code-")
		if err != nil {
			return nil, err
		}
		if tg == "" {
			return nil, fmt.Errorf("no code runs found")
		}
		return db.RecentRuns(calldb.RecentFilter{TaskGroup: tg})
	}

	if psFilesLastReview {
		rows, err := db.RecentRuns(calldb.RecentFilter{Action: "review", Limit: 1})
		if err != nil {
			return nil, err
		}
		return rows, nil
	}

	if psFilesLastRun {
		rows, err := db.RecentRuns(calldb.RecentFilter{Limit: 1})
		if err != nil {
			return nil, err
		}
		return rows, nil
	}

	return nil, fmt.Errorf("specify task IDs or use --last-run, --last-report, --last-review, --last-code, or --task-group")
}

func recentRowsByIDs(db *calldb.CallDB, ids []int64) ([]calldb.RecentRow, error) {
	var rows []calldb.RecentRow
	for _, id := range ids {
		r, err := db.GetRunByID(id)
		if err != nil {
			return nil, fmt.Errorf("query failed for ID %d: %w", id, err)
		}
		if r == nil {
			return nil, fmt.Errorf("no run found with ID %d", id)
		}
		rows = append(rows, *r)
	}
	return rows, nil
}

func logFilesForRun(env *root.ResolvedEnv, r calldb.RecentRow) []string {
	if r.StreamFile == "" {
		return nil
	}
	streamPath := root.ResolveStreamPath(env.ProjectDir, env.OrgDir, r.StreamFile)
	prefix := strings.TrimSuffix(streamPath, "_stream.jsonl")

	suffixes := []string{"_exec.md", "_settings.json", "_stream.jsonl", "_stderr.log"}
	var files []string
	for _, s := range suffixes {
		path := prefix + s
		if _, err := os.Stat(path); err == nil {
			files = append(files, path)
		}
	}
	return files
}

func buildDebugContext(rows []calldb.RecentRow, filePaths []string) string {
	var b strings.Builder
	for _, r := range rows {
		started := fmtStartedAt(r.StartedAt)
		fmt.Fprintf(&b, "## Run ID:%d — %s/%s\n", r.ID, r.Role, r.Action)
		fmt.Fprintf(&b, "- Started: %s\n", started)
		fmt.Fprintf(&b, "- Profile: %s\n", r.Profile)
		fmt.Fprintf(&b, "- Model: %s\n", r.Model)
		if r.ExitCode != 0 {
			fmt.Fprintf(&b, "- Exit code: %d\n", r.ExitCode)
		}
		if r.IsError {
			b.WriteString("- Status: ERROR\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("## Log files to examine\n\n")
	for _, f := range filePaths {
		fmt.Fprintf(&b, "- %s\n", f)
	}
	return b.String()
}

func launchAutoDebug(env *root.ResolvedEnv, prompt string) error {
	r, err := resolveRunner(env, psFilesProfile, psFilesAgent, runner.ActionDebug, "")
	if err != nil {
		return err
	}

	setSourceWritable(r)

	dbForRun := openProjectDB(env)
	if dbForRun != nil {
		defer dbForRun.Close()
		r.CallDB = dbForRun
	}

	logsDir := env.SupervisorLogsDir()
	reportPath := filepath.Join(logsDir, time.Now().Format(runner.TimestampFormat)+"_debug_report.md")

	progress := make(chan runner.RunProgress, 64)
	var progressWg sync.WaitGroup
	progressWg.Add(1)
	go func() {
		defer progressWg.Done()
		printProgress(progress)
	}()

	ctx, stop := cmdContext()
	defer stop()

	summary := r.Run(ctx, prompt, runner.RunOpts{
		RoleID:              "supervisor",
		Action:              runner.ActionDebug,
		LogsDir:             logsDir,
		WorkDir:             env.SourceDir,
		Verbose:             true,
		PromptName:          "auto_debug_prompt.md",
		LastMessageFilePath: reportPath,
	}, progress)

	close(progress)
	progressWg.Wait()

	if f, err := os.Open(summary.StderrFilePath); err == nil {
		io.Copy(os.Stderr, f)
		f.Close()
	}

	printRunSummary(summary)

	if summary.Err != nil {
		return fmt.Errorf("auto-debug failed: %w", summary.Err)
	}

	// Print the saved debug report
	if data, err := os.ReadFile(reportPath); err == nil && len(data) > 0 {
		cwd, _ := os.Getwd()
		fmt.Printf("\n--- Debug report: %s ---\n\n", relPath(cwd, reportPath))
		fmt.Print(string(data))
		if data[len(data)-1] != '\n' {
			fmt.Println()
		}
	}

	return nil
}

func fmtFileSize(n int64) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%dB", n)
}
