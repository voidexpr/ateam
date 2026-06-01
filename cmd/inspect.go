package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/prompts/assembler"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/spf13/cobra"
)

var (
	inspectBatch       string
	inspectLast        bool
	inspectLastRun     bool
	inspectLastReport  bool
	inspectLastReview  bool
	inspectLastCode    bool
	inspectAutoDebug   bool
	inspectExtraPrompt string
	inspectPrePrompt   string
	inspectPostPrompt  string
	inspectProfile     string
	inspectAgent       string
)

var inspectCmd = &cobra.Command{
	Use:   "inspect [ID...]",
	Short: "Show log files for agent runs",
	Long: `Display the ps summary and log files for one or more runs.

Select runs by ID, batch, or shorthand flags.

Example:
  ateam inspect 42
  ateam inspect 42 43
  ateam inspect --last
  ateam inspect --last-report
  ateam inspect --last-report --auto-debug`,
	RunE: runPsFiles,
}

func init() {
	inspectCmd.Flags().StringVar(&inspectBatch, "batch", "", "select all runs in a batch")
	inspectCmd.Flags().BoolVar(&inspectLast, "last", false, "select the most recent run (alias for --last-run)")
	inspectCmd.Flags().BoolVar(&inspectLastRun, "last-run", false, "select the most recent run")
	inspectCmd.Flags().BoolVar(&inspectLastReport, "last-report", false, "select all execs from the last report batch")
	inspectCmd.Flags().BoolVar(&inspectLastReview, "last-review", false, "select the last review run")
	inspectCmd.Flags().BoolVar(&inspectLastCode, "last-code", false, "select all execs from the last code session")
	inspectCmd.Flags().BoolVar(&inspectAutoDebug, "auto-debug", false, "launch an agent to investigate the selected runs")
	inspectCmd.Flags().StringVar(&inspectExtraPrompt, "extra-prompt", "", "additional instructions for the auto-debug agent (text or @filepath)")
	inspectCmd.Flags().StringVar(&inspectPrePrompt, "pre-prompt", "", "text wrapped at the very front of the auto-debug prompt (text or @filepath)")
	inspectCmd.Flags().StringVar(&inspectPostPrompt, "post-prompt", "", "text wrapped at the very end of the auto-debug prompt (text or @filepath)")
	addProfileFlags(inspectCmd, &inspectProfile, &inspectAgent)
}

func runPsFiles(cmd *cobra.Command, args []string) error {
	env, err := lookupEnv()
	if err != nil {
		return fmt.Errorf("cannot find project: %w", err)
	}

	db, err := requireStateDB(env)
	if err != nil {
		return err
	}
	defer db.Close()

	rows, err := resolveRunSelection(db, env, args)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return fmt.Errorf("no runs found")
	}

	printRunsTable(rows, false, false)

	cwd, _ := os.Getwd()
	var allFiles []string

	for _, r := range rows {
		files := logFilesForRun(env, r)
		if len(files) == 0 {
			continue
		}

		started := fmtStartedAt(r.StartedAt)
		fmt.Printf("\n=== [ID:%d] %s/%s %s ===\n\n", r.ID, r.Role, r.Action, started)

		if isResumableAgent(r.Agent) {
			streamPath := root.ResolveStreamPath(env.ProjectDir, env.OrgDir, r.AgentFile)
			if sid, err := resolveSessionID(streamPath, r.Agent); err == nil && sid != "" {
				row := r // addressable copy for the shared printer
				printResumeInfo(env, &row, streamPath, sid)
				fmt.Println()
			}
		}
		fmt.Println("Files:")
		fmt.Println()
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

	if inspectAutoDebug {
		prompt, err := buildAutoDebugPrompt(env, rows, allFiles, inspectExtraPrompt, inspectPrePrompt, inspectPostPrompt)
		if err != nil {
			return err
		}
		fmt.Printf("\n--- Auto-debug ---\n")
		return launchAutoDebug(env, prompt)
	}

	return nil
}

// buildAutoDebugPrompt assembles the auto-debug prompt for a set of runs.
// extraPrompt is resolved (text or @filepath) and appended under an
// "Additional Debug Instructions" heading when non-empty. prePrompt /
// postPrompt wrap at the outermost positions; extraPrompt sits between the
// assembled body and post-prompt, matching the supervisor-helper convention.
func buildAutoDebugPrompt(env *root.ResolvedEnv, rows []calldb.RecentRow, files []string, extraPrompt, prePrompt, postPrompt string) (string, error) {
	debugContext := buildDebugContext(rows, files)
	a := env.Assembler()
	vars := env.BuildAssemblerVars("exec_debug", "exec debugger", "debug")
	vars.Exec["debug_context"] = debugContext

	pre, err := prompts.ResolveOptional(prePrompt)
	if err != nil {
		return "", fmt.Errorf("cannot resolve --pre-prompt: %w", err)
	}
	post, err := prompts.ResolveOptional(postPrompt)
	if err != nil {
		return "", fmt.Errorf("cannot resolve --post-prompt: %w", err)
	}
	extra := ""
	if extraPrompt != "" {
		extra, err = prompts.ResolveValue(extraPrompt)
		if err != nil {
			return "", err
		}
	}

	opts := &assembler.AssembleOptions{PrePrompt: pre, PostPrompt: post}
	if extra == "" {
		res, err := a.Assemble("exec_debug", vars, nil, opts)
		if err != nil {
			return "", err
		}
		return res.Prompt, nil
	}
	// extra needs to land between the assembled body and post-prompt;
	// hold post out of the assembler call and append it manually.
	opts.PostPrompt = ""
	res, err := a.Assemble("exec_debug", vars, nil, opts)
	if err != nil {
		return "", err
	}
	prompt := res.Prompt + "\n\n# Additional Debug Instructions\n\n" + extra
	rendered, err := renderCLIWrapper(a, vars, post)
	if err != nil {
		return "", err
	}
	if rendered != "" {
		prompt += "\n\n---\n\n" + rendered
	}
	return prompt, nil
}

func resolveRunSelection(db *calldb.CallDB, env *root.ResolvedEnv, args []string) ([]calldb.RecentRow, error) {
	if len(args) > 0 {
		ids, err := parseIDArgs(args)
		if err != nil {
			return nil, err
		}
		return recentRowsByIDs(db, ids)
	}

	if inspectBatch != "" {
		return db.RecentRuns(calldb.RecentFilter{Batch: inspectBatch})
	}

	if inspectLastReport {
		batch, err := db.LatestBatch(env.ProjectID(), "report-")
		if err != nil {
			return nil, err
		}
		if batch == "" {
			return nil, fmt.Errorf("no report runs found")
		}
		return db.RecentRuns(calldb.RecentFilter{Batch: batch})
	}

	if inspectLastCode {
		batch, err := db.LatestBatch(env.ProjectID(), "code-")
		if err != nil {
			return nil, err
		}
		if batch == "" {
			return nil, fmt.Errorf("no code runs found")
		}
		return db.RecentRuns(calldb.RecentFilter{Batch: batch})
	}

	if inspectLastReview {
		rows, err := db.RecentRuns(calldb.RecentFilter{Action: "review", Limit: 1})
		if err != nil {
			return nil, err
		}
		return rows, nil
	}

	if inspectLast || inspectLastRun {
		rows, err := db.RecentRuns(calldb.RecentFilter{Limit: 1})
		if err != nil {
			return nil, err
		}
		return rows, nil
	}

	return nil, fmt.Errorf("specify exec IDs or use --last, --last-report, --last-review, --last-code, or --batch")
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
	if r.AgentFile == "" {
		return nil
	}
	streamPath := root.ResolveStreamPath(env.ProjectDir, env.OrgDir, r.AgentFile)
	var files []string
	if root.IsLegacyStreamFile(streamPath) {
		prefix := strings.TrimSuffix(streamPath, "_stream.jsonl")
		for _, s := range []string{"_exec.md", "_settings.json", "_stream.jsonl", "_stderr.log"} {
			path := prefix + s
			if _, err := os.Stat(path); err == nil {
				files = append(files, path)
			}
		}
	} else {
		dir := filepath.Dir(streamPath)
		if entries, err := os.ReadDir(dir); err == nil {
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				files = append(files, filepath.Join(dir, e.Name()))
			}
		}
	}
	runtimeDir := env.RuntimeDir(r.ID)
	if entries, err := os.ReadDir(runtimeDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			files = append(files, filepath.Join(runtimeDir, e.Name()))
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
	r, err := resolveRunner(env, inspectProfile, inspectAgent, runner.ActionDebug, "", false)
	if err != nil {
		return err
	}

	setSourceWritable(r)

	dbForRun, err := openStateDB(env)
	if err != nil {
		return err
	}
	defer dbForRun.Close()
	r.CallDB = dbForRun

	progress := make(chan runner.RunProgress, 64)
	var progressWg sync.WaitGroup
	progressWg.Add(1)
	go func() {
		defer progressWg.Done()
		printProgress(progress)
	}()

	ctx, stop := cmdContext()
	defer stop()

	summary := r.Execute(ctx, prompt, runner.RunOpts{
		RoleID:  "supervisor",
		Action:  runner.ActionDebug,
		WorkDir: env.WorkDir,
		Verbose: true,
	}, runner.ProgressChan(progress))

	close(progress)
	progressWg.Wait()

	if f, err := os.Open(summary.StderrFilePath); err == nil {
		_, _ = io.Copy(os.Stderr, f)
		f.Close()
	}

	if summary.Output != "" {
		fmt.Print(summary.Output)
		if summary.Output[len(summary.Output)-1] != '\n' {
			fmt.Println()
		}
	}

	printExecSummary(summary)

	if summary.Err != nil {
		return fmt.Errorf("auto-debug failed: %w", summary.Err)
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
