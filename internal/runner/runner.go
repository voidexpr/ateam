package runner

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/container"
)

const (
	TimestampFormat = "2006-01-02_15-04-05"

	ActionReport = "report"
	ActionRun    = "run"
	ActionCode   = "code"
	ActionReview = "review"
	ActionDebug  = "debug"
)

// Runner orchestrates agent execution with logging, file I/O, and progress reporting.
type Runner struct {
	Agent                   agent.Agent
	Container               container.Container // nil or NoneContainer means run on host
	LogFile                 string              // append-only runner log
	ProjectDir              string              // .ateam/ dir
	OrgDir                  string              // .ateamorg/ dir
	SourceDir               string              // project root (parent of .ateam/)
	ProjectName             string              // from config.toml
	ExtraWriteDirs          []string            // additional dirs granted sandbox write access
	ExtraArgs               []string            // extra args passed to the agent
	SandboxSettings         string              // inline JSON settings template (from runtime.hcl)
	SandboxRWPaths          []string            // from agent config rw_paths
	SandboxROPaths          []string            // from agent config ro_paths
	SandboxDenied           []string            // from agent config denied_paths
	SandboxExtraWrite       []string            // from config.toml [sandbox-extra]
	SandboxExtraRead        []string            // from config.toml [sandbox-extra]
	SandboxExtraDomains     []string            // from config.toml [sandbox-extra]
	SandboxExtraExcludedCmd []string            // from config.toml [sandbox-extra]
	ConfigDir               string              // CLAUDE_CONFIG_DIR; relative resolves from ProjectDir
	CallDB                  *calldb.CallDB      // nil = no DB tracking
	Profile                 string              // profile name for DB
	ContainerType           string              // "none" or "docker" for DB
	ContainerName           string              // docker container name for liveness checks
	ProjectID               string              // project ID for DB
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
	Verbose              bool   // print agent and docker commands to stderr
	TaskGroup            string // groups related calls (e.g. all tasks in one ateam code run)
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
	RoleID    string
	StartedAt time.Time
	EndedAt   time.Time
	Duration  time.Duration
	ExitCode  int
	Err       error

	Output           string
	Cost             float64
	DurationMS       int64
	Turns            int
	IsError          bool
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
	ToolCounts       map[string]int

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

	if err := os.MkdirAll(opts.LogsDir, 0700); err != nil {
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

	// Write sandbox settings if configured
	var settingsJSON []byte
	if r.SandboxSettings != "" {
		settingsTarget := prefix + "_settings.json"
		var err error
		settingsJSON, err = r.writeSettings(settingsTarget, opts)
		if err != nil {
			return failEarly(fmt.Errorf("cannot create settings file: %w", err))
		}
		extraArgs = append(extraArgs, "--settings", settingsTarget)
	}

	// Insert call tracking record early so EXEC_ID is available for templates.
	agentName := r.Agent.Name()
	model := agent.NormalizeModel(extractModel(r.Agent))
	var callID int64
	if r.CallDB != nil {
		relStream := streamFile
		if r.ProjectDir != "" {
			if rel, err := filepath.Rel(r.ProjectDir, streamFile); err == nil {
				relStream = rel
			}
		}
		if id, err := r.CallDB.InsertCall(&calldb.Call{
			ProjectID:  r.ProjectID,
			Profile:    r.Profile,
			Agent:      agentName,
			Container:  r.ContainerType,
			Action:     opts.Action,
			Role:       opts.RoleID,
			TaskGroup:  opts.TaskGroup,
			Model:      model,
			PromptHash: hashPrompt(prompt),
			StartedAt:  startedAt,
			StreamFile: relStream,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: call tracking insert failed: %v\n", err)
		} else {
			callID = id
		}
	}

	// Resolve {{VAR}} templates in agent args and extra args.
	tmplVars := BuildTemplateVars(r, opts, startedAt, callID, agentName, model)
	extraArgs = ResolveTemplateArgs(extraArgs, tmplVars)
	resolveAgentTemplateArgs(r.Agent, tmplVars)

	// Archive the prompt to history before running.
	promptFile := archivePrompt(opts.HistoryDir, opts.PromptName, prompt, startedAt)

	// Resolve CLAUDE_CONFIG_DIR for isolated agents.
	// Relative config_dir is resolved from ProjectDir (.ateam/); absolute is used as-is.
	var reqEnv map[string]string
	if r.ConfigDir != "" {
		var configPath string
		if filepath.IsAbs(r.ConfigDir) {
			configPath = r.ConfigDir
		} else {
			if r.ProjectDir == "" {
				return failEarly(fmt.Errorf("relative config_dir requires project context (no .ateam/ found)"))
			}
			configPath = filepath.Join(r.ProjectDir, r.ConfigDir)
		}
		reqEnv = map[string]string{
			"CLAUDE_CONFIG_DIR": configPath,
		}
	}

	// Build the agent request
	req := agent.Request{
		Prompt:     prompt,
		WorkDir:    cwd,
		StreamFile: streamFile,
		StderrFile: stderrFile,
		ExtraArgs:  extraArgs,
		Env:        reqEnv,
	}

	// If running inside a Docker container, set up CmdFactory and translate paths.
	if dc, ok := r.Container.(*container.DockerContainer); ok {
		if err := dc.EnsureImage(ctx); err != nil {
			return failEarly(fmt.Errorf("docker image build failed: %w", err))
		}
		if err := dc.EnsureRunning(ctx); err != nil {
			return failEarly(fmt.Errorf("docker container start failed: %w", err))
		}
		if dc.PrecheckScript != "" {
			if dc.Persistent {
				if err := dc.RunPrecheck(ctx); err != nil {
					return failEarly(fmt.Errorf("precheck failed: %w", err))
				}
			} else {
				fmt.Fprintf(os.Stderr, "[docker] warning: precheck ignored in oneshot mode — use Dockerfile ENTRYPOINT instead\n")
			}
		}
		req.CmdFactory = dc.CmdFactory()
		// Note: StreamFile and StderrFile are NOT translated — they are
		// opened by the host process (os.Create) to capture piped output,
		// not accessed inside the container.
		// Translate any host paths in extraArgs (e.g. --settings path)
		for i, a := range req.ExtraArgs {
			if a == "--settings" && i+1 < len(req.ExtraArgs) {
				req.ExtraArgs[i+1] = dc.TranslatePath(req.ExtraArgs[i+1])
			}
		}
		req.WorkDir = dc.TranslatePath(cwd)
	}

	// If running inside a devcontainer, start it and validate the agent.
	if dc, ok := r.Container.(*container.DevcontainerContainer); ok {
		if err := dc.EnsureRunning(ctx); err != nil {
			return failEarly(fmt.Errorf("devcontainer start failed: %w", err))
		}
		agentCmd, _ := r.Agent.DebugCommandArgs(nil)
		if err := dc.ValidateAgent(ctx, agentCmd); err != nil {
			return failEarly(err)
		}
		req.CmdFactory = dc.CmdFactory()
	}

	// If running inside a Docker sandbox, create it and validate the agent.
	if ds, ok := r.Container.(*container.DockerSandboxContainer); ok {
		if err := ds.EnsureRunning(ctx); err != nil {
			return failEarly(fmt.Errorf("docker sandbox start failed: %w", err))
		}
		agentCmd, _ := r.Agent.DebugCommandArgs(nil)
		if err := ds.ValidateAgent(ctx, agentCmd); err != nil {
			return failEarly(err)
		}
		req.CmdFactory = ds.CmdFactory()
	}

	command, agentArgs := r.Agent.DebugCommandArgs(extraArgs)
	cliStr := command + " " + strings.Join(agentArgs, " ")

	if opts.Verbose {
		fmt.Fprintf(os.Stderr, "[verbose] agent: %s\n", cliStr)
		if dc, ok := r.Container.(*container.DockerContainer); ok {
			fmt.Fprintf(os.Stderr, "[verbose] docker: %s\n",
				dc.DebugCommand(container.RunOpts{Command: command, Args: agentArgs}))
		}
		if dc, ok := r.Container.(*container.DevcontainerContainer); ok {
			fmt.Fprintf(os.Stderr, "[verbose] devcontainer: %s\n",
				dc.DebugCommand(container.RunOpts{Command: command, Args: agentArgs}))
		}
		if ds, ok := r.Container.(*container.DockerSandboxContainer); ok {
			fmt.Fprintf(os.Stderr, "[verbose] docker-sandbox: %s\n",
				ds.DebugCommand(container.RunOpts{Command: command, Args: agentArgs}))
		}
	}

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
			if ev.PID > 0 && r.CallDB != nil && callID > 0 {
				if err := r.CallDB.SetPID(callID, ev.PID, r.ContainerName); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: call tracking SetPID failed: %v\n", err)
				}
			}
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
		if summary.DurationMS == 0 {
			summary.DurationMS = duration.Milliseconds()
		}
		summary.Turns = resultEv.Turns
		summary.IsError = resultEv.IsError
		summary.InputTokens = resultEv.InputTokens
		summary.OutputTokens = resultEv.OutputTokens
		summary.CacheReadTokens = resultEv.CacheReadTokens
		summary.CacheWriteTokens = resultEv.CacheWriteTokens
	} else if streamFile != "" {
		// No result event received (timeout, crash). Try to extract
		// cost/usage from the stream file which may have been written
		// before the process was killed.
		if res := scanStreamFileForResult(streamFile); res != nil {
			summary.Cost = res.Cost
			summary.DurationMS = res.DurationMS
			summary.Turns = res.Turns
			summary.InputTokens = res.InputTokens
			summary.OutputTokens = res.OutputTokens
			summary.CacheReadTokens = res.CacheReadTokens
			summary.CacheWriteTokens = res.CacheWriteTokens
		}
	}

	success := resultEv != nil && resultEv.Type == "result" && resultEv.ExitCode == 0 && !resultEv.IsError

	if success {
		if opts.LastMessageFilePath != "" && output != "" {
			dir := filepath.Dir(opts.LastMessageFilePath)
			_ = os.MkdirAll(dir, 0700)
			_ = os.WriteFile(opts.LastMessageFilePath, []byte(output), 0600)
		}
		appendLog(r.LogFile, opts.RoleID, "ok", cwd, cliStr)
		emitProgress(PhaseDone, "", "", "", totalTools, eventCount)
	} else {
		summary.IsError = true
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

	// Update call tracking record with results.
	if r.CallDB != nil && callID > 0 {
		errMsg := ""
		if summary.Err != nil {
			errMsg = summary.Err.Error()
		}
		// If the result event reported a model (e.g. Codex discovering the
		// model at runtime), propagate it so the DB row is accurate.
		resultModel := ""
		if resultEv != nil && resultEv.Model != "" {
			resultModel = agent.NormalizeModel(resultEv.Model)
		}
		if err := r.CallDB.UpdateCall(callID, &calldb.CallResult{
			EndedAt:          summary.EndedAt,
			DurationMS:       summary.DurationMS,
			ExitCode:         summary.ExitCode,
			IsError:          summary.IsError,
			ErrorMessage:     errMsg,
			CostUSD:          summary.Cost,
			InputTokens:      summary.InputTokens,
			OutputTokens:     summary.OutputTokens,
			CacheReadTokens:  summary.CacheReadTokens,
			CacheWriteTokens: summary.CacheWriteTokens,
			Turns:            summary.Turns,
			Model:            resultModel,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: call tracking update failed: %v\n", err)
		}
	}

	return summary
}

// RenderSettings generates the merged sandbox settings JSON without writing to disk.
// workDir is the effective working directory (e.g. SourceDir).
func (r *Runner) RenderSettings(workDir string) ([]byte, error) {
	if r.SandboxSettings == "" {
		return nil, nil
	}
	var settings map[string]any
	if err := json.Unmarshal([]byte(r.SandboxSettings), &settings); err != nil {
		return nil, fmt.Errorf("cannot parse sandbox settings: %w", err)
	}

	r.mergeSandboxPaths(settings, workDir, nil)
	return json.MarshalIndent(settings, "", "  ")
}

// writeSettings parses the inline sandbox settings JSON from the agent config,
// merges in runtime paths, and writes the result to settingsPath.
func (r *Runner) writeSettings(settingsPath string, opts RunOpts) ([]byte, error) {
	var settings map[string]any
	if err := json.Unmarshal([]byte(r.SandboxSettings), &settings); err != nil {
		return nil, fmt.Errorf("cannot parse sandbox settings: %w", err)
	}

	r.mergeSandboxPaths(settings, effectiveWorkDir(opts), []string{settingsPath})

	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, err
	}

	if err := os.WriteFile(settingsPath, data, 0600); err != nil {
		return nil, err
	}
	return data, nil
}

// mergeSandboxPaths merges runtime-discovered paths into the parsed settings JSON.
// extraDenyWrite contains paths to deny (e.g. the settings file itself).
func (r *Runner) mergeSandboxPaths(settings map[string]any, workDir string, extraDenyWrite []string) {
	runtimeWriteDirs := []string{workDir}
	runtimeWriteDirs = append(runtimeWriteDirs, r.ExtraWriteDirs...)
	runtimeWriteDirs = append(runtimeWriteDirs, r.SandboxRWPaths...)
	runtimeWriteDirs = append(runtimeWriteDirs, r.SandboxExtraWrite...)

	var runtimeReadDirs []string
	if r.OrgDir != "" {
		runtimeReadDirs = append(runtimeReadDirs, r.OrgDir)
	}
	runtimeReadDirs = append(runtimeReadDirs, r.SandboxExtraRead...)

	// additionalDirectories: project root, .ateamorg/, agent ro_paths
	runtimeAdditionalDirs := []string{workDir}
	if r.OrgDir != "" {
		runtimeAdditionalDirs = append(runtimeAdditionalDirs, r.OrgDir)
	}
	runtimeAdditionalDirs = append(runtimeAdditionalDirs, r.SandboxROPaths...)

	mergeStringList(settings, []string{"sandbox", "filesystem", "allowWrite"}, runtimeWriteDirs)
	mergeStringList(settings, []string{"sandbox", "filesystem", "allowRead"}, runtimeReadDirs)
	denyPaths := append([]string{}, extraDenyWrite...)
	denyPaths = append(denyPaths, r.SandboxDenied...)
	if len(denyPaths) > 0 {
		mergeStringList(settings, []string{"sandbox", "filesystem", "denyWrite"}, denyPaths)
	}
	mergeStringList(settings, []string{"permissions", "additionalDirectories"}, runtimeAdditionalDirs)
	if len(r.SandboxExtraDomains) > 0 {
		mergeStringList(settings, []string{"sandbox", "network", "allowedDomains"}, r.SandboxExtraDomains)
	}
	if len(r.SandboxExtraExcludedCmd) > 0 {
		mergeStringList(settings, []string{"sandbox", "excludedCommands"}, r.SandboxExtraExcludedCmd)
	}
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
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	cut := 0
	for i, r := range s {
		end := i + utf8.RuneLen(r)
		if end > max {
			break
		}
		cut = end
	}
	if cut == 0 {
		return "…"
	}
	return s[:cut] + "…"
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
	_ = os.MkdirAll(dir, 0700)

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
		fmt.Fprintf(&b, "- Cache read tokens: %d\n", s.CacheReadTokens)
		fmt.Fprintf(&b, "- Cache write tokens: %d\n\n", s.CacheWriteTokens)
	}
	if s.Output != "" {
		fmt.Fprintf(&b, "## Partial Output\n\n%s\n", s.Output)
	}

	_ = os.WriteFile(path, []byte(b.String()), 0600)
}

func appendLog(logFile, roleID, status, cwd, cli string, extra ...string) {
	if logFile == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(logFile), 0700)
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
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

func archivePrompt(historyDir, promptName, prompt string, startedAt time.Time) string {
	if historyDir == "" || promptName == "" {
		return ""
	}
	_ = os.MkdirAll(historyDir, 0700)
	ts := startedAt.Format(TimestampFormat)
	name := strings.ReplaceAll(fmt.Sprintf("%s.%s", ts, promptName), " ", "_")
	path := filepath.Join(historyDir, name)
	_ = os.WriteFile(path, []byte(prompt), 0600)
	return path
}

// FormatDuration returns a human-readable duration string.
func FormatDuration(d time.Duration) string {
	rounded := d.Round(time.Second)
	if rounded < time.Minute {
		return fmt.Sprintf("%ds", int(rounded/time.Second))
	}
	minutes := int(rounded / time.Minute)
	seconds := int((rounded % time.Minute) / time.Second)
	if seconds == 0 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%dm%ds", minutes, seconds)
}

// ArchiveFile copies a file to archiveDir with a timestamped name.
// The ts parameter sets the timestamp prefix; pass the run's startedAt so all
// files for a run share the same timestamp, making association deterministic.
func ArchiveFile(srcPath, archiveDir, name string, ts time.Time) error {
	if err := os.MkdirAll(archiveDir, 0700); err != nil {
		return err
	}
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}
	archiveName := strings.ReplaceAll(fmt.Sprintf("%s.%s", ts.Format(TimestampFormat), name), " ", "_")
	return os.WriteFile(filepath.Join(archiveDir, archiveName), data, 0600)
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

	_ = os.WriteFile(path, []byte(b.String()), 0600)
}

func extractModel(a agent.Agent) string {
	if mp, ok := a.(agent.ModelProvider); ok {
		return mp.ModelName()
	}
	return ""
}

func hashPrompt(prompt string) string {
	h := sha256.Sum256([]byte(prompt))
	return fmt.Sprintf("%x", h[:8])
}

func looksLikeSecret(name string) bool {
	up := strings.ToUpper(name)
	for _, substr := range []string{
		"KEY", "SECRET", "TOKEN", "PASSWORD", "PASSWD",
		"CREDENTIAL", "AUTH", "PRIVATE",
		"URL", "URI", "DSN", "CONN",
	} {
		if strings.Contains(up, substr) {
			return true
		}
	}
	return false
}
