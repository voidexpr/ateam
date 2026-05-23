package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	codextui "github.com/ateam/internal/codex"
	"github.com/ateam/internal/tmuxctl"
)

// CodexTmuxAgent drives the interactive Codex TUI through tmux so TUI-only
// input such as /review can be used from normal ATeam runs.
//
// Auth and config: this agent does NOT manipulate `$CODEX_HOME`. Codex reads
// `~/.codex/` natively. The first trust dialog in a new workdir is
// auto-accepted (Enter) which causes Codex to write one
// `[projects."<workdir>"] trust_level = "trusted"` entry to the user's
// `~/.codex/config.toml` — identical to running codex by hand. See
// `plans/feature_codex_tmux_agent.md` for the full rationale.
type CodexTmuxAgent struct {
	Command          string
	Args             []string
	Model            string
	Effort           string
	MaxBudgetUSD     string
	DefaultModel     string
	Pricing          PricingTable
	Env              map[string]string
	StartTimeout     time.Duration
	BusyTimeout      time.Duration
	QuiescenceWindow time.Duration
	TmuxWidth        int
	TmuxHeight       int
	// ProjectDir is the absolute path to the ateam project (the dir that
	// contains `.ateam/`). Used to root the tmux socket and any future
	// per-run scratch state. Set by cmd/table.go from ResolvedEnv at
	// agent construction time so it survives clone-for-pool dispatch.
	ProjectDir string
}

func (c *CodexTmuxAgent) Name() string { return NameCodexTmux }

func (c *CodexTmuxAgent) ModelName() string {
	if c.Model != "" {
		return c.Model
	}
	return c.DefaultModel
}

func (c *CodexTmuxAgent) SetModel(model string) { c.Model = model }

func (c *CodexTmuxAgent) SetEffort(effort string) { c.Effort = effort }

func (c *CodexTmuxAgent) SetMaxBudgetUSD(value string) { c.MaxBudgetUSD = value }

func (c *CodexTmuxAgent) AgentEnv() map[string]string { return c.Env }

func (c *CodexTmuxAgent) CloneWithResolvedTemplates(replacer *strings.Replacer) Agent {
	clone := *c
	clone.Args = resolveSlice(c.Args, replacer)
	clone.Env = resolveStringMap(c.Env, replacer)
	clone.Pricing = c.Pricing.Clone()
	return &clone
}

func (c *CodexTmuxAgent) DebugCommandArgs(extraArgs []string) (string, []string) {
	command := c.Command
	if command == "" {
		command = "codex"
	}
	return command, codextui.InteractiveArgs(c.Args, c.Model, c.Effort, extraArgs)
}

func (c *CodexTmuxAgent) Run(ctx context.Context, req Request) <-chan StreamEvent {
	ch := make(chan StreamEvent, 8)
	go c.run(ctx, req, ch)
	return ch
}

func (c *CodexTmuxAgent) run(ctx context.Context, req Request, ch chan<- StreamEvent) {
	defer close(ch)

	stderrWriters, streamWriter, closers := setupStreamFiles(req)
	for _, c := range closers {
		defer c.Close()
	}
	stderr := io.MultiWriter(stderrWriters...)

	if c.ProjectDir == "" {
		ch <- errorEvent(fmt.Errorf("codex-tmux requires project context (no .ateam/ found)"), ErrorSourceAteamInternal, -1)
		return
	}
	if req.ExecID <= 0 {
		ch <- errorEvent(fmt.Errorf("codex-tmux requires ExecID on the request"), ErrorSourceAteamInternal, -1)
		return
	}

	sessionName := codextui.SessionName(req.ExecID)
	socketPath := codextui.SocketPath(c.ProjectDir, req.ExecID)

	env := explicitEnv(c.Env, req.Env)

	cfg := codextui.TmuxConfig{
		Command:          firstNonEmpty(c.Command, "codex"),
		Args:             c.Args,
		ExtraArgs:        req.ExtraArgs,
		Model:            c.Model,
		Effort:           c.Effort,
		InitialInput:     req.Prompt,
		WorkDir:          req.WorkDir,
		Env:              env,
		Width:            c.TmuxWidth,
		Height:           c.TmuxHeight,
		StartTimeout:     c.StartTimeout,
		BusyTimeout:      c.BusyTimeout,
		QuiescenceWindow: c.QuiescenceWindow,
		CmdFactory:       tmuxctl.CmdFactory(req.CmdFactory),
		SessionName:      sessionName,
		SocketPath:       socketPath,
		ExecID:           req.ExecID,
		// tmux.log lands next to stream.jsonl / stderr.out under the
		// per-EXEC_ID logs dir. Real-time diagnostic that survives stuck
		// runs (where stream.jsonl is still empty).
		TmuxLogPath: tmuxLogPathFor(req.StreamFile),
		OnPanePID: func(pid int) {
			// Emit the codex pane PID so the runner records it in
			// agent_execs.pid. The runner's processEvent handler
			// reads ev.PID off the first system event; the subtype
			// `process_start` is what other agents emit too.
			ch <- StreamEvent{Type: "system", Subtype: "process_start", PID: pid, Model: c.ModelName()}
		},
		OnHeartbeat: func(preview string) {
			// Surface pane-change heartbeats as `thinking` events. The
			// runner's stall watchdog resets on every event, so a
			// long-running /review that emits no other intermediate
			// output stops mis-firing "agent stalled" warnings. Text is
			// a short pane preview so an operator tailing the live log
			// sees forward progress.
			ch <- StreamEvent{Type: "thinking", Text: preview}
		},
	}

	start := time.Now()
	writeSyntheticStream(streamWriter, "tmux.start", map[string]any{
		"initial_input":     req.Prompt,
		"model":             c.ModelName(),
		"tmux_session_name": sessionName,
		"tmux_socket_path":  socketPath,
	})

	// Live-tail codex's rollout JSONL into stream.jsonl. Each translated
	// line is a codex-exec-stream-shape event (turn.started, agent_message,
	// turn.completed) that parse_stream.go renders for `ateam tail`/`cat`.
	// Stops when this run's context is canceled or the function returns.
	tailCtx, tailCancel := context.WithCancel(ctx)
	defer tailCancel()
	var tailWg sync.WaitGroup
	if streamWriter != nil {
		marker := ""
		// Free-form prompts get an EXEC_ID marker injected by
		// preparePrompt; match it here so we lock onto OUR session
		// file even if a concurrent codex-tmux run shares the workdir.
		// Slash commands have no marker (we can't inject text); fall
		// back to cwd+timestamp matching.
		if !strings.HasPrefix(strings.TrimSpace(req.Prompt), "/") {
			marker = codextui.SessionLogMarker(req.ExecID)
		}
		tailWg.Add(1)
		go func() {
			defer tailWg.Done()
			codextui.TailSessionLog(tailCtx, "", req.WorkDir, start, marker, func(line []byte) {
				_, _ = streamWriter.Write(line)
				_ = streamWriter.Flush()
			})
		}()
	}

	result, err := codextui.RunTmux(ctx, cfg)
	// Stop the live tailer now that codex has finished — its post-run
	// ReadSessionStats read (inside RunTmux) already captured the final
	// usage, and any new lines codex writes during shutdown are noise.
	// Wait for the tailer goroutine to exit before writing further to
	// streamWriter; bufio.Writer is not goroutine-safe.
	tailCancel()
	tailWg.Wait()
	if result.SessionName != "" {
		ch <- StreamEvent{Type: "system", Subtype: "tmux_session", SessionID: result.SessionName, Model: c.ModelName()}
	}

	// Best-effort: gzip-copy the rollout JSONL into .ateam/logs/<EXEC_ID>/
	// so `ateam inspect` lists it and the rollout survives a CODEX_HOME
	// wipe. Failures don't fail the run.
	archivedPath := ""
	archivedBytes := int64(0)
	if result.SessionStats.SessionLogPath != "" && req.StreamFile != "" {
		dst := filepath.Join(filepath.Dir(req.StreamFile), "codex-session.jsonl.gz")
		if n, aerr := codextui.GzipCopyFile(result.SessionStats.SessionLogPath, dst); aerr == nil {
			archivedPath = dst
			archivedBytes = n
		} else {
			fmt.Fprintf(stderr, "codex-tmux: failed to archive codex session log to %s: %v\n", dst, aerr)
		}
	}
	// Best-effort: gzip-replace tmux.log with tmux.log.gz. Trace files
	// for long /review runs reach ~1MB and compress ~15x; the .gz still
	// streams cleanly under `gunzip -c | less`.
	if tmuxLog := tmuxLogPathFor(req.StreamFile); tmuxLog != "" {
		if _, statErr := os.Stat(tmuxLog); statErr == nil {
			dst := tmuxLog + ".gz"
			if _, aerr := codextui.GzipCopyFile(tmuxLog, dst); aerr == nil {
				_ = os.Remove(tmuxLog)
			} else {
				fmt.Fprintf(stderr, "codex-tmux: failed to gzip %s: %v\n", tmuxLog, aerr)
			}
		}
	}
	if result.Output != "" {
		ch <- StreamEvent{Type: "assistant", Text: result.Output, IsModelResponse: true}
		writeSyntheticStream(streamWriter, "assistant", map[string]any{"text": result.Output})
	}

	model := c.ModelName()
	// Prefer the model name Codex itself recorded in its rollout JSONL —
	// that's the actual model used after any --profile / fast-mode tweaks.
	if result.SessionStats.Model != "" {
		model = result.SessionStats.Model
	}
	contextWindow := ContextWindow(model)
	if result.SessionStats.ContextWindow > 0 {
		contextWindow = result.SessionStats.ContextWindow
	}
	resultEvent := StreamEvent{
		Type:            "result",
		Output:          result.Output,
		Model:           model,
		DurationMS:      result.Duration.Milliseconds(),
		Turns:           1,
		ContextWindow:   contextWindow,
		InputTokens:     result.SessionStats.InputTokens,
		OutputTokens:    result.SessionStats.OutputTokens,
		CacheReadTokens: result.SessionStats.CachedInputTokens,
		ContextTokens:   result.SessionStats.TotalTokens,
		Cost: EstimateCost(c.Pricing, model, c.DefaultModel,
			result.SessionStats.InputTokens,
			result.SessionStats.CachedInputTokens,
			result.SessionStats.OutputTokens),
	}
	if result.SessionStats.DurationMS > 0 {
		resultEvent.DurationMS = result.SessionStats.DurationMS
	}
	if resultEvent.DurationMS == 0 {
		resultEvent.DurationMS = time.Since(start).Milliseconds()
	}
	if err != nil {
		fmt.Fprintf(stderr, "codex-tmux: %v\n", err)
		if result.RawCapture != "" {
			// Strip empty + non-ASCII-only decorative lines so the
			// diagnostic isn't padded with dozens of blank lines and
			// horizontal separators.
			fmt.Fprintf(stderr, "\n--- codex tmux capture ---\n%s\n--- end codex tmux capture ---\n", codextui.CleanCapture(result.RawCapture))
		}
		resultEvent.IsError = true
		resultEvent.ExitCode = -1
		resultEvent.Err = err
		resultEvent.ErrorSource = ErrorSourceAgentProcess
		if ctx.Err() != nil {
			resultEvent.ErrorSource = ErrorSourceUserCanceled
		}
		resultEvent.ErrorCause = err.Error()
	} else {
		resultEvent.ExitCode = 0
	}

	writeSyntheticStream(streamWriter, "result", map[string]any{
		"duration_ms":       resultEvent.DurationMS,
		"exit_code":         resultEvent.ExitCode,
		"is_error":          resultEvent.IsError,
		"error_source":      resultEvent.ErrorSource,
		"error_cause":       resultEvent.ErrorCause,
		"idle_signal":       string(result.IdleSignal),
		"tmux_session_name": result.SessionName,
		"output_chars":      len(result.Output),
		"pane_pid":          result.PanePID,
		"session_log":       result.SessionStats.SessionLogPath,
		"session_log_gz":    archivedPath,
		"session_log_bytes": archivedBytes,
		"input_tokens":      result.SessionStats.InputTokens,
		"output_tokens":     result.SessionStats.OutputTokens,
		"cached_tokens":     result.SessionStats.CachedInputTokens,
		"reasoning_tokens":  result.SessionStats.ReasoningTokens,
		"total_tokens":      result.SessionStats.TotalTokens,
		"context_window":    result.SessionStats.ContextWindow,
		"task_complete":     result.SessionStats.TaskCompleteFound,
		"cost_usd":          resultEvent.Cost,
	})
	ch <- resultEvent
}

// tmuxLogPathFor returns the per-EXEC_ID tmux.log path derived from the
// runner-supplied stream file. Empty if no stream file (early-failure or
// test paths) — codex/RunTmux treats empty as "tracing disabled".
func tmuxLogPathFor(streamFile string) string {
	if streamFile == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(streamFile), "tmux.log")
}

func explicitEnv(agentEnv, reqEnv map[string]string) map[string]string {
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

func writeSyntheticStream(w *bufio.Writer, typ string, fields map[string]any) {
	if w == nil {
		return
	}
	if fields == nil {
		fields = map[string]any{}
	}
	fields["type"] = typ
	b, err := json.Marshal(fields)
	if err != nil {
		return
	}
	_, _ = w.Write(append(b, '\n'))
}
