package agent

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/ateam/internal/streamutil"
)

// parseClaudeLine delegates to the shared streamutil parser.
var parseClaudeLine = streamutil.ParseClaudeLine

// ClaudeAgent executes prompts using the Claude CLI.
type ClaudeAgent struct {
	Command      string            // e.g. "claude"
	Args         []string          // base args from config, e.g. ["-p", "--output-format", "stream-json", "--verbose"]
	Model        string            // optional model override (passed as --model flag)
	DefaultModel string            // assumed model for pricing when stream doesn't report one
	Env          map[string]string // env vars to set (empty string = exclude from parent env)
}

func (c *ClaudeAgent) Name() string { return "claude" }

func (c *ClaudeAgent) ModelName() string {
	if c.Model != "" {
		return c.Model
	}
	return c.DefaultModel
}

func (c *ClaudeAgent) DebugCommandArgs(extraArgs []string) (string, []string) {
	command := c.Command
	if command == "" {
		command = "claude"
	}
	return command, buildAgentArgs(c.Args, c.Model, extraArgs)
}

func (c *ClaudeAgent) Run(ctx context.Context, req Request) <-chan StreamEvent {
	ch := make(chan StreamEvent, 64)
	go c.run(ctx, req, ch)
	return ch
}

func (c *ClaudeAgent) run(ctx context.Context, req Request, ch chan<- StreamEvent) {
	defer close(ch)

	// If CLAUDE_CONFIG_DIR is set (isolated mode), create the dir and require sandbox settings.
	if configDir := resolveConfigDir(c.Env, req.Env); configDir != "" {
		if err := os.MkdirAll(configDir, 0755); err != nil {
			ch <- StreamEvent{Type: "error", Err: fmt.Errorf("cannot create config dir %s: %w", configDir, err), ExitCode: -1}
			return
		}
		if !hasSettingsArg(req.ExtraArgs) {
			ch <- StreamEvent{Type: "error", Err: fmt.Errorf("CLAUDE_CONFIG_DIR is set (%s) but no --settings specified; isolated claude requires sandbox settings", configDir), ExitCode: -1}
			return
		}
	}

	// ExtraArgs may include --settings for sandbox, model overrides, etc.
	args := buildAgentArgs(c.Args, c.Model, req.ExtraArgs)

	command := c.Command
	if command == "" {
		command = "claude"
	}

	var cmd *exec.Cmd
	if req.CmdFactory != nil {
		cmd = req.CmdFactory(ctx, command, args...)
	} else {
		cmd = exec.CommandContext(ctx, command, args...)
	}
	// Set working directory for host execution. When CmdFactory is used (e.g. Docker),
	// the factory already handles workdir via container flags (docker -w), so we
	// must not set cmd.Dir to a container path that doesn't exist on the host.
	if req.WorkDir != "" && cmd.Dir == "" && req.CmdFactory == nil {
		cmd.Dir = req.WorkDir
	}
	cmd.Stdin = strings.NewReader(req.Prompt)
	if cmd.Env == nil {
		cmd.Env = buildProcessEnv(c.Env, req.Env)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		ch <- StreamEvent{Type: "error", Err: err, ExitCode: -1}
		return
	}

	var stderrBuf bytes.Buffer
	stderrWriters := []io.Writer{&stderrBuf}
	if req.StderrFile != "" {
		if ef, err := os.Create(req.StderrFile); err == nil {
			defer ef.Close()
			stderrWriters = append(stderrWriters, ef)
		} else {
			fmt.Fprintf(os.Stderr, "warning: cannot create stderr file %s: %v\n", req.StderrFile, err)
		}
	}
	cmd.Stderr = io.MultiWriter(stderrWriters...)

	if err := cmd.Start(); err != nil {
		ch <- StreamEvent{Type: "error", Err: err, ExitCode: -1}
		return
	}
	ch <- StreamEvent{Type: "system", PID: cmd.Process.Pid}

	// Open stream file for writing raw JSONL
	var streamWriter *bufio.Writer
	if req.StreamFile != "" {
		if sf, err := os.Create(req.StreamFile); err == nil {
			defer sf.Close()
			streamWriter = bufio.NewWriter(sf)
			defer streamWriter.Flush()
		} else {
			fmt.Fprintf(os.Stderr, "warning: cannot create stream file %s: %v\n", req.StreamFile, err)
		}
	}

	startedAt := time.Now()

	scanner := bufio.NewScanner(stdoutPipe)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var lastAssistantText string

	for scanner.Scan() {
		line := scanner.Bytes()

		if streamWriter != nil {
			streamWriter.Write(line)
			streamWriter.WriteByte('\n')
		}

		typ, ev, parseErr := parseClaudeLine(line)
		if parseErr != nil || ev == nil {
			continue
		}

		switch typ {
		case "system":
			sys := ev.(*streamutil.SystemEvent)
			ch <- StreamEvent{Type: "system", SessionID: sys.SessionID}

		case "assistant":
			ast := ev.(*streamutil.AssistantEvent)
			u := ast.Message.Usage
			ctxTokens := u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
			var textParts []string
			for _, block := range ast.Message.Content {
				switch block.Type {
				case "text":
					textParts = append(textParts, block.Text)
				case "tool_use":
					ch <- StreamEvent{
						Type:          "tool_use",
						ToolName:      block.Name,
						ToolInput:     strings.TrimSpace(string(block.Input)),
						ContextTokens: ctxTokens,
					}
				}
			}
			if len(textParts) > 0 {
				text := strings.Join(textParts, "")
				lastAssistantText = text
				ch <- StreamEvent{Type: "assistant", Text: text, ContextTokens: ctxTokens}
			}

		case "tool_result":
			tr := ev.(*streamutil.ToolResultEvent)
			ch <- StreamEvent{Type: "tool_result", ToolResult: tr.Content}

		case "result":
			res := ev.(*streamutil.ResultEvent)
			ch <- StreamEvent{
				Type:             "result",
				Output:           lastAssistantText,
				Cost:             res.TotalCostUSD,
				InputTokens:      res.Usage.InputTokens,
				OutputTokens:     res.Usage.OutputTokens,
				CacheReadTokens:  res.Usage.CacheReadInputTokens,
				CacheWriteTokens: res.Usage.CacheWriteInputTokens,
				Turns:            res.NumTurns,
				DurationMS:       res.DurationMS,
				IsError:          res.IsError,
				ContextWindow:    res.MaxContextWindow(),
			}
		}
	}

	cmdErr := cmd.Wait()
	exitCode := 0
	if cmdErr != nil {
		if exitErr, ok := cmdErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
		// Only emit error if we haven't sent a result event already
		ch <- StreamEvent{
			Type:       "error",
			Err:        cmdErr,
			ExitCode:   exitCode,
			DurationMS: time.Since(startedAt).Milliseconds(),
		}
	}
}

// resolveConfigDir returns the CLAUDE_CONFIG_DIR value from request env (priority) or agent env.
func resolveConfigDir(agentEnv, reqEnv map[string]string) string {
	if v, ok := reqEnv["CLAUDE_CONFIG_DIR"]; ok && v != "" {
		return v
	}
	if v, ok := agentEnv["CLAUDE_CONFIG_DIR"]; ok && v != "" {
		return v
	}
	return ""
}

func hasSettingsArg(args []string) bool {
	for _, a := range args {
		if a == "--settings" {
			return true
		}
	}
	return false
}
