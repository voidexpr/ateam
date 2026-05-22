package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	codextui "github.com/ateam/internal/codex"
	"github.com/ateam/internal/tmuxctl"
)

// CodexTmuxAgent drives the interactive Codex TUI through tmux so TUI-only
// input such as /review can be used from normal ATeam runs.
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
	env, envErr := codexTmuxEnv(req.WorkDir, c.Env, req.Env)
	if envErr != nil {
		ch <- errorEvent(envErr, ErrorSourceAteamInternal, -1)
		return
	}

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
	}

	start := time.Now()
	writeSyntheticStream(streamWriter, "tmux.start", map[string]any{
		"initial_input": req.Prompt,
		"model":         c.ModelName(),
	})

	result, err := codextui.RunTmux(ctx, cfg)
	if result.SessionName != "" {
		ch <- StreamEvent{Type: "system", Subtype: "tmux_session", SessionID: result.SessionName, Model: c.ModelName()}
	}
	if result.Output != "" {
		ch <- StreamEvent{Type: "assistant", Text: result.Output, IsModelResponse: true}
		writeSyntheticStream(streamWriter, "assistant", map[string]any{"text": result.Output})
	}

	model := c.ModelName()
	resultEvent := StreamEvent{
		Type:          "result",
		Output:        result.Output,
		Model:         model,
		DurationMS:    result.Duration.Milliseconds(),
		Turns:         1,
		ContextWindow: ContextWindow(model),
	}
	if resultEvent.DurationMS == 0 {
		resultEvent.DurationMS = time.Since(start).Milliseconds()
	}
	if err != nil {
		fmt.Fprintf(stderr, "codex-tmux: %v\n", err)
		if result.RawCapture != "" {
			fmt.Fprintf(stderr, "\n--- codex tmux capture ---\n%s\n--- end codex tmux capture ---\n", result.RawCapture)
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
	})
	ch <- resultEvent
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

func codexTmuxEnv(workdir string, agentEnv, reqEnv map[string]string) (map[string]string, error) {
	env := explicitEnv(agentEnv, reqEnv)
	if _, ok := env["CODEX_HOME"]; ok {
		return env, nil
	}
	codexHome, err := ensureWritableCodexHome(workdir)
	if err != nil {
		return nil, err
	}
	if env == nil {
		env = map[string]string{}
	}
	env["CODEX_HOME"] = codexHome
	return env, nil
}

func ensureWritableCodexHome(workdir string) (string, error) {
	if workdir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		workdir = cwd
	}
	dir := filepath.Join(workdir, ".cache", "codex-home")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create codex home %s: %w", dir, err)
	}
	if err := seedCodexHomeFile(dir, "auth.json"); err != nil {
		return "", err
	}
	if err := writeCodexTmuxConfig(dir, workdir); err != nil {
		return "", err
	}
	return dir, nil
}

func writeCodexTmuxConfig(dstDir, workdir string) error {
	dst := filepath.Join(dstDir, "config.toml")
	if info, err := os.Lstat(dst); err == nil && info.Mode()&os.ModeSymlink != 0 {
		if err := os.Remove(dst); err != nil {
			return err
		}
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}

	var b strings.Builder
	b.WriteString("check_for_update_on_startup = false\n\n")
	if workdir != "" {
		b.WriteString("[projects.")
		b.WriteString(strconv.Quote(workdir))
		b.WriteString("]\ntrust_level = \"trusted\"\n")
	}
	return os.WriteFile(dst, []byte(b.String()), 0600)
}

func seedCodexHomeFile(dstDir, name string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	src := filepath.Join(home, ".codex", name)
	if _, err := os.Stat(src); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	dst := filepath.Join(dstDir, name)
	if _, err := os.Lstat(dst); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Symlink(src, dst); err == nil {
		return nil
	}
	content, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, content, 0600)
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
