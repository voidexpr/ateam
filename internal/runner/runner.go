// Package runner orchestrates agent task execution, managing scheduling, output collection, and result persistence.
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
	"github.com/ateam/internal/display"
)

// TimestampFormat is kept as an alias for backward compatibility with
// consumers that reference runner.TimestampFormat.
const TimestampFormat = display.TimestampFormat

const (
	ActionReport   = "report"
	ActionRun      = "run"
	ActionParallel = "parallel"
	ActionCode     = "code"
	ActionReview   = "review"
	ActionDebug    = "debug"
)

// IsInContainer detects whether the current process is running inside a container.
// Delegates to container.IsInContainer.
func IsInContainer() bool {
	return container.IsInContainer()
}

// ExpandHome replaces a leading ~/ with the user's home directory.
func ExpandHome(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[2:])
}

// SandboxConfig groups all sandbox-related settings for agent execution.
type SandboxConfig struct {
	Settings         string   // inline JSON settings template (from runtime.hcl)
	RWPaths          []string // from agent config rw_paths
	ROPaths          []string // from agent config ro_paths
	Denied           []string // from agent config denied_paths
	ExtraWrite       []string // from config.toml [sandbox-extra]
	ExtraRead        []string // from config.toml [sandbox-extra]
	ExtraDomains     []string // from config.toml [sandbox-extra]
	ExtraExcludedCmd []string // from config.toml [sandbox-extra]
	ExtraWriteDirs   []string // additional dirs granted sandbox write access
	InsideContainer  bool     // if false, skip sandbox inside containers
}

// ContainerNameSource values for Runner.ContainerNameSource.
const (
	ContainerNameSourceConfig = "config"
	ContainerNameSourceCLI    = "cli"
	ContainerNameSourceSecret = "secret"
	ContainerNameSourceEnv    = "env"
)

// Runner orchestrates agent execution with logging, file I/O, and progress reporting.
//
// Concurrency contract (see CONCURRENCY.md):
//
//   - All Runner fields are WRITTEN only during construction in the main
//     goroutine (cmd/table.go:newRunner and friends, plus applyContainerName
//     and the cmd-layer overrides). After a Runner is handed to RunPool —
//     including PoolTask.Runner overrides — its fields become READ-ONLY.
//   - Agent and Container fields look mutable but are cloned per task at the
//     top of Run via CloneWithResolvedTemplates / Clone. The shared originals
//     are never mutated inside Run.
//   - CallDB is a *sql.DB — safe for concurrent use by stdlib guarantee,
//     further serialized to one writer via SetMaxOpenConns(1).
//   - Sandbox, ExtraArgs, ArgsInsideContainer, ArgsOutsideContainer: slice
//     backing memory is read-only once construction finishes; Run copies
//     r.ExtraArgs into a local before appending.
type Runner struct {
	Agent                agent.Agent
	Container            container.Container // nil means run on host
	LogFile              string              // append-only runner log
	ProjectDir           string              // .ateam/ dir
	OrgDir               string              // .ateamorg/ dir
	SourceDir            string              // project root (parent of .ateam/)
	ProjectName          string              // from config.toml
	ExtraArgs            []string            // extra args passed to the agent
	Sandbox              SandboxConfig       // sandbox filesystem/network restrictions
	ConfigDir            string              // CLAUDE_CONFIG_DIR; relative resolves from ProjectDir
	ArgsInsideContainer  []string            // extra args when inside a container
	ArgsOutsideContainer []string            // extra args when on the host
	CallDB               *calldb.CallDB      // nil = no DB tracking
	Profile              string              // profile name for DB
	ContainerType        string              // "none" or "docker" for DB
	ContainerName        string              // docker container name for liveness checks
	ContainerNameSource  string              // where ContainerName came from (ContainerNameSource* constants)
	ProjectID            string              // project ID for DB

	// StallWarnAfter is the idle duration after which Run logs a stall
	// warning and emits a PhaseStall progress event. Re-armed after each
	// warning. 0 = 5m default. Negative = disable the watchdog.
	StallWarnAfter time.Duration
}

const defaultStallWarn = 5 * time.Minute

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
	ExecID         int64
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
	ContextTokens  int
	ContextWindow  int

	// Cumulative usage so far (populated as assistant events arrive; zero
	// until the first one). EstimatedCost is computed via the agent's
	// pricing table and is 0 when no table is configured.
	CumulativeInputTokens  int
	CumulativeOutputTokens int
	EstimatedCost          float64
}

// RunSummary is the final result returned by Run.
type RunSummary struct {
	ExecID    int64
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

	PeakContextTokens int
	ContextWindow     int

	StreamFilePath string
	StderrFilePath string

	// ErrorSource / ErrorCause classify why the run failed.
	// Set only when the run ends in error. See internal/agent.ErrorSource*.
	ErrorSource string
	ErrorCause  string
}

// LogQueued writes a "queued" entry to the runner log.
func (r *Runner) LogQueued(opts RunOpts) {
	appendLog(r.LogFile, opts.RoleID, "queued", effectiveWorkDir(opts),
		relToDir(r.ProjectDir, opts.LastMessageFilePath))
}

// Run executes the agent with the given prompt and options.
func (r *Runner) Run(ctx context.Context, prompt string, opts RunOpts, progress chan<- RunProgress) RunSummary {
	startedAt := time.Now()
	var callID int64

	prefix := filepath.Join(opts.LogsDir, startedAt.Format(TimestampFormat)+"_"+opts.Action)
	streamFile := prefix + "_stream.jsonl"
	stderrFile := prefix + "_stderr.log"
	execTarget := prefix + "_exec.md"

	failEarly := func(err error) RunSummary {
		s := RunSummary{
			ExecID:         callID,
			RoleID:         opts.RoleID,
			StartedAt:      startedAt,
			EndedAt:        time.Now(),
			Duration:       time.Since(startedAt),
			ExitCode:       -1,
			IsError:        true,
			Err:            err,
			ErrorSource:    agent.ErrorSourceAteamInternal,
			ErrorCause:     err.Error(),
			StreamFilePath: streamFile,
			StderrFilePath: stderrFile,
		}
		writeErrorFile(opts.ErrorMessageFilePath, s, "")
		appendStderrSummary(stderrFile, s)
		return s
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

	// Append environment-aware args.
	// Use container args when: already inside a container, OR launching into one.
	if IsInContainer() || r.Container != nil {
		extraArgs = append(extraArgs, r.ArgsInsideContainer...)
	} else {
		extraArgs = append(extraArgs, r.ArgsOutsideContainer...)
	}

	// Safety warning: --dangerously-skip-permissions outside a container.
	// Skip warning when launching into a container (r.Container != nil) since the
	// flag will only be used inside that container.
	if !IsInContainer() && r.Container == nil {
		for _, a := range extraArgs {
			if a == "--dangerously-skip-permissions" {
				fmt.Fprintf(os.Stderr, "Warning: --dangerously-skip-permissions used outside a Docker container. This skips all safety checks.\n")
				break
			}
		}
	}

	// Ensure logs directory exists before writing settings or stream files.
	if err := os.MkdirAll(opts.LogsDir, 0700); err != nil {
		return failEarly(fmt.Errorf("cannot create logs directory: %w", err))
	}

	// Write sandbox settings if configured.
	// Skip when: already inside a container, OR launching into a container (r.Container != nil),
	// unless sandbox_inside_container is explicitly true.
	skipSandbox := (IsInContainer() || r.Container != nil) && !r.Sandbox.InsideContainer
	var settingsJSON []byte
	if r.Sandbox.Settings != "" && !skipSandbox {
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
	if r.CallDB != nil {
		relStream := streamFile
		relOutput := opts.LastMessageFilePath
		if r.ProjectDir != "" {
			if rel, err := filepath.Rel(r.ProjectDir, streamFile); err == nil {
				relStream = rel
			}
			if relOutput != "" {
				if rel, err := filepath.Rel(r.ProjectDir, relOutput); err == nil {
					relOutput = rel
				}
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
			OutputFile: relOutput,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: call tracking insert failed: %v\n", err)
		} else {
			callID = id
		}
	}

	// Clone the container so per-run template resolution and name refresh
	// never mutate shared state across parallel pool workers.
	var runContainer container.Container
	if r.Container != nil {
		runContainer = r.Container.Clone()
	}
	containerName := r.ContainerName

	// Resolve {{VAR}} templates in agent args, extra args, and container fields.
	tmplVars := BuildTemplateVars(r, opts, startedAt, callID, agentName, model)
	extraArgs = resolveArgs(extraArgs, tmplVars.Replacer())
	runAgent := ResolveAgentTemplateArgs(r.Agent, tmplVars)
	resolveContainerTemplates(runContainer, tmplVars)

	// Build agent request (includes log dir creation and prompt archival).
	req, promptFile, err := r.buildPrompt(prompt, opts, startedAt, tmplVars, cwd, streamFile, stderrFile, extraArgs)
	if err != nil {
		return failEarly(err)
	}

	// Prepare container and translate request paths.
	if name, err := setupContainer(ctx, runContainer, &req, cwd); err != nil {
		return failEarly(err)
	} else if name != "" {
		containerName = name
	}

	command, agentArgs := runAgent.DebugCommandArgs(extraArgs)
	cliStr := command + " " + strings.Join(agentArgs, " ")

	if opts.Verbose {
		fmt.Fprintf(os.Stderr, "[verbose] agent: %s\n", cliStr)
		if runContainer != nil && runContainer.Type() != "none" {
			fmt.Fprintf(os.Stderr, "[verbose] container: %s\n",
				runContainer.DebugCommand(container.RunOpts{Command: command, Args: agentArgs}))
		}
	}

	// Write exec file with full context for debugging.
	writeExecFile(execTarget, startedAt, opts, prompt, settingsJSON, cliStr, cwd, agentName)

	appendLog(r.LogFile, opts.RoleID, "start", cwd, cliStr,
		relToDir(r.ProjectDir, promptFile),
		relToDir(r.ProjectDir, opts.LastMessageFilePath))

	// Run the agent and consume events
	events := runAgent.Run(ctx, req)

	var (
		toolCounts        = make(map[string]int)
		eventCount        int
		totalTools        int
		lastOutput        string
		resultEv          *agent.StreamEvent
		peakContextTokens int
		contextWindow     int
		cumInputTokens    int
		cumOutputTokens   int
		estimatedCost     float64
	)

	emitProgress := func(phase, toolName, toolInput, content string, toolCount, evCount int) {
		sendProgress(progress, RunProgress{
			ExecID:                 callID,
			RoleID:                 opts.RoleID,
			Phase:                  phase,
			ToolName:               toolName,
			ToolInput:              toolInput,
			Content:                content,
			ToolCount:              toolCount,
			EventCount:             evCount,
			StartedAt:              startedAt,
			Elapsed:                time.Since(startedAt),
			StreamFilePath:         streamFile,
			StderrFilePath:         stderrFile,
			ContextTokens:          peakContextTokens,
			ContextWindow:          contextWindow,
			CumulativeInputTokens:  cumInputTokens,
			CumulativeOutputTokens: cumOutputTokens,
			EstimatedCost:          estimatedCost,
		})
	}

	stallWarn := r.StallWarnAfter
	if stallWarn == 0 {
		stallWarn = defaultStallWarn
	}
	var stallC <-chan time.Time
	var stallTimer *time.Timer
	if stallWarn > 0 {
		stallTimer = time.NewTimer(stallWarn)
		defer stallTimer.Stop()
		stallC = stallTimer.C
	}
	lastEventAt := time.Now()

	processEvent := func(ev agent.StreamEvent) {
		eventCount++
		lastEventAt = time.Now()

		if ev.ContextTokens > peakContextTokens {
			peakContextTokens = ev.ContextTokens
		}
		// Non-terminal events (assistant, tool_use) carry running totals
		// from the agent. Terminal events (result, error) carry final
		// totals — still monotonically ≥ the last running snapshot.
		if ev.InputTokens > cumInputTokens {
			cumInputTokens = ev.InputTokens
		}
		if ev.OutputTokens > cumOutputTokens {
			cumOutputTokens = ev.OutputTokens
		}
		if ev.Cost > estimatedCost {
			estimatedCost = ev.Cost
		}

		switch ev.Type {
		case "system":
			if ev.PID > 0 && r.CallDB != nil && callID > 0 {
				if err := r.CallDB.SetPID(callID, ev.PID, containerName); err != nil {
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
			if ev.ContextWindow > 0 {
				contextWindow = ev.ContextWindow
			}

		case "error":
			// A prior "result" event already classified the run (with
			// cost, usage, IsError, ErrorSource=agent_api). The trailing
			// process error from cmd.Wait is usually just the agent
			// exiting non-zero to surface its own is_error — keep the
			// rich result and only adopt the exit code.
			if resultEv != nil && resultEv.Type == "result" {
				if ev.ExitCode != 0 && resultEv.ExitCode == 0 {
					resultEv.ExitCode = ev.ExitCode
				}
			} else {
				evCopy := ev
				resultEv = &evCopy
			}
		}
	}

eventLoop:
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				break eventLoop
			}
			processEvent(ev)
		case now := <-stallC:
			// `select` picks randomly among ready cases, so the timer
			// branch can win even when an event is already buffered.
			// Try a non-blocking receive first to avoid false stalls.
			select {
			case ev, ok := <-events:
				if !ok {
					break eventLoop
				}
				processEvent(ev)
				stallTimer.Reset(stallWarn)
				continue
			default:
			}
			// Once ctx is cancelled the run is already terminating;
			// further warnings are noise. Keep the timer armed but
			// stay quiet until events closes.
			if ctx.Err() != nil {
				stallTimer.Reset(stallWarn)
				continue
			}
			idle := now.Sub(lastEventAt)
			if idle < stallWarn {
				stallTimer.Reset(stallWarn - idle)
				continue
			}
			msg := fmt.Sprintf("no agent events for %s", idle.Round(time.Second))
			fmt.Fprintf(os.Stderr, "Warning: %s [role=%s action=%s exec=%d]\n",
				msg, opts.RoleID, opts.Action, callID)
			appendLog(r.LogFile, opts.RoleID, PhaseStall, effectiveWorkDir(opts), msg)
			emitProgress(PhaseStall, "", "", msg, totalTools, eventCount)
			stallTimer.Reset(stallWarn)
		}
	}

	endedAt := time.Now()
	duration := endedAt.Sub(startedAt)

	output := lastOutput
	if resultEv != nil && resultEv.Output != "" {
		output = resultEv.Output
	}

	summary := RunSummary{
		ExecID:            callID,
		RoleID:            opts.RoleID,
		StartedAt:         startedAt,
		EndedAt:           endedAt,
		Duration:          duration,
		Output:            output,
		ToolCounts:        toolCounts,
		PeakContextTokens: peakContextTokens,
		ContextWindow:     contextWindow,
		StreamFilePath:    streamFile,
		StderrFilePath:    stderrFile,
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
			summary.ContextWindow = res.ContextWindow
		}
	}

	// Finalize: write output/error files, update DB, log result.
	if r.finalizeCall(ctx, callID, &summary, resultEv, opts, output, cliStr, cwd) {
		emitProgress(PhaseDone, "", "", "", totalTools, eventCount)
	} else {
		emitProgress(PhaseError, "", "", "", totalTools, eventCount)
	}

	return summary
}

// buildPrompt archives the prompt, resolves CLAUDE_CONFIG_DIR, and assembles
// the agent.Request. Returns the request, the archived prompt file path, and
// any error. The caller must ensure opts.LogsDir exists before calling.
func (r *Runner) buildPrompt(prompt string, opts RunOpts, startedAt time.Time, tmplVars TemplateVars, cwd, streamFile, stderrFile string, extraArgs []string) (agent.Request, string, error) {
	promptFile := archivePrompt(opts.HistoryDir, opts.PromptName, prompt, startedAt)

	// Resolve CLAUDE_CONFIG_DIR for isolated agents.
	// Relative config_dir is resolved from ProjectDir (.ateam/); absolute is used as-is.
	configDir := ExpandHome(ResolveTemplateString(r.ConfigDir, tmplVars))
	var reqEnv map[string]string
	if configDir != "" {
		var configPath string
		if filepath.IsAbs(configDir) {
			configPath = configDir
		} else {
			if r.ProjectDir == "" {
				return agent.Request{}, "", fmt.Errorf("relative config_dir requires project context (no .ateam/ found)")
			}
			configPath = filepath.Join(r.ProjectDir, configDir)
		}
		reqEnv = map[string]string{"CLAUDE_CONFIG_DIR": configPath}
	}

	req := agent.Request{
		Prompt:     prompt,
		WorkDir:    cwd,
		StreamFile: streamFile,
		StderrFile: stderrFile,
		ExtraArgs:  extraArgs,
		Env:        reqEnv,
	}
	return req, promptFile, nil
}

// setupContainer prepares the container for execution and translates the
// request's WorkDir and settings paths to container-relative paths. It
// operates on the per-run container clone (never the shared original) and
// returns the resolved container name so the caller can record it locally.
func setupContainer(ctx context.Context, c container.Container, req *agent.Request, cwd string) (string, error) {
	if c == nil {
		return "", nil
	}
	if err := c.Prepare(ctx); err != nil {
		return "", err
	}
	if factory := c.CmdFactory(); factory != nil {
		req.CmdFactory = factory
	}
	// Note: StreamFile and StderrFile are NOT translated — they are
	// opened by the host process (os.Create) to capture piped output,
	// not accessed inside the container.
	req.WorkDir = c.TranslatePath(cwd)
	for i, a := range req.ExtraArgs {
		if a == "--settings" && i+1 < len(req.ExtraArgs) {
			req.ExtraArgs[i+1] = c.TranslatePath(req.ExtraArgs[i+1])
		}
	}
	return c.GetContainerName(), nil
}

// finalizeCall handles post-execution work: writes the output or error file,
// appends to the runner log, and updates the call tracking record. Returns
// true on success.
func (r *Runner) finalizeCall(ctx context.Context, callID int64, summary *RunSummary, resultEv *agent.StreamEvent, opts RunOpts, output, cliStr, cwd string) bool {
	success := resultEv != nil && resultEv.Type == "result" && resultEv.ExitCode == 0 && !resultEv.IsError

	if success {
		if opts.LastMessageFilePath != "" && output != "" {
			dir := filepath.Dir(opts.LastMessageFilePath)
			if err := os.MkdirAll(dir, 0700); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to create output dir %s: %v\n", dir, err)
			}
			if err := os.WriteFile(opts.LastMessageFilePath, []byte(output), 0600); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to write output file %s: %v\n", opts.LastMessageFilePath, err)
			}
		}
		appendLog(r.LogFile, opts.RoleID, "ok", cwd, cliStr)
	} else {
		summary.IsError = true
		source, cause := classifyFailure(ctx, resultEv, opts.TimeoutMin)
		summary.ErrorSource = source
		summary.ErrorCause = cause
		summary.Err = fmt.Errorf("[%s] %s", source, cause)
		writeErrorFile(opts.ErrorMessageFilePath, *summary, "")
		appendStderrSummary(summary.StderrFilePath, *summary)
		appendLog(r.LogFile, opts.RoleID, "error", cwd, cliStr, summary.Err.Error())
	}

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
			EndedAt:           summary.EndedAt,
			DurationMS:        summary.DurationMS,
			ExitCode:          summary.ExitCode,
			IsError:           summary.IsError,
			ErrorMessage:      errMsg,
			CostUSD:           summary.Cost,
			InputTokens:       summary.InputTokens,
			OutputTokens:      summary.OutputTokens,
			CacheReadTokens:   summary.CacheReadTokens,
			CacheWriteTokens:  summary.CacheWriteTokens,
			Turns:             summary.Turns,
			Model:             resultModel,
			PeakContextTokens: summary.PeakContextTokens,
			ContextWindow:     summary.ContextWindow,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: call tracking update failed: %v\n", err)
		}
	}

	return success
}

// RenderSettings generates the merged sandbox settings JSON without writing to disk.
// workDir is the effective working directory (e.g. SourceDir).
func (r *Runner) RenderSettings(workDir string) ([]byte, error) {
	if r.Sandbox.Settings == "" {
		return nil, nil
	}
	var settings map[string]any
	if err := json.Unmarshal([]byte(r.Sandbox.Settings), &settings); err != nil {
		return nil, fmt.Errorf("cannot parse sandbox settings: %w", err)
	}

	r.mergeSandboxPaths(settings, workDir, nil)
	return json.MarshalIndent(settings, "", "  ")
}

// writeSettings parses the inline sandbox settings JSON from the agent config,
// merges in runtime paths, and writes the result to settingsPath.
func (r *Runner) writeSettings(settingsPath string, opts RunOpts) ([]byte, error) {
	var settings map[string]any
	if err := json.Unmarshal([]byte(r.Sandbox.Settings), &settings); err != nil {
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
	runtimeWriteDirs = append(runtimeWriteDirs, r.Sandbox.ExtraWriteDirs...)
	runtimeWriteDirs = append(runtimeWriteDirs, r.Sandbox.RWPaths...)
	runtimeWriteDirs = append(runtimeWriteDirs, r.Sandbox.ExtraWrite...)

	var runtimeReadDirs []string
	if r.OrgDir != "" {
		runtimeReadDirs = append(runtimeReadDirs, r.OrgDir)
	}
	runtimeReadDirs = append(runtimeReadDirs, r.Sandbox.ExtraRead...)

	// additionalDirectories: project root, .ateamorg/, agent ro_paths
	runtimeAdditionalDirs := []string{workDir}
	if r.OrgDir != "" {
		runtimeAdditionalDirs = append(runtimeAdditionalDirs, r.OrgDir)
	}
	runtimeAdditionalDirs = append(runtimeAdditionalDirs, r.Sandbox.ROPaths...)

	mergeStringList(settings, []string{"sandbox", "filesystem", "allowWrite"}, runtimeWriteDirs)
	mergeStringList(settings, []string{"sandbox", "filesystem", "allowRead"}, runtimeReadDirs)
	denyPaths := append([]string{}, extraDenyWrite...)
	denyPaths = append(denyPaths, r.Sandbox.Denied...)
	if len(denyPaths) > 0 {
		mergeStringList(settings, []string{"sandbox", "filesystem", "denyWrite"}, denyPaths)
	}
	mergeStringList(settings, []string{"permissions", "additionalDirectories"}, runtimeAdditionalDirs)
	if len(r.Sandbox.ExtraDomains) > 0 {
		mergeStringList(settings, []string{"sandbox", "network", "allowedDomains"}, r.Sandbox.ExtraDomains)
	}
	if len(r.Sandbox.ExtraExcludedCmd) > 0 {
		mergeStringList(settings, []string{"sandbox", "excludedCommands"}, r.Sandbox.ExtraExcludedCmd)
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

// Truncate shortens s to at most max bytes on a rune boundary, appending "…"
// when it had to cut. Returns "" for max<=0 and the original s when it fits.
func Truncate(s string, max int) string { return truncate(s, max) }

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

// classifyFailure determines why a run failed given the context state and
// the final stream event received (may be nil).
func classifyFailure(ctx context.Context, resultEv *agent.StreamEvent, timeoutMin int) (source, cause string) {
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		return agent.ErrorSourceAteamTimeout,
			fmt.Sprintf("ateam timed out the run after %d minutes", timeoutMin)
	case resultEv != nil && resultEv.ErrorCause != "":
		src := resultEv.ErrorSource
		if src == "" {
			src = agent.ErrorSourceAgentProcess
		}
		return src, resultEv.ErrorCause
	case resultEv != nil && resultEv.Err != nil:
		return agent.ErrorSourceAgentProcess, resultEv.Err.Error()
	case resultEv != nil && resultEv.IsError:
		return agent.ErrorSourceAgentAPI, "agent reported is_error=true with no message"
	default:
		return agent.ErrorSourceAteamInternal, "agent produced no result event"
	}
}

// appendStderrSummary writes a short, grep-able failure summary to the stderr
// log so the cause is visible without parsing _stream.jsonl. No-op if path is
// empty or the file cannot be opened.
func appendStderrSummary(path string, s RunSummary) {
	if path == "" || s.ErrorSource == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "\n--- ateam: run failed ---\nsource: %s\ncause: %s\nexit: %d\nduration: %s\n",
		s.ErrorSource, s.ErrorCause, s.ExitCode, FormatDuration(s.Duration))
	// agent_api failures carry real totals from the result event; every
	// other failure path with non-zero tokens means we reconstructed the
	// figure from partial assistant events — flag it as estimated.
	if s.ErrorSource != agent.ErrorSourceAgentAPI && s.InputTokens > 0 {
		fmt.Fprintf(f, "estimated: true (tokens and cost recovered from partial stream)\n")
	}
}

func writeErrorFile(path string, s RunSummary, stderr string) {
	if path == "" {
		return
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to create error file dir %s: %v\n", dir, err)
	}

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

	if err := os.WriteFile(path, []byte(b.String()), 0600); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to write error file %s: %v\n", path, err)
	}
}

func appendLog(logFile, roleID, status, cwd, cli string, extra ...string) {
	if logFile == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(logFile), 0700); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to create log dir %s: %v\n", filepath.Dir(logFile), err)
	}
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
	if err := os.MkdirAll(historyDir, 0700); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to create prompt history dir %s: %v\n", historyDir, err)
	}
	ts := startedAt.Format(TimestampFormat)
	name := strings.ReplaceAll(fmt.Sprintf("%s.%s", ts, promptName), " ", "_")
	path := filepath.Join(historyDir, name)
	if err := os.WriteFile(path, []byte(prompt), 0600); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to archive prompt to %s: %v\n", path, err)
	}
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
	if path == "" {
		return
	}
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

	if err := os.WriteFile(path, []byte(b.String()), 0600); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to write exec file %s: %v\n", path, err)
	}
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
		"PASS", "CERT", "PEM",
	} {
		if strings.Contains(up, substr) {
			return true
		}
	}
	return false
}
