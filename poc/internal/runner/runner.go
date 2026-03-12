package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ateam-poc/internal/agent"
)

const (
	TimestampFormat = "2006-01-02_15-04-05"

	ActionReport = "report"
	ActionRun    = "run"
	ActionCode   = "code"
	ActionReview = "review"
)

// Runner orchestrates agent execution with logging, file I/O, and progress reporting.
type Runner struct {
	Agent          agent.Agent
	LogFile        string   // append-only runner log
	ProjectDir     string   // .ateam/ dir
	OrgDir         string   // .ateamorg/ dir
	ExtraWriteDirs []string // additional dirs granted sandbox write access
	ExtraArgs      []string // extra args passed to the agent
}

// RunOpts holds per-invocation settings.
type RunOpts struct {
	RoleID               string
	Action               string // "report", "run", "code", "review"
	LogsDir              string // flat dir for all timestamped log files
	LastMessageFilePath  string // where to write extracted report text (on success only)
	ErrorMessageFilePath string // where to write error info (on failure only)
	WorkDir              string // cwd for the subprocess
	TimeoutMin           int
	HistoryDir           string // where to archive the prompt
	PromptName           string // archive name
}

// RunProgress is a lightweight status sent on a channel during execution.
type RunProgress struct {
	RoleID         string
	Phase          string
	ToolName       string
	ToolInput      string
	Content        string
	ToolCount      int
	EventCount     int
	Elapsed        time.Duration
	StartedAt      time.Time
	StreamFilePath string
	StderrFilePath string
}

// RunSummary is the final result returned by Run.
type RunSummary struct {
	RoleID          string
	StartedAt       time.Time
	EndedAt         time.Time
	Duration        time.Duration
	ExitCode        int
	Err             error

	Output          string
	Cost            float64
	DurationMS      int64
	Turns           int
	IsError         bool
	InputTokens     int
	OutputTokens    int
	CacheReadTokens int
	ToolCounts      map[string]int

	StreamFilePath string
	StderrFilePath string
}

// LogQueued writes a "queued" entry to the runner log.
func (r *Runner) LogQueued(opts RunOpts) {
	appendLog(r.LogFile, opts.RoleID, "queued", effectiveWorkDir(opts),
		relToDir(r.ProjectDir, opts.LastMessageFilePath))
}

// Run executes the agent with the given prompt and options.
func (r *Runner) Run(ctx context.Context, prompt string, opts RunOpts, progress chan<- RunProgress) RunSummary {
	startedAt := time.Now()

	prefix := filepath.Join(opts.LogsDir, startedAt.Format(TimestampFormat)+"_"+opts.Action)
	streamFile := prefix + "_stream.jsonl"
	stderrFile := prefix + "_stderr.log"
	execTarget := prefix + "_exec.md"

	failEarly := func(err error) RunSummary {
		s := RunSummary{
			RoleID:         opts.RoleID,
			StartedAt:      startedAt,
			EndedAt:        time.Now(),
			Duration:       time.Since(startedAt),
			ExitCode:       -1,
			Err:            err,
			StreamFilePath: streamFile,
			StderrFilePath: stderrFile,
		}
		writeErrorFile(opts.ErrorMessageFilePath, s, "")
		return s
	}

	if err := os.MkdirAll(opts.LogsDir, 0755); err != nil {
		return failEarly(fmt.Errorf("cannot create logs directory: %w", err))
	}

	if opts.TimeoutMin > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(opts.TimeoutMin)*time.Minute)
		defer cancel()
	}

	cwd := effectiveWorkDir(opts)

	// Build extra args (settings for claude agents, model overrides, etc.)
	extraArgs := make([]string, len(r.ExtraArgs))
	copy(extraArgs, r.ExtraArgs)

	// Write sandbox settings for claude-type agents
	var settingsJSON []byte
	if r.agentNeedsSettings() {
		settingsTarget := prefix + "_settings.json"
		var err error
		settingsJSON, err = r.writeSettings(settingsTarget, opts)
		if err != nil {
			return failEarly(fmt.Errorf("cannot create settings file: %w", err))
		}
		extraArgs = append(extraArgs, "--settings", settingsTarget)
	}

	// Archive the prompt to history before running.
	promptFile := archivePrompt(opts.HistoryDir, opts.PromptName, prompt)

	// Build the agent request
	req := agent.Request{
		Prompt:     prompt,
		WorkDir:    cwd,
		StreamFile: streamFile,
		StderrFile: stderrFile,
		ExtraArgs:  extraArgs,
	}

	agentName := r.Agent.Name()
	cliStr := agentName + " " + strings.Join(extraArgs, " ")

	// Write exec file with full context for debugging.
	writeExecFile(execTarget, startedAt, opts, prompt, settingsJSON, cliStr, cwd, agentName)

	appendLog(r.LogFile, opts.RoleID, "start", cwd, cliStr,
		relToDir(r.ProjectDir, promptFile),
		relToDir(r.ProjectDir, opts.LastMessageFilePath))

	// Run the agent and consume events
	events := r.Agent.Run(ctx, req)

	var (
		toolCounts = make(map[string]int)
		eventCount int
		totalTools int
		lastOutput string
		resultEv   *agent.StreamEvent
	)

	emitProgress := func(phase, toolName, toolInput, content string, toolCount, evCount int) {
		sendProgress(progress, RunProgress{
			RoleID:         opts.RoleID,
			Phase:          phase,
			ToolName:       toolName,
			ToolInput:      toolInput,
			Content:        content,
			ToolCount:      toolCount,
			EventCount:     evCount,
			StartedAt:      startedAt,
			Elapsed:        time.Since(startedAt),
			StreamFilePath: streamFile,
			StderrFilePath: stderrFile,
		})
	}

	for ev := range events {
		eventCount++

		switch ev.Type {
		case "system":
			emitProgress(PhaseInit, "", "", "", 0, eventCount)

		case "assistant":
			if ev.Text != "" {
				lastOutput = ev.Text
				emitProgress(PhaseThinking, "", "", truncate(ev.Text, 200), totalTools, eventCount)
			}

		case "tool_use":
			toolCounts[ev.ToolName]++
			totalTools++
			emitProgress(PhaseTool, ev.ToolName, truncate(ev.ToolInput, 200), "", totalTools, eventCount)

		case "tool_result":
			emitProgress(PhaseToolResult, "", "", truncate(ev.ToolResult, 200), totalTools, eventCount)

		case "result":
			evCopy := ev
			resultEv = &evCopy

		case "error":
			evCopy := ev
			resultEv = &evCopy
		}
	}

	endedAt := time.Now()
	duration := endedAt.Sub(startedAt)

	output := lastOutput
	if resultEv != nil && resultEv.Output != "" {
		output = resultEv.Output
	}

	summary := RunSummary{
		RoleID:         opts.RoleID,
		StartedAt:      startedAt,
		EndedAt:        endedAt,
		Duration:       duration,
		Output:         output,
		ToolCounts:     toolCounts,
		StreamFilePath: streamFile,
		StderrFilePath: stderrFile,
	}

	if resultEv != nil {
		summary.ExitCode = resultEv.ExitCode
		summary.Cost = resultEv.Cost
		summary.DurationMS = resultEv.DurationMS
		summary.Turns = resultEv.Turns
		summary.IsError = resultEv.IsError
		summary.InputTokens = resultEv.InputTokens
		summary.OutputTokens = resultEv.OutputTokens
		summary.CacheReadTokens = resultEv.CacheReadTokens
	}

	success := resultEv != nil && resultEv.Type == "result" && resultEv.ExitCode == 0 && !resultEv.IsError

	if success {
		if opts.LastMessageFilePath != "" && output != "" {
			dir := filepath.Dir(opts.LastMessageFilePath)
			_ = os.MkdirAll(dir, 0755)
			_ = os.WriteFile(opts.LastMessageFilePath, []byte(output), 0644)
		}
		appendLog(r.LogFile, opts.RoleID, "ok", cwd, cliStr)
		emitProgress(PhaseDone, "", "", "", totalTools, eventCount)
	} else {
		switch {
		case ctx.Err() == context.DeadlineExceeded:
			summary.Err = fmt.Errorf("timed out after %d minutes", opts.TimeoutMin)
		case resultEv != nil && resultEv.Err != nil:
			summary.Err = fmt.Errorf("agent exited with error: %w", resultEv.Err)
		case resultEv != nil && resultEv.IsError:
			summary.Err = fmt.Errorf("agent reported error (is_error=true)")
		default:
			summary.Err = fmt.Errorf("agent produced no result event")
		}
		writeErrorFile(opts.ErrorMessageFilePath, summary, "")
		appendLog(r.LogFile, opts.RoleID, "error", cwd, cliStr, summary.Err.Error())
		emitProgress(PhaseError, "", "", "", totalTools, eventCount)
	}

	return summary
}

// agentNeedsSettings returns true if the agent is claude-based and needs settings.
func (r *Runner) agentNeedsSettings() bool {
	return r.Agent.Name() == "claude"
}

// writeSettings resolves the sandbox settings via 3-level fallback
// (.ateam/ -> .ateamorg/ -> .ateamorg/defaults/), merges in runtime paths,
// and writes the settings to settingsPath.
func (r *Runner) writeSettings(settingsPath string, opts RunOpts) ([]byte, error) {
	const sandboxFile = "ateam_claude_sandbox_extra_settings.json"

	base := readFileOr3Level(
		filepath.Join(r.ProjectDir, sandboxFile),
		filepath.Join(r.OrgDir, sandboxFile),
		filepath.Join(r.OrgDir, "defaults", sandboxFile),
	)
	if base == "" {
		return nil, fmt.Errorf("no %s found in project, org, or defaults", sandboxFile)
	}

	var settings map[string]any
	if err := json.Unmarshal([]byte(base), &settings); err != nil {
		return nil, fmt.Errorf("cannot parse %s: %w", sandboxFile, err)
	}

	workDir := effectiveWorkDir(opts)

	runtimeWriteDirs := []string{workDir, r.ProjectDir}
	runtimeWriteDirs = append(runtimeWriteDirs, r.ExtraWriteDirs...)
	runtimeAdditionalDirs := append([]string{r.ProjectDir}, r.ExtraWriteDirs...)

	mergeStringList(settings, []string{"sandbox", "filesystem", "allowWrite"}, runtimeWriteDirs)
	mergeStringList(settings, []string{"sandbox", "filesystem", "denyWrite"}, []string{
		settingsPath,
		filepath.Join(r.ProjectDir, sandboxFile),
		filepath.Join(r.OrgDir, sandboxFile),
		filepath.Join(r.OrgDir, "defaults", sandboxFile),
	})
	mergeStringList(settings, []string{"permissions", "additionalDirectories"}, runtimeAdditionalDirs)

	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, err
	}

	if err := os.WriteFile(settingsPath, data, 0644); err != nil {
		return nil, err
	}
	return data, nil
}

func readFileOr3Level(paths ...string) string {
	for _, p := range paths {
		if data, err := os.ReadFile(p); err == nil {
			return string(data)
		}
	}
	return ""
}

func mergeStringList(obj map[string]any, keyPath []string, values []string) {
	m := obj
	for _, key := range keyPath[:len(keyPath)-1] {
		child, ok := m[key].(map[string]any)
		if !ok {
			child = make(map[string]any)
			m[key] = child
		}
		m = child
	}
	lastKey := keyPath[len(keyPath)-1]
	var existing []any
	if arr, ok := m[lastKey].([]any); ok {
		existing = arr
	}
	for _, v := range values {
		existing = append(existing, v)
	}
	m[lastKey] = existing
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func sendProgress(ch chan<- RunProgress, p RunProgress) {
	if ch == nil {
		return
	}
	select {
	case ch <- p:
	default:
	}
}

func writeErrorFile(path string, s RunSummary, stderr string) {
	if path == "" {
		return
	}
	dir := filepath.Dir(path)
	_ = os.MkdirAll(dir, 0755)

	var b strings.Builder
	fmt.Fprintf(&b, "# Error: %s\n\n", s.RoleID)
	fmt.Fprintf(&b, "**Error:** %v\n\n", s.Err)
	fmt.Fprintf(&b, "**Exit Code:** %d\n\n", s.ExitCode)
	fmt.Fprintf(&b, "**Duration:** %s\n\n", FormatDuration(s.Duration))

	if stderr != "" {
		fmt.Fprintf(&b, "## Stderr\n\n```\n%s\n```\n\n", stderr)
	}
	if s.Cost > 0 || s.InputTokens > 0 {
		fmt.Fprintf(&b, "## Usage\n\n")
		fmt.Fprintf(&b, "- Cost: $%.4f\n", s.Cost)
		fmt.Fprintf(&b, "- Turns: %d\n", s.Turns)
		fmt.Fprintf(&b, "- Input tokens: %d\n", s.InputTokens)
		fmt.Fprintf(&b, "- Output tokens: %d\n", s.OutputTokens)
		fmt.Fprintf(&b, "- Cache read tokens: %d\n\n", s.CacheReadTokens)
	}
	if s.Output != "" {
		fmt.Fprintf(&b, "## Partial Output\n\n%s\n", s.Output)
	}

	_ = os.WriteFile(path, []byte(b.String()), 0644)
}

func appendLog(logFile, roleID, status, cwd, cli string, extra ...string) {
	if logFile == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(logFile), 0755)
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	ts := time.Now().Format(TimestampFormat)
	fields := []string{ts, roleID, status, cwd, cli}
	fields = append(fields, extra...)
	fmt.Fprintln(f, strings.Join(fields, " | "))
}

func effectiveWorkDir(opts RunOpts) string {
	if opts.WorkDir != "" {
		return opts.WorkDir
	}
	cwd, _ := os.Getwd()
	return cwd
}

func relToDir(base, path string) string {
	if base == "" || path == "" {
		return path
	}
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return path
	}
	return rel
}

func archivePrompt(historyDir, promptName, prompt string) string {
	if historyDir == "" || promptName == "" {
		return ""
	}
	_ = os.MkdirAll(historyDir, 0755)
	ts := time.Now().Format(TimestampFormat)
	name := strings.ReplaceAll(fmt.Sprintf("%s.%s", ts, promptName), " ", "_")
	path := filepath.Join(historyDir, name)
	_ = os.WriteFile(path, []byte(prompt), 0644)
	return path
}

// FormatDuration returns a human-readable duration string.
func FormatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.0fs", d.Seconds())
	}
	minutes := int(d.Minutes())
	seconds := int(d.Seconds()) % 60
	if seconds == 0 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%dm%ds", minutes, seconds)
}

// ArchiveFile copies a file to archiveDir with a timestamped name.
func ArchiveFile(srcPath, archiveDir, name string) error {
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		return err
	}
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}
	timestamp := time.Now().Format(TimestampFormat)
	archiveName := strings.ReplaceAll(fmt.Sprintf("%s.%s", timestamp, name), " ", "_")
	return os.WriteFile(filepath.Join(archiveDir, archiveName), data, 0644)
}

func writeExecFile(path string, startedAt time.Time, opts RunOpts, prompt string, settingsJSON []byte, cliStr, cwd, agentName string) {
	var b strings.Builder

	fmt.Fprintf(&b, "# Command\n")
	fmt.Fprintf(&b, "* started: %s\n", startedAt.Format(TimestampFormat))
	fmt.Fprintf(&b, "* agent: %s\n", agentName)
	fmt.Fprintf(&b, "* action: %s\n", opts.Action)
	fmt.Fprintf(&b, "* role: %s\n", opts.RoleID)
	fmt.Fprintf(&b, "* cwd: %s\n", cwd)
	fmt.Fprintf(&b, "* coding agent cli:\n  ```bash\n  %s\n  ```\n", cliStr)

	fmt.Fprintf(&b, "\n# Env\n")
	fmt.Fprintf(&b, "## Inherited\n")
	env := os.Environ()
	sort.Strings(env)
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		if looksLikeSecret(k) {
			fmt.Fprintf(&b, "%s=<redacted:%d>\n", k, len(v))
		} else {
			fmt.Fprintf(&b, "%s\n", e)
		}
	}
	fmt.Fprintf(&b, "\n## Specified\n")
	fmt.Fprintf(&b, "unsets CLAUDECODE\n")

	if len(settingsJSON) > 0 {
		fmt.Fprintf(&b, "\n# Settings\n```json\n%s\n```\n", string(settingsJSON))
	}

	fmt.Fprintf(&b, "\n# Prompt\n%s\n", prompt)

	_ = os.WriteFile(path, []byte(b.String()), 0644)
}

func looksLikeSecret(name string) bool {
	up := strings.ToUpper(name)
	for _, substr := range []string{
		"KEY", "SECRET", "TOKEN", "PASSWORD", "PASSWD",
		"CREDENTIAL", "AUTH", "PRIVATE",
	} {
		if strings.Contains(up, substr) {
			return true
		}
	}
	return false
}
