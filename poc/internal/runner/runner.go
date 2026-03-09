package runner

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ClaudeRunner holds shared execution config for invoking claude.
type ClaudeRunner struct {
	ExtraArgs  []string
	LogFile    string // append-only runner log (e.g. .ateam/logs/runner.log)
	ProjectDir string // .ateam/ dir, used for computing relative paths in logs
}

// RunOpts holds per-invocation settings.
type RunOpts struct {
	AgentID              string
	OutputDir            string // where to write stream.jsonl, stderr.log
	LastMessageFilePath  string // where to write extracted report text (on success only)
	ErrorMessageFilePath string // where to write error info (on failure only)
	WorkDir              string // cwd for the subprocess
	TimeoutMin           int
	HistoryDir           string // where to archive the prompt (e.g. agents/<name>/history)
	PromptName           string // archive name (e.g. "report_prompt.md", "review_prompt.md")
}

// RunProgress is a lightweight status sent on a channel during execution.
type RunProgress struct {
	AgentID        string
	Phase          string // PhaseInit, PhaseThinking, PhaseTool, PhaseToolResult, PhaseDone, PhaseError
	ToolName       string // set when Phase == PhaseTool
	ToolInput      string // tool input snippet (for PhaseTool)
	Content        string // text content (for PhaseThinking) or tool result (for PhaseToolResult)
	ToolCount      int
	EventCount     int
	Elapsed        time.Duration
	StartedAt      time.Time
	StreamFilePath string
	StderrFilePath string
}

// RunSummary is the final result returned by Run.
type RunSummary struct {
	AgentID         string
	StartedAt       time.Time
	EndedAt         time.Time
	Duration        time.Duration
	ExitCode        int
	Err             error

	Output          string // extracted report text
	Cost            float64
	DurationMS      int64 // claude's own measurement
	Turns           int
	IsError         bool
	InputTokens     int
	OutputTokens    int
	CacheReadTokens int
	ToolCounts      map[string]int

	StreamFilePath string
	StderrFilePath string
}

// Run executes claude with stream-json output, parsing events in real time.
// If progress is non-nil, lightweight status updates are sent on it.
func (r *ClaudeRunner) Run(ctx context.Context, prompt string, opts RunOpts, progress chan<- RunProgress) RunSummary {
	startedAt := time.Now()

	streamFile := filepath.Join(opts.OutputDir, "last_run_stream.jsonl")
	stderrFile := filepath.Join(opts.OutputDir, "last_run_stderr.log")

	failEarly := func(err error) RunSummary {
		s := RunSummary{
			AgentID:        opts.AgentID,
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

	if err := os.MkdirAll(opts.OutputDir, 0755); err != nil {
		return failEarly(fmt.Errorf("cannot create output directory: %w", err))
	}

	sf, err := os.Create(streamFile)
	if err != nil {
		return failEarly(fmt.Errorf("cannot create stream file: %w", err))
	}
	defer sf.Close()

	ef, err := os.Create(stderrFile)
	if err != nil {
		return failEarly(fmt.Errorf("cannot create stderr file: %w", err))
	}
	defer ef.Close()

	if opts.TimeoutMin > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(opts.TimeoutMin)*time.Minute)
		defer cancel()
	}

	args := []string{"-p", "--output-format", "stream-json", "--verbose"}
	args = append(args, r.ExtraArgs...)
	cliStr := "claude " + strings.Join(args, " ")

	// Resolve cwd for logging.
	cwd := opts.WorkDir
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	// Archive the prompt to history before running.
	promptFile := archivePrompt(opts.HistoryDir, opts.PromptName, prompt)

	cmd := exec.CommandContext(ctx, "claude", args...)
	if opts.WorkDir != "" {
		cmd.Dir = opts.WorkDir
	}
	cmd.Stdin = strings.NewReader(prompt)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return failEarly(fmt.Errorf("cannot create stdout pipe: %w", err))
	}

	var stderrBuf bytes.Buffer
	cmd.Stderr = io.MultiWriter(ef, &stderrBuf)

	appendLog(r.LogFile, opts.AgentID, "start", cwd, cliStr,
		relToDir(r.ProjectDir, promptFile),
		relToDir(r.ProjectDir, opts.LastMessageFilePath))

	if err := cmd.Start(); err != nil {
		appendLog(r.LogFile, opts.AgentID, "error", cwd, cliStr, err.Error())
		return failEarly(fmt.Errorf("cannot start claude: %w", err))
	}

	emitProgress := func(phase, toolName, toolInput, content string, toolCount, eventCount int) {
		sendProgress(progress, RunProgress{
			AgentID:        opts.AgentID,
			Phase:          phase,
			ToolName:       toolName,
			ToolInput:      toolInput,
			Content:        content,
			ToolCount:      toolCount,
			EventCount:     eventCount,
			StartedAt:      startedAt,
			Elapsed:        time.Since(startedAt),
			StreamFilePath: streamFile,
			StderrFilePath: stderrFile,
		})
	}

	// Parse stdout stream
	var (
		lastAssistant *assistantEvent
		result        *resultEvent
		toolCounts    = make(map[string]int)
		eventCount    int
		totalTools    int
	)

	streamWriter := bufio.NewWriter(sf)
	defer streamWriter.Flush()

	scanner := bufio.NewScanner(stdoutPipe)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()

		streamWriter.Write(line)
		streamWriter.WriteByte('\n')

		typ, ev, parseErr := parseStreamLine(line)
		if parseErr != nil || ev == nil {
			continue
		}
		eventCount++

		switch typ {
		case "system":
			emitProgress(PhaseInit, "", "", "", 0, eventCount)

		case "assistant":
			ast := ev.(*assistantEvent)
			lastAssistant = ast

			hasToolUse := false
			for _, block := range ast.Message.Content {
				if block.Type == "tool_use" {
					toolCounts[block.Name]++
					totalTools++
					hasToolUse = true
					emitProgress(PhaseTool, block.Name, truncate(string(block.Input), 200), "", totalTools, eventCount)
				}
			}
			if !hasToolUse {
				text := extractReportText(ast)
				emitProgress(PhaseThinking, "", "", truncate(text, 200), totalTools, eventCount)
			}

		case "tool_result":
			tr := ev.(*toolResultEvent)
			emitProgress(PhaseToolResult, "", "", truncate(tr.Content, 200), totalTools, eventCount)

		case "result":
			result = ev.(*resultEvent)
		}
	}

	cmdErr := cmd.Wait()
	endedAt := time.Now()
	duration := endedAt.Sub(startedAt)
	stderr := strings.TrimSpace(stderrBuf.String())

	exitCode := 0
	if cmdErr != nil {
		if exitErr, ok := cmdErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	output := extractReportText(lastAssistant)

	summary := RunSummary{
		AgentID:        opts.AgentID,
		StartedAt:      startedAt,
		EndedAt:        endedAt,
		Duration:       duration,
		ExitCode:       exitCode,
		Output:         output,
		ToolCounts:     toolCounts,
		StreamFilePath: streamFile,
		StderrFilePath: stderrFile,
	}

	if result != nil {
		summary.Cost = result.TotalCostUSD
		summary.DurationMS = result.DurationMS
		summary.Turns = result.NumTurns
		summary.IsError = result.IsError
		summary.InputTokens = result.Usage.InputTokens
		summary.OutputTokens = result.Usage.OutputTokens
		summary.CacheReadTokens = result.Usage.CacheReadInputTokens
	}

	success := result != nil && exitCode == 0 && !result.IsError

	if success {
		if opts.LastMessageFilePath != "" && output != "" {
			dir := filepath.Dir(opts.LastMessageFilePath)
			_ = os.MkdirAll(dir, 0755)
			_ = os.WriteFile(opts.LastMessageFilePath, []byte(output), 0644)
		}
		appendLog(r.LogFile, opts.AgentID, "ok", cwd, cliStr)
		emitProgress(PhaseDone, "", "", "", totalTools, eventCount)
	} else {
		switch {
		case ctx.Err() == context.DeadlineExceeded:
			summary.Err = fmt.Errorf("timed out after %d minutes", opts.TimeoutMin)
		case cmdErr != nil:
			summary.Err = fmt.Errorf("claude exited with error: %w", cmdErr)
		case result != nil && result.IsError:
			summary.Err = fmt.Errorf("claude reported error (is_error=true)")
		default:
			summary.Err = fmt.Errorf("claude produced no result event")
		}
		writeErrorFile(opts.ErrorMessageFilePath, summary, stderr)
		appendLog(r.LogFile, opts.AgentID, "error", cwd, cliStr, summary.Err.Error())
		emitProgress(PhaseError, "", "", "", totalTools, eventCount)
	}

	return summary
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
	fmt.Fprintf(&b, "# Error: %s\n\n", s.AgentID)
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

func appendLog(logFile, agentID, status, cwd, cli string, extra ...string) {
	if logFile == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(logFile), 0755)
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	ts := time.Now().Format(time.RFC3339)
	fmt.Fprintf(f, "%s\t%q\t%q\t%q\t%q", ts, agentID, status, cwd, cli)
	for _, e := range extra {
		fmt.Fprintf(f, "\t%q", e)
	}
	f.Write([]byte("\n"))
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

// archivePrompt writes the prompt to historyDir and returns the file path (empty if skipped).
func archivePrompt(historyDir, promptName, prompt string) string {
	if historyDir == "" || promptName == "" {
		return ""
	}
	_ = os.MkdirAll(historyDir, 0755)
	ts := time.Now().Format("2006-01-02_1504")
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

// ArchiveFile copies a file to archiveDir with a timestamped name: "2006-01-02_1504.{name}".
func ArchiveFile(srcPath, archiveDir, name string) error {
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		return err
	}

	data, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}

	timestamp := time.Now().Format("2006-01-02_1504")
	archiveName := strings.ReplaceAll(fmt.Sprintf("%s.%s", timestamp, name), " ", "_")

	return os.WriteFile(filepath.Join(archiveDir, archiveName), data, 0644)
}
