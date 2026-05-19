// Package runner orchestrates agent execution, managing scheduling, output collection, and result persistence.
package runner

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/container"
	"github.com/ateam/internal/display"
	"github.com/ateam/internal/fsclone"
	"github.com/ateam/internal/gitutil"
)

const (
	ActionReport   = "report"
	ActionExec     = "exec"
	ActionParallel = "parallel"
	ActionCode     = "code"
	ActionReview   = "review"
	ActionVerify   = "verify"
	ActionDebug    = "debug"
)

// IsInContainer detects whether the current process is running inside a container.
// Delegates to container.IsInContainer.
func IsInContainer() bool {
	return container.IsInContainer()
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
//     including PoolExec.Runner overrides — its fields become READ-ONLY.
//   - Agent and Container fields look mutable but are cloned per agent exec at the
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
	ProjectDir           string              // .ateam/ dir
	OrgDir               string              // .ateamorg/ dir
	SourceDir            string              // project root (parent of .ateam/)
	ProjectName          string              // from config.toml
	ExtraArgs            []string            // extra args passed to the agent
	Sandbox              SandboxConfig       // sandbox filesystem/network restrictions
	ConfigDir            string              // CLAUDE_CONFIG_DIR; relative resolves from ProjectDir
	ArgsInsideContainer  []string            // extra args when inside a container
	ArgsOutsideContainer []string            // extra args when on the host
	CallDB               *calldb.CallDB      // required: every Run inserts an exec row
	Profile              string              // profile name for DB
	ProfileDef           string              // verbatim profile definition (HCL) for cmd.md
	AgentDef             string              // verbatim agent definition (HCL) for cmd.md
	ContainerType        string              // "none" or "docker" for DB
	ContainerName        string              // docker container name for liveness checks
	ContainerNameSource  string              // where ContainerName came from (ContainerNameSource* constants)
	ProjectID            string              // project ID for DB

	// StallWarnAfter is the idle duration after which Run logs a stall
	// warning and emits a PhaseStall progress event. Re-armed after each
	// warning. 0 = 5m default. Negative = disable the watchdog.
	StallWarnAfter time.Duration
}

// defaultStallWarn is the idle threshold before Run logs a stall warning.
// Five minutes accommodates slow API calls, container startup, and long
// extended-thinking passes between events without producing false alarms.
const defaultStallWarn = 5 * time.Minute

// RunOpts holds per-invocation settings.
//
// All paths are derived from the exec_id returned by InsertCall — callers do
// not pre-compute log or history paths. The runner owns:
//   - logs/<exec_id>/        forensic artefacts (stream, stderr, settings, prompt, cmd.md)
//   - runtime/<exec_id>/     agent-writable output area; primary file driven by OutputKind
//   - <CanonicalDestDir>/    where runtime files are cloned on success (e.g. roles/<id>/)
type RunOpts struct {
	RoleID           string
	Action           string // "report", "exec", "code", "review", ...
	OutputKind       string // OutputKindReport / Review / Verify / ExecutionReport / SetupOverview / "" (no primary output)
	CanonicalDestDir string // where runtime/<exec_id>/ files are cloned on success; "" disables promotion
	WorkDir          string // cwd for the subprocess
	TimeoutMin       int
	Verbose          bool      // print agent and docker commands to stderr
	Batch            string    // groups related agent_execs (e.g. all execs in one ateam code run)
	StartedAt        time.Time // optional override; if zero, Run() uses time.Now()

	// AutoRolesCommandsOutput is the pre-baked context bundle injected into
	// `{{ATEAM_AUTO_ROLES_COMMANDS_OUTPUT}}` for the --auto-roles planner agent.
	// Only set by cmd/auto_roles.go; empty for every other action.
	AutoRolesCommandsOutput string
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
	TurnCount      int
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

// Run executes the agent with the given prompt and options.
func (r *Runner) Run(ctx context.Context, prompt string, opts RunOpts, progress chan<- RunProgress) RunSummary {
	startedAt := opts.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now()
	}

	failPreInsert := func(err error) RunSummary {
		s := RunSummary{
			RoleID:      opts.RoleID,
			StartedAt:   startedAt,
			EndedAt:     time.Now(),
			Duration:    time.Since(startedAt),
			ExitCode:    -1,
			IsError:     true,
			Err:         err,
			ErrorSource: agent.ErrorSourceAteamInternal,
			ErrorCause:  err.Error(),
		}
		return s
	}

	// Hard requirement: a project DB must exist. Without it we can't allocate
	// an exec_id, which the new layout needs for every per-run path.
	if r.CallDB == nil || r.ProjectDir == "" {
		return failPreInsert(fmt.Errorf("ateam project required: no .ateam/ found"))
	}

	// Insert FIRST so callID drives logs/<id>/ and runtime/<id>/.
	agentName := r.Agent.Name()
	model := agent.NormalizeModel(extractModel(r.Agent))
	callID, err := r.CallDB.InsertCall(&calldb.Call{
		ProjectID:  r.ProjectID,
		Profile:    r.Profile,
		Agent:      agentName,
		Container:  r.ContainerType,
		Action:     opts.Action,
		Role:       opts.RoleID,
		Batch:      opts.Batch,
		Model:      model,
		PromptHash: hashPrompt(prompt),
		StartedAt:  startedAt,
		// note: resolve from work-dir, not GitRepoDir or AteamDir. The recorded
		// HEAD must reflect the code the agent actually operated on; centralizing
		// this via GitRepoDir would record a misleading hash for worktree runs or
		// external-repo --work-dir invocations.
		GitStartHash:   gitutil.HeadHash(effectiveWorkDir(opts)),
		GitStartBranch: gitutil.CurrentBranch(effectiveWorkDir(opts)),
		WorkDir:        effectiveWorkDir(opts),
	})
	if err != nil {
		return failPreInsert(fmt.Errorf("call tracking insert failed: %w", err))
	}

	logsDir := logsDirFor(r.ProjectDir, callID)
	runtimeDir := runtimeDirFor(r.ProjectDir, callID)
	streamFile := filepath.Join(logsDir, "stream.jsonl")
	stderrFile := filepath.Join(logsDir, "stderr.out")
	settingsFile := filepath.Join(logsDir, "settings.json")
	cmdFile := filepath.Join(logsDir, "cmd.md")
	promptFile := filepath.Join(logsDir, "prompt.md")

	// Persist the canonical stream path on the row.
	if relStream, relErr := filepath.Rel(r.ProjectDir, streamFile); relErr == nil {
		_ = r.CallDB.UpdateStreamFile(callID, relStream)
	}

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
	if !IsInContainer() && r.Container == nil {
		for _, a := range extraArgs {
			if a == "--dangerously-skip-permissions" {
				fmt.Fprintf(os.Stderr, "Warning: --dangerously-skip-permissions used outside a Docker container. This skips all safety checks.\n")
				break
			}
		}
	}

	// Create the per-exec_id dirs.
	if err := os.MkdirAll(logsDir, 0700); err != nil {
		return failEarly(fmt.Errorf("cannot create logs directory: %w", err))
	}
	if opts.OutputKind != "" {
		if err := os.MkdirAll(runtimeDir, 0700); err != nil {
			return failEarly(fmt.Errorf("cannot create runtime directory: %w", err))
		}
	}

	// Write sandbox settings if configured.
	// Skip when: already inside a container, OR launching into a container (r.Container != nil),
	// unless sandbox_inside_container is explicitly true.
	skipSandbox := (IsInContainer() || r.Container != nil) && !r.Sandbox.InsideContainer
	var settingsJSON []byte
	if r.Sandbox.Settings != "" && !skipSandbox {
		var serr error
		settingsJSON, serr = r.writeSettings(settingsFile, opts)
		if serr != nil {
			return failEarly(fmt.Errorf("cannot create settings file: %w", serr))
		}
		extraArgs = append(extraArgs, "--settings", settingsFile)
	}

	// Clone the container so per-run template resolution and name refresh
	// never mutate shared state across parallel pool workers.
	var runContainer container.Container
	if r.Container != nil {
		runContainer = r.Container.Clone()
	}
	containerName := r.ContainerName

	// Resolve {{VAR}} templates in agent args, extra args, container fields,
	// and the prompt itself (so {{OUTPUT_DIR}} / {{OUTPUT_FILE}} expand using
	// the now-known callID).
	tmplVars := BuildTemplateVars(r, opts, startedAt, callID, agentName, model)
	extraArgs = resolveArgs(extraArgs, tmplVars.Replacer())
	runAgent := ResolveAgentTemplateArgs(r.Agent, tmplVars)
	resolveContainerTemplates(runContainer, tmplVars)
	prompt = ResolveTemplateString(prompt, tmplVars)
	// Resolve {{EXEC_ID}} (and friends) inside CanonicalDestDir so callers can
	// build per-exec_id paths without knowing the id ahead of time.
	opts.CanonicalDestDir = ResolveTemplateString(opts.CanonicalDestDir, tmplVars)

	// Build agent request (no longer archives prompt — that's our job below).
	req, err := r.buildRequest(prompt, tmplVars, cwd, streamFile, stderrFile, extraArgs)
	if err != nil {
		return failEarly(err)
	}

	// Archive the rendered prompt next to the rest of the forensics.
	if err := os.WriteFile(promptFile, []byte(prompt), 0600); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to write prompt file %s: %v\n", promptFile, err)
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

	// Pre-run cmd.md (Run details left as "(pending)" — re-rendered at finalize).
	cmdInfo := cmdFileInfo{
		StartedAt:     startedAt,
		ExecID:        callID,
		Profile:       r.Profile,
		ProfileDef:    r.ProfileDef,
		Agent:         agentName,
		AgentDef:      r.AgentDef,
		Model:         model,
		ContainerType: r.ContainerType,
		ContainerName: containerName,
		Action:        opts.Action,
		Role:          opts.RoleID,
		Batch:         opts.Batch,
		Cwd:           cwd,
		CLI:           cliStr,
		SpecifiedEnv:  mergedAgentEnv(runAgent, req.Env),
		SettingsJSON:  settingsJSON,
	}
	writeCmdFile(cmdFile, cmdInfo)

	// Run the agent and consume events
	events := runAgent.Run(ctx, req)

	var (
		toolCounts        = make(map[string]int)
		eventCount        int
		totalTools        int
		observedTurns     int
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
			TurnCount:              observedTurns,
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

		if ev.IsModelResponse {
			observedTurns++
		}

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
				emitProgress(PhaseThinking, "", "", display.Truncate(ev.Text, 200), totalTools, eventCount)
			}

		case "thinking":
			// Distinct from "assistant" so the run's Output isn't
			// overwritten with reasoning content. Emits a progress
			// heartbeat so the live UI keeps redrawing during long
			// extended-thinking passes between tool calls.
			if ev.Text != "" {
				emitProgress(PhaseThinking, "", "", display.Truncate(ev.Text, 200), totalTools, eventCount)
			}

		case "tool_use":
			toolCounts[ev.ToolName]++
			totalTools++
			emitProgress(PhaseTool, ev.ToolName, display.Truncate(ev.ToolInput, 200), "", totalTools, eventCount)

		case "tool_result":
			emitProgress(PhaseToolResult, "", "", display.Truncate(ev.ToolResult, 200), totalTools, eventCount)

		case "result":
			evCopy := ev
			resultEv = &evCopy
			if ev.ContextWindow > 0 {
				contextWindow = ev.ContextWindow
			}

		case "error":
			resultEv = reconcileErrorEvent(resultEv, ev)
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
			summary.InputTokens = res.InputTokens
			summary.OutputTokens = res.OutputTokens
			summary.CacheReadTokens = res.CacheReadTokens
			summary.CacheWriteTokens = res.CacheWriteTokens
			summary.ContextWindow = res.ContextWindow
		}
	}

	// Turns: prefer the count we observed from IsModelResponse markers
	// across agents. Fall back to the agent-reported value only when we
	// saw nothing (e.g. early crash before any assistant event).
	summary.Turns = observedTurns
	if summary.Turns == 0 && resultEv != nil {
		summary.Turns = resultEv.Turns
	}

	// Streamed-text fallback: if the agent didn't Write a primary OUTPUT_FILE
	// but produced text, seed it into runtime/<exec_id>/<primary>.md so the
	// promote step still copies a non-empty canonical file. Real agents
	// (claude with the Write tool) populate the file directly; this catches
	// mocks and prompts that go off-script.
	if !summary.IsError && opts.OutputKind != "" && output != "" {
		primary := PrimaryOutputName(opts.OutputKind)
		if primary != "" {
			target := filepath.Join(runtimeDir, primary)
			if _, err := os.Stat(target); os.IsNotExist(err) {
				if err := os.WriteFile(target, []byte(output), 0600); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: write fallback output %s: %v\n", target, err)
				}
			}
		}
	}

	// Finalize: promote runtime files, re-render cmd.md, update DB.
	r.finalizeCall(ctx, callID, &summary, resultEv, opts, runtimeDir, cmdFile, cmdInfo)
	if summary.IsError {
		emitProgress(PhaseError, "", "", "", totalTools, eventCount)
	} else {
		emitProgress(PhaseDone, "", "", "", totalTools, eventCount)
	}

	return summary
}

// reconcileErrorEvent decides which event to keep when a stream "error" event
// arrives. A prior "result" event already classified the run (with cost,
// usage, IsError, ErrorSource=agent_api); the trailing process error from
// cmd.Wait is usually just the agent exiting non-zero to surface its own
// is_error, so we keep the richer result and only inherit the exit code.
// Without a prior result, the error event becomes the terminal event.
func reconcileErrorEvent(prev *agent.StreamEvent, ev agent.StreamEvent) *agent.StreamEvent {
	if prev != nil && prev.Type == "result" {
		if ev.ExitCode != 0 && prev.ExitCode == 0 {
			prev.ExitCode = ev.ExitCode
		}
		return prev
	}
	evCopy := ev
	return &evCopy
}

// buildRequest resolves CLAUDE_CONFIG_DIR and assembles the agent.Request.
// The prompt is expected to already have its templates resolved.
func (r *Runner) buildRequest(prompt string, tmplVars TemplateVars, cwd, streamFile, stderrFile string, extraArgs []string) (agent.Request, error) {
	// Resolve CLAUDE_CONFIG_DIR for isolated agents.
	// Relative config_dir is resolved from ProjectDir (.ateam/); absolute is used as-is.
	configDir := display.ExpandHome(ResolveTemplateString(r.ConfigDir, tmplVars))
	var reqEnv map[string]string
	if configDir != "" {
		var configPath string
		if filepath.IsAbs(configDir) {
			configPath = configDir
		} else {
			if r.ProjectDir == "" {
				return agent.Request{}, fmt.Errorf("relative config_dir requires project context (no .ateam/ found)")
			}
			configPath = filepath.Join(r.ProjectDir, configDir)
		}
		reqEnv = map[string]string{"CLAUDE_CONFIG_DIR": configPath}
	}

	return agent.Request{
		Prompt:     prompt,
		WorkDir:    cwd,
		StreamFile: streamFile,
		StderrFile: stderrFile,
		ExtraArgs:  extraArgs,
		Env:        reqEnv,
	}, nil
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
	// Forward request-scoped env into the container (translated to container
	// paths). Once the agent attaches CmdFactory it skips buildProcessEnv,
	// so without this step per-run overrides like CLAUDE_CONFIG_DIR never
	// reach the docker invocation. req.Env itself is left untranslated so
	// host-side preflight (e.g. mkdir for an isolated config dir) still
	// targets the bind-mounted host path.
	if len(req.Env) > 0 {
		translated := make(map[string]string, len(req.Env))
		for k, v := range req.Env {
			translated[k] = c.TranslatePath(v)
		}
		c.ApplyAgentEnv(translated)
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

// finalizeCall handles post-execution work: promotes runtime files to their
// canonical destinations, re-renders cmd.md with `# Run details` filled in
// and a `# Files Copy` section, and updates the agent_execs row. On failure
// it sets summary.IsError, ErrorSource, ErrorCause, and Err so callers can
// branch off summary.IsError without a separate return value.
func (r *Runner) finalizeCall(ctx context.Context, callID int64, summary *RunSummary, resultEv *agent.StreamEvent, opts RunOpts, runtimeDir, cmdFile string, cmdInfo cmdFileInfo) {
	success := resultEv != nil && resultEv.Type == "result" && resultEv.ExitCode == 0 && !resultEv.IsError

	var copyEntries []fileCopyEntry
	var primaryRuntime string

	if success {
		copyEntries, primaryRuntime = r.promoteRuntimeFiles(runtimeDir, opts.CanonicalDestDir, opts.OutputKind)
	} else {
		summary.IsError = true
		source, cause := classifyFailure(ctx, resultEv, opts.TimeoutMin)
		summary.ErrorSource = source
		summary.ErrorCause = cause
		summary.Err = fmt.Errorf("[%s] %s", source, cause)
		appendStderrSummary(summary.StderrFilePath, *summary)
		// Even on failure, record what was found in runtime/ so the trace is
		// complete (files are not promoted, just listed).
		copyEntries = r.listRuntimeForReport(runtimeDir)
	}

	// Re-render cmd.md with finalized run details and the file copy log.
	// EndedAt non-zero is what flips writeCmdFile from "(pending)" to concrete values.
	cmdInfo.Status = summaryStatus(*summary)
	cmdInfo.EndedAt = summary.EndedAt
	cmdInfo.ExitCode = summary.ExitCode
	cmdInfo.FileCopy = copyEntries
	if cmdInfo.Model == "" {
		cmdInfo.Model = resolveExecModel(resultEv, r.Agent)
	}
	writeCmdFile(cmdFile, cmdInfo)

	// Persist the immutable per-exec runtime path (not the canonical, which is
	// overwritten on every run). History views need a stable per-row pointer.
	if primaryRuntime != "" {
		if rel, err := filepath.Rel(r.ProjectDir, primaryRuntime); err == nil {
			_ = r.CallDB.UpdateOutputFile(callID, rel)
		}
	}

	if r.CallDB != nil && callID > 0 {
		errMsg := ""
		if summary.Err != nil {
			errMsg = summary.Err.Error()
		}
		resultModel := resolveExecModel(resultEv, r.Agent)
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
			// note: resolve from work-dir. See GitStartHash note above — the
			// recorded HEAD must reflect the agent's actual work-dir, not a
			// project-wide GitRepoDir or AteamDir.
			GitEndHash:   gitutil.HeadHash(effectiveWorkDir(opts)),
			GitEndBranch: gitutil.CurrentBranch(effectiveWorkDir(opts)),
		}); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: call tracking update failed: %v\n", err)
		}
	}
}

// renderSettingsJSON unmarshals, merges, and re-marshals sandbox settings.
// Returns nil, nil when Settings is empty.
func (r *Runner) renderSettingsJSON(workDir string, extraDenyWrite []string) ([]byte, error) {
	if r.Sandbox.Settings == "" {
		return nil, nil
	}
	var settings map[string]any
	if err := json.Unmarshal([]byte(r.Sandbox.Settings), &settings); err != nil {
		return nil, fmt.Errorf("cannot parse sandbox settings: %w", err)
	}
	r.mergeSandboxPaths(settings, workDir, extraDenyWrite)
	return json.MarshalIndent(settings, "", "  ")
}

// RenderSettings generates the merged sandbox settings JSON without writing to disk.
// workDir is the effective working directory (e.g. SourceDir).
func (r *Runner) RenderSettings(workDir string) ([]byte, error) {
	return r.renderSettingsJSON(workDir, nil)
}

// writeSettings parses the inline sandbox settings JSON from the agent config,
// merges in runtime paths, and writes the result to settingsPath.
func (r *Runner) writeSettings(settingsPath string, opts RunOpts) ([]byte, error) {
	data, err := r.renderSettingsJSON(effectiveWorkDir(opts), []string{settingsPath})
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, nil
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
	case ctx.Err() == context.Canceled:
		// Long-running commands wrap ctx with signal.NotifyContext, so SIGINT /
		// SIGTERM surface here as context.Canceled. Distinguish operator-
		// initiated cancellation from genuine agent failure so the persisted
		// row and stderr summary don't read as "agent_process" / "ateam_internal".
		return agent.ErrorSourceUserCanceled, "run canceled (Ctrl-C, SIGTERM, or parent context canceled)"
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
	started := s.ExitCode != -1 || s.Duration != 0
	fmt.Fprintf(f, "\n--- ateam: run failed ---\nsource: %s\ncause: %s\nexit: %d\nduration: %s\nstarted: %v\n",
		s.ErrorSource, s.ErrorCause, s.ExitCode, display.FormatDuration(s.Duration), started)
	if !started {
		fmt.Fprintf(f, "note: process never launched (exit=-1, duration=0s)\n")
	}
	if strings.Contains(s.ErrorCause, "resource temporarily unavailable") {
		fmt.Fprintf(f, "hint: EAGAIN on fork — OS process table full; a runaway agent spin loop likely exhausted available process slots\n")
	}
	if s.ErrorSource == agent.ErrorSourceAteamTimeout && s.ToolCounts["Agent"] > 0 {
		fmt.Fprintf(f, "note: Agent subagent was called %d time(s) — a subagent was likely still running when the timeout hit\n", s.ToolCounts["Agent"])
		fmt.Fprintf(f, "hint: if other processes on this machine failed with EAGAIN during this run, the subagent spin loop likely exhausted OS process slots\n")
	}
	// agent_api failures carry real totals from the result event; every
	// other failure path with non-zero tokens means we reconstructed the
	// figure from partial assistant events — flag it as estimated.
	if s.ErrorSource != agent.ErrorSourceAgentAPI && s.InputTokens > 0 {
		fmt.Fprintf(f, "estimated: true (tokens and cost recovered from partial stream)\n")
	}
}

func effectiveWorkDir(opts RunOpts) string {
	if opts.WorkDir != "" {
		return opts.WorkDir
	}
	cwd, _ := os.Getwd()
	return cwd
}

// ParseTimestampPrefix parses the leading TimestampFormat prefix
// ("YYYY-MM-DD_HH-MM-SS") from a filename. Returns ok=false when the
// name is too short or doesn't match. Local timezone is used.
func ParseTimestampPrefix(name string) (time.Time, bool) { return display.ParseTimestampPrefix(name) }

// fileCopyEntry records one runtime/<exec_id>/* file's promote decision so
// cmd.md can include a verifiable trace of "what landed where".
type fileCopyEntry struct {
	Source string // path relative to ProjectDir, e.g. "runtime/42/report.md"
	Dest   string // path relative to ProjectDir; empty when skipped
	Note   string // "cloned" or "SKIPPED (...)"
}

// cmdFileInfo bundles the metadata recorded into logs/<exec_id>/cmd.md.
// Written twice: once before the run starts (EndedAt zero → renders as
// "(pending)"), then re-rendered at finalize time with run-result fields
// and the file copy log.
type cmdFileInfo struct {
	StartedAt     time.Time
	EndedAt       time.Time // zero before the run finishes
	ExecID        int64
	Action        string
	Role          string
	Batch         string
	Profile       string
	ProfileDef    string
	Agent         string
	AgentDef      string
	Model         string
	ContainerType string
	ContainerName string
	Cwd           string
	CLI           string
	SpecifiedEnv  map[string]string
	SettingsJSON  []byte
	ExitCode      int
	Status        string
	FileCopy      []fileCopyEntry
}

func writeCmdFile(path string, info cmdFileInfo) {
	if path == "" {
		return
	}
	var b strings.Builder

	// # Runtime — what configured the agent (profile/agent/model)
	fmt.Fprintf(&b, "# Runtime\n")
	if info.Profile != "" {
		fmt.Fprintf(&b, "* profile: %s\n", info.Profile)
	}
	fmt.Fprintf(&b, "* agent: %s\n", info.Agent)
	if info.Model != "" {
		fmt.Fprintf(&b, "* model: %s\n", info.Model)
	}
	if info.ProfileDef != "" {
		fmt.Fprintf(&b, "\n## profile definition\n```hcl\n%s\n```\n", strings.TrimRight(info.ProfileDef, "\n"))
	}
	if info.AgentDef != "" {
		fmt.Fprintf(&b, "\n## agent definition\n```hcl\n%s\n```\n", strings.TrimRight(info.AgentDef, "\n"))
	}

	// # Run details — start/end/model/status (two-pass; EndedAt zero on first write)
	hasResult := !info.EndedAt.IsZero()
	endedAt, exitCode, status := "(pending)", "(pending)", "(pending)"
	if hasResult {
		endedAt = info.EndedAt.Format(time.RFC3339)
		exitCode = strconv.Itoa(info.ExitCode)
		status = info.Status
	}
	fmt.Fprintf(&b, "\n# Run details\n")
	fmt.Fprintf(&b, "* Started At: %s\n", info.StartedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "* Ended At: %s\n", endedAt)
	fmt.Fprintf(&b, "* Model: %s\n", info.Model)
	fmt.Fprintf(&b, "* Exit Code: %s\n", exitCode)
	fmt.Fprintf(&b, "* Status: %s\n", status)

	// # Command — invocation context
	fmt.Fprintf(&b, "\n# Command\n")
	if info.ExecID > 0 {
		fmt.Fprintf(&b, "* exec_id: %d\n", info.ExecID)
	}
	if info.ContainerType != "" && info.ContainerType != "none" {
		c := info.ContainerType
		if info.ContainerName != "" {
			c += " (" + info.ContainerName + ")"
		}
		fmt.Fprintf(&b, "* container: %s\n", c)
	}
	fmt.Fprintf(&b, "* action: %s\n", info.Action)
	fmt.Fprintf(&b, "* role: %s\n", info.Role)
	if info.Batch != "" {
		fmt.Fprintf(&b, "* batch: %s\n", info.Batch)
	}
	fmt.Fprintf(&b, "* cwd: %s\n", info.Cwd)
	fmt.Fprintf(&b, "* coding agent cli:\n  ```bash\n  %s\n  ```\n", info.CLI)

	fmt.Fprintf(&b, "\n# Env\n")
	fmt.Fprintf(&b, "## Inherited\n```\n")
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
	fmt.Fprintf(&b, "```\n\n## Specified\n```\n")
	keys := make([]string, 0, len(info.SpecifiedEnv))
	for k := range info.SpecifiedEnv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := info.SpecifiedEnv[k]
		switch {
		case v == "":
			// agent.Env / reqEnv entries with empty values mean "unset from
			// the parent process env" (see buildProcessEnv).
			fmt.Fprintf(&b, "unsets %s\n", k)
		case looksLikeSecret(k):
			fmt.Fprintf(&b, "%s=<redacted:%d>\n", k, len(v))
		default:
			fmt.Fprintf(&b, "%s=%s\n", k, v)
		}
	}
	fmt.Fprintf(&b, "```\n")

	if len(info.SettingsJSON) > 0 {
		fmt.Fprintf(&b, "\n# Settings\n```json\n%s\n```\n", string(info.SettingsJSON))
	}

	if hasResult {
		fmt.Fprintf(&b, "\n# Files Copy\n")
		if len(info.FileCopy) == 0 {
			fmt.Fprintf(&b, "_(no files in runtime/%d)_\n", info.ExecID)
		} else {
			for _, e := range info.FileCopy {
				if e.Dest != "" {
					fmt.Fprintf(&b, "* %s → %s  (%s)\n", e.Source, e.Dest, e.Note)
				} else {
					fmt.Fprintf(&b, "* %s  %s\n", e.Source, e.Note)
				}
			}
		}
	}

	if err := os.WriteFile(path, []byte(b.String()), 0600); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to write cmd file %s: %v\n", path, err)
	}
}

// summaryStatus maps a RunSummary onto the short label written into cmd.md's
// `# Run details > Status`.
func summaryStatus(s RunSummary) string {
	switch {
	case s.IsError && s.ErrorSource != "":
		return "error: " + s.ErrorSource
	case s.IsError:
		return "error"
	case s.ExitCode == 0:
		return "ok"
	default:
		return fmt.Sprintf("exit %d", s.ExitCode)
	}
}

// promoteRuntimeFiles clones every file in runtimeDir (except *_prompt.md —
// see TODO) into destDir. Returns the per-file decisions for cmd.md and the
// absolute path of the primary file in runtimeDir (the immutable per-exec
// copy), used for the agent_execs.output_file column. The canonical copy is
// overwritten on subsequent runs and is not suitable as a per-row pointer.
//
// TODO: get rid of this exclusion once configured prompts are kept separate
// from files.
func (r *Runner) promoteRuntimeFiles(runtimeDir, destDir, outputKind string) ([]fileCopyEntry, string) {
	var entries []fileCopyEntry
	primary := PrimaryOutputName(outputKind)
	primaryRuntime := ""

	dir, err := os.ReadDir(runtimeDir)
	if err != nil || len(dir) == 0 {
		return entries, ""
	}

	for _, e := range dir {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		src := filepath.Join(runtimeDir, name)
		entry := fileCopyEntry{Source: relToProject(r.ProjectDir, src)}
		if strings.HasSuffix(name, "_prompt.md") {
			entry.Note = "SKIPPED (*_prompt.md exclusion)"
			entries = append(entries, entry)
			continue
		}
		if destDir == "" {
			entry.Note = "SKIPPED (action has no canonical destination)"
			entries = append(entries, entry)
			continue
		}
		dst := filepath.Join(destDir, name)
		if name == primary {
			primaryRuntime = src
		}
		if err := fsclone.Clone(src, dst); err != nil {
			entry.Note = fmt.Sprintf("FAILED (%v)", err)
			fmt.Fprintf(os.Stderr, "Warning: clone %s → %s: %v\n", src, dst, err)
		} else {
			entry.Dest = relToProject(r.ProjectDir, dst)
			entry.Note = "cloned"
		}
		entries = append(entries, entry)
	}
	return entries, primaryRuntime
}

// listRuntimeForReport mirrors promoteRuntimeFiles but does not copy; used on
// the failure path so cmd.md still shows what landed in runtime/<exec_id>/.
func (r *Runner) listRuntimeForReport(runtimeDir string) []fileCopyEntry {
	dir, err := os.ReadDir(runtimeDir)
	if err != nil {
		return nil
	}
	var entries []fileCopyEntry
	for _, e := range dir {
		if e.IsDir() {
			continue
		}
		entries = append(entries, fileCopyEntry{
			Source: relToProject(r.ProjectDir, filepath.Join(runtimeDir, e.Name())),
			Note:   "SKIPPED (run failed; not promoted)",
		})
	}
	return entries
}

func relToProject(projectDir, path string) string {
	if projectDir == "" {
		return path
	}
	if rel, err := filepath.Rel(projectDir, path); err == nil {
		return rel
	}
	return path
}

func extractModel(a agent.Agent) string {
	if mp, ok := a.(agent.ModelProvider); ok {
		return mp.ModelName()
	}
	return ""
}

// mergedAgentEnv returns the union of the agent's configured env and the
// per-request env. reqEnv wins on conflicts, mirroring buildProcessEnv.
// Returns nil when both inputs are empty so writeCmdFile can skip the
// section entirely.
func mergedAgentEnv(a agent.Agent, reqEnv map[string]string) map[string]string {
	var agentEnv map[string]string
	if ep, ok := a.(agent.EnvProvider); ok {
		agentEnv = ep.AgentEnv()
	}
	if len(agentEnv) == 0 && len(reqEnv) == 0 {
		return nil
	}
	out := make(map[string]string, len(agentEnv)+len(reqEnv))
	for k, v := range agentEnv {
		out[k] = v
	}
	for k, v := range reqEnv {
		out[k] = v
	}
	return out
}

// resolveExecModel returns the canonical model name to write into the
// agent_execs row at finalize time. The stream-reported model wins because
// it reflects what actually ran; fall back to the agent's configured model
// so rows that crashed or timed out before the stream produced a result are
// still labelled with the requested model.
func resolveExecModel(resultEv *agent.StreamEvent, a agent.Agent) string {
	if resultEv != nil && resultEv.Model != "" {
		return agent.NormalizeModel(resultEv.Model)
	}
	return agent.NormalizeModel(extractModel(a))
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
