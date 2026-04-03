package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ateam/internal/display"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/spf13/cobra"
)

const (
	reportStateQueued  = "queued"
	reportStateRunning = "running"
	reportStateDone    = "done"
	reportStateError   = "ERROR"
	reportStatusHeader = "  ID      ROLE                      STATUS   CALLS  DETAILS"
)

var (
	reportRoles                []string
	reportExtraPrompt          string
	reportTimeout              int
	reportParallel             int
	reportPrint                bool
	reportDryRun               bool
	reportIgnorePreviousReport bool
	reportCheaperModel         bool
	reportProfile              string
	reportAgent                string
	reportVerbose              bool
	reportForce                bool
	reportReview               bool
	reportDockerAutoSetup      bool
)

// ReportOptions holds configuration for a report run.
type ReportOptions struct {
	Roles                []string
	ExtraPrompt          string
	Timeout              int
	Parallel             int
	Print                bool
	DryRun               bool
	IgnorePreviousReport bool
	CheaperModel         bool
	Profile              string
	Agent                string
	Verbose              bool
	Force                bool
	Review               bool
	DockerAutoSetup      bool
}

var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "Run roles to produce analysis reports",
	Long: `Run one or more roles in parallel to analyze the project source code
and produce markdown reports. Defaults to all enabled roles.

Works from any project directory — discovers the .ateamorg/ and .ateam/ structure.

Example:
  ateam report
  ateam report --roles testing_basic,security
  ateam report --roles refactor_small --extra-prompt "Focus on the auth module"
  ateam report --extra-prompt @notes.md`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runReport(ReportOptions{
			Roles:                reportRoles,
			ExtraPrompt:          reportExtraPrompt,
			Timeout:              reportTimeout,
			Parallel:             reportParallel,
			Print:                reportPrint,
			DryRun:               reportDryRun,
			IgnorePreviousReport: reportIgnorePreviousReport,
			CheaperModel:         reportCheaperModel,
			Profile:              reportProfile,
			Agent:                reportAgent,
			Verbose:              reportVerbose,
			Force:                reportForce,
			Review:               reportReview,
			DockerAutoSetup:      reportDockerAutoSetup,
		})
	},
}

func init() {
	reportCmd.Flags().StringSliceVar(&reportRoles, "roles", nil, prompts.RoleFlagUsage()+" (default: all)")
	reportCmd.Flags().StringVar(&reportExtraPrompt, "extra-prompt", "", "additional instructions (text or @filepath)")
	reportCmd.Flags().IntVar(&reportTimeout, "timeout", 0, "timeout in minutes per role (overrides config)")
	reportCmd.Flags().IntVar(&reportParallel, "parallel", 0, "max parallel roles (overrides config max_parallel)")
	reportCmd.Flags().BoolVar(&reportPrint, "print", false, "print reports to stdout after completion")
	reportCmd.Flags().BoolVar(&reportReview, "review", false, "run review automatically after reports complete")
	reportCmd.Flags().BoolVar(&reportDryRun, "dry-run", false, "print the computed prompt for each role without running")
	reportCmd.Flags().BoolVar(&reportIgnorePreviousReport, "ignore-previous-report", false, "do not include the role's previous report in the prompt")
	addCheaperModelFlag(reportCmd, &reportCheaperModel)
	addProfileFlags(reportCmd, &reportProfile, &reportAgent)
	addVerboseFlag(reportCmd, &reportVerbose)
	addForceFlag(reportCmd, &reportForce)
	addDockerAutoSetupFlag(reportCmd, &reportDockerAutoSetup)
}

func runReport(opts ReportOptions) error {
	env, err := root.Resolve(orgFlag, projectFlag)
	if err != nil {
		return err
	}

	roles := opts.Roles
	if len(roles) == 0 {
		roles = []string{"all"}
	}
	roleIDs, err := prompts.ResolveRoleList(roles, env.Config.Roles, env.ProjectDir, env.OrgDir)
	if err != nil {
		return err
	}

	if err := root.EnsureRoles(env.ProjectDir, roleIDs); err != nil {
		return err
	}

	extraPrompt, err := prompts.ResolveOptional(opts.ExtraPrompt)
	if err != nil {
		return err
	}

	timeout := env.Config.Report.EffectiveTimeout(opts.Timeout)

	cr, err := resolveRunner(env, opts.Profile, opts.Agent, runner.ActionReport, "", opts.DockerAutoSetup)
	if err != nil {
		return err
	}
	applyCheaperModel(cr, opts.CheaperModel)

	db := openProjectDB(env)
	if db != nil {
		defer db.Close()
		cr.CallDB = db
	}

	if !opts.Force {
		if err := checkConcurrentRuns(db, "", runner.ActionReport, roleIDs); err != nil {
			return err
		}
	}

	taskGroup := "report-" + time.Now().Format(runner.TimestampFormat)

	basePinfo := env.NewProjectInfoParams("", "report")
	var tasks []runner.PoolTask
	for _, roleID := range roleIDs {
		pinfo := basePinfo
		pinfo.Role = "role " + roleID
		prompt, err := prompts.AssembleRolePrompt(env.OrgDir, env.ProjectDir, roleID, env.SourceDir, extraPrompt, pinfo, opts.IgnorePreviousReport)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: skipping %s — %v\n", roleID, err)
			continue
		}
		roleDir := env.RoleDir(roleID)
		tasks = append(tasks, runner.PoolTask{
			Prompt: prompt,
			RunOpts: runner.RunOpts{
				RoleID:               roleID,
				Action:               runner.ActionReport,
				LogsDir:              env.RoleLogsDir(roleID),
				LastMessageFilePath:  env.RoleReportPath(roleID),
				ErrorMessageFilePath: filepath.Join(roleDir, prompts.ReportErrorFile),
				WorkDir:              env.SourceDir,
				TimeoutMin:           timeout,
				HistoryDir:           env.RoleHistoryDir(roleID),
				PromptName:           "report_prompt.md",
				Verbose:              opts.Verbose,
				TaskGroup:            taskGroup,
			},
		})
	}

	if len(tasks) == 0 {
		return fmt.Errorf("no valid roles to run")
	}

	if opts.DryRun {
		for i, t := range tasks {
			if i > 0 {
				fmt.Println()
			}
			fmt.Printf("╔══ %s ══╗\n\n", t.RoleID)
			fmt.Println(t.Prompt)
			fmt.Printf("\n╚══ %s ══╝\n", t.RoleID)
		}
		return nil
	}

	maxParallel := env.Config.Report.EffectiveMaxParallel(opts.Parallel)

	reportStart := time.Now()

	fmt.Printf("Running %d role(s) (max %d parallel, %dm timeout)...\n\n",
		len(tasks), maxParallel, timeout)

	cwd, _ := os.Getwd()

	// Build role order index for status display
	statusRows, roleIndex := newReportStatusRows(tasks)
	renderedRows := printReportStatuses(statusRows)

	// Process completions and progress as they arrive
	completed := make(chan runner.RunSummary, len(tasks))
	progress := make(chan runner.RunProgress, 64)
	var succeeded, failed int
	var results []runner.RunSummary
	var statusMu sync.Mutex

	ctx, stop := cmdContext()
	defer stop()
	go func() {
		runner.RunPool(ctx, cr, tasks, maxParallel, progress, completed)
		close(progress)
	}()

	resizeCh, stopResize := subscribeWindowResize()
	var resizeDone sync.WaitGroup
	if resizeCh != nil {
		resizeDone.Add(1)
		go func() {
			defer resizeDone.Done()
			for range resizeCh {
				statusMu.Lock()
				renderedRows = reprintReportStatuses(statusRows, renderedRows)
				statusMu.Unlock()
			}
		}()
	}
	defer func() {
		stopResize()
		resizeDone.Wait()
	}()

	agentName := cr.Agent.Name()

	// Consume progress events to update in-flight status lines.
	// Rate-limit terminal redraws to avoid excessive output with many parallel roles.
	var progressDone sync.WaitGroup
	progressDone.Add(1)
	go func() {
		defer progressDone.Done()
		var lastRedraw time.Time
		for p := range progress {
			idx, ok := roleIndex[p.RoleID]
			if !ok {
				continue
			}
			statusMu.Lock()
			statusRows[idx] = nextReportStatusRow(statusRows[idx], p)
			if time.Since(lastRedraw) >= 500*time.Millisecond {
				renderedRows = reprintReportStatuses(statusRows, renderedRows)
				lastRedraw = time.Now()
			}
			statusMu.Unlock()
		}
	}()

	for r := range completed {
		statusMu.Lock()
		idx := roleIndex[r.RoleID]
		if r.Err != nil {
			statusRows[idx] = errorReportStatusRow(statusRows[idx], r, cwd)
			failed++
		} else {
			reportPath := env.RoleReportPath(r.RoleID)
			historyDir := env.RoleHistoryDir(r.RoleID)
			if err := runner.ArchiveFile(reportPath, historyDir, prompts.ReportFile, r.StartedAt); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not archive report for %s: %v\n", r.RoleID, err)
			}
			statusRows[idx] = doneReportStatusRow(statusRows[idx], r, relPath(cwd, reportPath))
			succeeded++
		}
		renderedRows = reprintReportStatuses(statusRows, renderedRows)
		statusMu.Unlock()
		results = append(results, r)
	}
	progressDone.Wait()
	statusMu.Lock()
	finalRows := cloneReportStatusRows(statusRows)
	if ctx.Err() == nil {
		renderedRows = reprintReportStatuses(finalRows, renderedRows)
	}
	statusMu.Unlock()

	if ctx.Err() != nil {
		fmt.Println()
		printPlainReportStatuses(finalRows)
	}

	fmt.Printf("\n%d succeeded, %d failed (%s)\n", succeeded, failed, runner.FormatDuration(time.Since(reportStart)))

	if failed > 0 {
		for _, r := range results {
			if r.Err == nil {
				continue
			}
			tail := runner.StreamTailError(r.StreamFilePath, agentName, 5)
			if tail == "" {
				continue
			}
			fmt.Printf("\n  %s:\n", r.RoleID)
			for _, line := range strings.Split(tail, "\n") {
				fmt.Printf("        %s\n", line)
			}
		}
	}

	if opts.Print && succeeded > 0 {
		for _, r := range results {
			if r.Err != nil {
				continue
			}
			fmt.Printf("\n══════ %s ══════\n\n%s\n", r.RoleID, r.Output)
		}
	}

	if failed > 0 {
		return fmt.Errorf("%d role(s) failed", failed)
	}

	if opts.Review && succeeded > 0 {
		fmt.Println()
		return runReview(ReviewOptions{})
	}

	if succeeded > 0 {
		fmt.Printf("\nRun 'ateam review' to have the supervisor synthesize findings.\n")
	}

	return nil
}

type reportStatusRow struct {
	ExecID int64
	RoleID string
	State  string
	Calls  int
	Detail string
	Path   string
}

func newReportStatusRows(tasks []runner.PoolTask) ([]reportStatusRow, map[string]int) {
	rows := make([]reportStatusRow, len(tasks))
	index := make(map[string]int, len(tasks))
	for i, t := range tasks {
		index[t.RoleID] = i
		rows[i] = reportStatusRow{
			RoleID: t.RoleID,
			State:  reportStateQueued,
		}
	}
	return rows, index
}

func cloneReportStatusRows(rows []reportStatusRow) []reportStatusRow {
	return append([]reportStatusRow(nil), rows...)
}

// streamFilePrefix returns the log file prefix (without _stream.jsonl suffix)
// relative to cwd, with a trailing "*" glob hint.
func streamFilePrefix(streamPath, cwd string) string {
	// Stream files are named <prefix>_stream.jsonl — strip the suffix.
	prefix := strings.TrimSuffix(streamPath, "_stream.jsonl")
	rel := relPath(cwd, prefix)
	return rel + "*"
}

func reportStatusLinesForWidth(rows []reportStatusRow, width int) []string {
	lines := make([]string, 0, len(rows)+1)
	lines = append(lines, fitReportLine(reportStatusHeader, width))
	for _, row := range rows {
		lines = append(lines, reportStatusRowLines(row, width)...)
	}
	return lines
}

func reportStatusRowLines(row reportStatusRow, width int) []string {
	execID := "-"
	if row.ExecID > 0 {
		execID = strconv.FormatInt(row.ExecID, 10)
	}
	calls := "-"
	if row.State != reportStateQueued || row.Calls > 0 {
		calls = strconv.Itoa(row.Calls)
	}
	line := strings.TrimRight(fmt.Sprintf("  %-7s %-25s %-8s %-6s %s", execID, row.RoleID, row.State, calls, row.Detail), " ")
	if row.State != reportStateDone || row.Path == "" {
		return []string{fitReportLine(line, width)}
	}
	return []string{
		fitReportLine(line, width),
		row.Path,
	}
}

func fitReportLine(line string, width int) string {
	line = strings.TrimRight(line, " ")
	if width <= 1 {
		return line
	}
	limit := width - 1
	runes := []rune(line)
	if len(runes) <= limit {
		return line
	}
	if limit == 1 {
		return "…"
	}
	return string(runes[:limit-1]) + "…"
}

func formatRunningToolDetail(elapsed, toolName string, toolCount int) string {
	label := "tool calls"
	if toolCount == 1 {
		label = "tool call"
	}
	return strings.TrimSpace(fmt.Sprintf("%s  %s (%d %s)", elapsed, toolName, toolCount, label))
}

func reportStatusTerminal(state string) bool {
	return state == reportStateDone || state == reportStateError
}

func nextReportStatusRow(row reportStatusRow, p runner.RunProgress) reportStatusRow {
	if reportStatusTerminal(row.State) {
		return row
	}
	elapsed := runner.FormatDuration(p.Elapsed)
	next := row
	next.ExecID = p.ExecID
	next.Calls = p.ToolCount
	switch p.Phase {
	case runner.PhaseInit:
		next.State = reportStateRunning
		next.Detail = elapsed
	case runner.PhaseTool:
		next.State = reportStateRunning
		next.Detail = formatRunningToolDetail(elapsed, p.ToolName, p.ToolCount)
	case runner.PhaseDone:
		next.State = reportStateDone
		next.Detail = elapsed
	case runner.PhaseError:
		next.State = reportStateError
		next.Detail = elapsed
	default:
		next.State = reportStateRunning
		next.Detail = elapsed
	}
	return next
}

func finalizedReportStatusRow(row reportStatusRow, summary runner.RunSummary, state, detail, path string) reportStatusRow {
	next := row
	next.ExecID = summary.ExecID
	next.State = state
	next.Detail = detail
	next.Path = path
	return next
}

func errorReportStatusRow(row reportStatusRow, summary runner.RunSummary, cwd string) reportStatusRow {
	return finalizedReportStatusRow(row, summary, reportStateError, strings.TrimSpace(fmt.Sprintf("%s  %s  %s  %s",
		summary.EndedAt.Format("15:04:05"),
		runner.FormatDuration(summary.Duration),
		reportStatusTokens(summary),
		streamFilePrefix(summary.StreamFilePath, cwd),
	)), "")
}

func doneReportStatusRow(row reportStatusRow, summary runner.RunSummary, reportPath string) reportStatusRow {
	return finalizedReportStatusRow(row, summary, reportStateDone, strings.TrimSpace(fmt.Sprintf("%s  %s  %s  %s",
		summary.EndedAt.Format("15:04:05"),
		runner.FormatDuration(summary.Duration),
		reportStatusCost(summary),
		reportStatusTokens(summary),
	)), reportPath)
}

func reportStatusCost(summary runner.RunSummary) string {
	cost := display.FmtCost(summary.Cost)
	if cost == "" {
		return "$0.00"
	}
	return cost
}

func reportStatusTokens(summary runner.RunSummary) string {
	return display.FmtTokens(int64(summary.InputTokens + summary.OutputTokens + summary.CacheReadTokens + summary.CacheWriteTokens))
}

func writeReportStatusLines(w io.Writer, lines []string, clear bool) {
	for _, line := range lines {
		if clear {
			fmt.Fprintf(w, "\r\033[2K%s\n", line)
			continue
		}
		fmt.Fprintln(w, line)
	}
}

func saveReportStatusAnchor(w io.Writer) {
	fmt.Fprint(w, "\r\033[2K\0337")
}

func redrawReportStatusLines(w io.Writer, lines []string, previousRows int, width int) int {
	currentRows := totalVisualRows(lines, width)
	fmt.Fprint(w, "\0338")
	if previousRows > 0 {
		fmt.Fprintf(w, "\033[%dA", previousRows)
	}
	fmt.Fprint(w, "\033[J")
	writeReportStatusLines(w, lines, true)
	saveReportStatusAnchor(w)
	return currentRows
}

func visualRowsForLine(line string, width int) int {
	if width <= 0 {
		return 1
	}
	runes := len([]rune(line))
	if runes == 0 {
		return 1
	}
	rows := runes / width
	if runes%width != 0 {
		rows++
	}
	if rows < 1 {
		return 1
	}
	return rows
}

func totalVisualRows(lines []string, width int) int {
	total := 0
	for _, line := range lines {
		total += visualRowsForLine(line, width)
	}
	return total
}

func currentReportStatusLines(rows []reportStatusRow) ([]string, int) {
	width := stdoutWidth()
	return reportStatusLinesForWidth(rows, width), width
}

// printReportStatuses prints the report status table.
func printReportStatuses(rows []reportStatusRow) int {
	lines, width := currentReportStatusLines(rows)
	writeReportStatusLines(os.Stdout, lines, false)
	saveReportStatusAnchor(os.Stdout)
	return totalVisualRows(lines, width)
}

func printPlainReportStatuses(rows []reportStatusRow) {
	writeReportStatusLines(os.Stdout, reportStatusLinesForWidth(rows, 0), false)
}

// reprintReportStatuses redraws the report status table in place.
func reprintReportStatuses(rows []reportStatusRow, previousRows int) int {
	lines, width := currentReportStatusLines(rows)
	return redrawReportStatusLines(os.Stdout, lines, previousRows, width)
}
