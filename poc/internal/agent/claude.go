package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ClaudeAgent executes prompts using the Claude CLI.
type ClaudeAgent struct {
	Command string            // e.g. "claude"
	Args    []string          // base args from config, e.g. ["-p", "--output-format", "stream-json", "--verbose"]
	Model   string            // optional model override
	Env     map[string]string // env vars to set (empty string = exclude from parent env)
}

func (c *ClaudeAgent) Name() string { return "claude" }

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

	args := make([]string, len(c.Args))
	copy(args, c.Args)

	if c.Model != "" {
		args = append(args, "--model", c.Model)
	}

	// ExtraArgs may include --settings for sandbox, model overrides, etc.
	args = append(args, req.ExtraArgs...)

	command := c.Command
	if command == "" {
		command = "claude"
	}

	cmd := exec.CommandContext(ctx, command, args...)
	if req.WorkDir != "" {
		cmd.Dir = req.WorkDir
	}
	cmd.Stdin = strings.NewReader(req.Prompt)
	cmd.Env = buildProcessEnv(c.Env, req.Env)

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
		}
	}
	cmd.Stderr = io.MultiWriter(stderrWriters...)

	if err := cmd.Start(); err != nil {
		ch <- StreamEvent{Type: "error", Err: err, ExitCode: -1}
		return
	}

	// Open stream file for writing raw JSONL
	var streamWriter *bufio.Writer
	if req.StreamFile != "" {
		if sf, err := os.Create(req.StreamFile); err == nil {
			defer sf.Close()
			streamWriter = bufio.NewWriter(sf)
			defer streamWriter.Flush()
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
			sys := ev.(*claudeSystemEvent)
			ch <- StreamEvent{Type: "system", SessionID: sys.SessionID}

		case "assistant":
			ast := ev.(*claudeAssistantEvent)
			// Emit text blocks
			var textParts []string
			for _, block := range ast.Message.Content {
				switch block.Type {
				case "text":
					textParts = append(textParts, block.Text)
				case "tool_use":
					ch <- StreamEvent{
						Type:      "tool_use",
						ToolName:  block.Name,
						ToolInput: strings.TrimSpace(string(block.Input)),
					}
				}
			}
			if len(textParts) > 0 {
				text := strings.Join(textParts, "")
				lastAssistantText = text
				ch <- StreamEvent{Type: "assistant", Text: text}
			}

		case "tool_result":
			tr := ev.(*claudeToolResultEvent)
			ch <- StreamEvent{Type: "tool_result", ToolResult: tr.Content}

		case "result":
			res := ev.(*claudeResultEvent)
			ch <- StreamEvent{
				Type:            "result",
				Output:          lastAssistantText,
				Cost:            res.TotalCostUSD,
				InputTokens:     res.Usage.InputTokens,
				OutputTokens:    res.Usage.OutputTokens,
				CacheReadTokens: res.Usage.CacheReadInputTokens,
				Turns:           res.NumTurns,
				DurationMS:      res.DurationMS,
				IsError:         res.IsError,
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

// claude-native JSONL event types

type claudeTypedEvent struct {
	Type string `json:"type"`
}

type claudeSystemEvent struct {
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`
}

type claudeAssistantEvent struct {
	Message struct {
		Content []claudeContentBlock `json:"content"`
	} `json:"message"`
}

type claudeContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type claudeToolResultEvent struct {
	Content string `json:"content"`
}

type claudeResultEvent struct {
	TotalCostUSD float64 `json:"total_cost_usd"`
	CostUSD      float64 `json:"cost_usd"`
	DurationMS   int64   `json:"duration_ms"`
	NumTurns     int     `json:"num_turns"`
	IsError      bool    `json:"is_error"`
	Usage        struct {
		InputTokens          int `json:"input_tokens"`
		OutputTokens         int `json:"output_tokens"`
		CacheReadInputTokens int `json:"cache_read_input_tokens"`
	} `json:"usage"`
}

// parseClaudeLine parses a single JSONL line from claude's stream-json output.
func parseClaudeLine(line []byte) (string, any, error) {
	line = trimBOM(line)
	if len(line) == 0 {
		return "", nil, nil
	}

	var typed claudeTypedEvent
	if err := json.Unmarshal(line, &typed); err != nil {
		return "", nil, err
	}

	switch typed.Type {
	case "system":
		var ev claudeSystemEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			return typed.Type, nil, err
		}
		return typed.Type, &ev, nil
	case "assistant":
		var ev claudeAssistantEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			return typed.Type, nil, err
		}
		return typed.Type, &ev, nil
	case "tool_result":
		var ev claudeToolResultEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			return typed.Type, nil, err
		}
		return typed.Type, &ev, nil
	case "result":
		var ev claudeResultEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			return typed.Type, nil, err
		}
		return typed.Type, &ev, nil
	default:
		return "", nil, nil
	}
}

func trimBOM(b []byte) []byte {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return b[3:]
	}
	return b
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

