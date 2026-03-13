package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// CodexAgent executes prompts using the OpenAI Codex CLI.
// Invocation: codex [args...] exec --json "prompt"
// The prompt is passed as a positional argument, not stdin.
type CodexAgent struct {
	Command string            // e.g. "codex"
	Args    []string          // base args, e.g. ["--sandbox", "workspace-write", "--ask-for-approval", "never"]
	Model   string            // optional model override
	Env     map[string]string // env vars to set (empty string = exclude from parent env)
}

func (c *CodexAgent) Name() string { return "codex" }

func (c *CodexAgent) DebugCommandArgs(extraArgs []string) (string, []string) {
	command := c.Command
	if command == "" {
		command = "codex"
	}
	args := make([]string, len(c.Args))
	copy(args, c.Args)
	if c.Model != "" {
		args = append(args, "--model", c.Model)
	}
	args = append(args, extraArgs...)
	args = append(args, "exec", "--json")
	return command, args
}

func (c *CodexAgent) Run(ctx context.Context, req Request) <-chan StreamEvent {
	ch := make(chan StreamEvent, 64)
	go c.run(ctx, req, ch)
	return ch
}

func (c *CodexAgent) run(ctx context.Context, req Request, ch chan<- StreamEvent) {
	defer close(ch)

	args := make([]string, len(c.Args))
	copy(args, c.Args)

	if c.Model != "" {
		args = append(args, "--model", c.Model)
	}

	// ExtraArgs before the exec subcommand
	args = append(args, req.ExtraArgs...)

	// exec --json <prompt> — the codex one-shot invocation
	args = append(args, "exec", "--json", req.Prompt)

	command := c.Command
	if command == "" {
		command = "codex"
	}

	var cmd *exec.Cmd
	if req.CmdFactory != nil {
		cmd = req.CmdFactory(ctx, command, args...)
	} else {
		cmd = exec.CommandContext(ctx, command, args...)
	}
	if req.WorkDir != "" && cmd.Dir == "" && req.CmdFactory == nil {
		cmd.Dir = req.WorkDir
	}
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
		}
	}
	cmd.Stderr = io.MultiWriter(stderrWriters...)

	if err := cmd.Start(); err != nil {
		ch <- StreamEvent{Type: "error", Err: err, ExitCode: -1}
		return
	}

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

	var lastText strings.Builder // accumulates agent_message_delta for final output
	var itemText string          // last item.completed text (preferred over deltas)

	for scanner.Scan() {
		line := scanner.Bytes()

		if streamWriter != nil {
			streamWriter.Write(line)
			streamWriter.WriteByte('\n')
		}

		typ, ev, parseErr := parseCodexLine(line)
		if parseErr != nil || ev == nil {
			continue
		}

		switch typ {
		case "system":
			ch <- StreamEvent{Type: "system"}

		case "tool_use":
			te := ev.(*codexToolUseEvent)
			ch <- StreamEvent{
				Type:      "tool_use",
				ToolName:  te.ToolName,
				ToolInput: te.ToolInput,
			}

		case "assistant":
			te := ev.(*codexTextEvent)
			if te.Text != "" {
				lastText.WriteString(te.Text)
				ch <- StreamEvent{Type: "assistant", Text: te.Text}
			}

		case "item_completed":
			te := ev.(*codexTextEvent)
			if te.Text != "" {
				itemText = te.Text
				ch <- StreamEvent{Type: "assistant", Text: te.Text}
			}

		case "result":
			re := ev.(*codexResultEvent)
			output := itemText
			if output == "" {
				output = lastText.String()
			}
			ch <- StreamEvent{
				Type:         "result",
				Output:       output,
				InputTokens:  re.InputTokens,
				OutputTokens: re.OutputTokens,
				DurationMS:   re.DurationMS,
				Turns:        1,
				IsError:      re.IsError,
			}

		case "error":
			ee := ev.(*codexErrorEvent)
			ch <- StreamEvent{
				Type: "assistant",
				Text: "error: " + ee.Message,
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
		ch <- StreamEvent{
			Type:       "error",
			Err:        cmdErr,
			ExitCode:   exitCode,
			DurationMS: time.Since(startedAt).Milliseconds(),
		}
	}
}

// codex event types for JSONL parsing

type codexToolUseEvent struct {
	ToolName  string
	ToolInput string
}

type codexTextEvent struct {
	Text string
}

type codexResultEvent struct {
	InputTokens  int
	OutputTokens int
	DurationMS   int64
	IsError      bool
}

type codexErrorEvent struct {
	Message string
}

// parseCodexLine parses a single JSONL line from codex exec --json output.
func parseCodexLine(line []byte) (string, any, error) {
	line = trimBOM(line)
	if len(line) == 0 {
		return "", nil, nil
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(line, &raw); err != nil {
		return "", nil, err
	}

	var eventType string
	if t, ok := raw["type"]; ok {
		json.Unmarshal(t, &eventType)
	}
	if eventType == "" {
		return "", nil, nil
	}

	switch eventType {
	case "turn.started", "thread.started":
		return "system", &struct{}{}, nil

	case "exec_command_begin", "web_search_begin", "mcp_tool_call_begin",
		"custom_tool_call_begin", "patch_apply_begin", "apply_patch_begin":

		toolName := strings.TrimSuffix(eventType, "_begin")
		toolInput := codexToolDetail(raw)
		return "tool_use", &codexToolUseEvent{
			ToolName:  toolName,
			ToolInput: toolInput,
		}, nil

	case "agent_message_delta":
		var delta string
		if d, ok := raw["delta"]; ok {
			json.Unmarshal(d, &delta)
		}
		return "assistant", &codexTextEvent{Text: delta}, nil

	case "agent_message", "assistant_message":
		text := codexMessageText(raw)
		return "assistant", &codexTextEvent{Text: text}, nil

	case "item.completed":
		text := codexItemCompletedText(raw)
		if text != "" {
			return "item_completed", &codexTextEvent{Text: text}, nil
		}
		return "", nil, nil

	case "turn.completed":
		return "result", parseCodexResult(raw, false), nil

	case "turn.failed":
		return "result", parseCodexResult(raw, true), nil

	case "error":
		var msg string
		if m, ok := raw["message"]; ok {
			json.Unmarshal(m, &msg)
		}
		if msg == "" {
			msg = "unknown error"
		}
		return "error", &codexErrorEvent{Message: msg}, nil

	default:
		return "", nil, nil
	}
}

// codexToolDetail extracts .command // .query // .tool_name // .name from a tool event.
func codexToolDetail(raw map[string]json.RawMessage) string {
	for _, key := range []string{"command", "query", "tool_name", "name"} {
		v, ok := raw[key]
		if !ok {
			continue
		}

		// Could be a string or an array of strings
		var s string
		if err := json.Unmarshal(v, &s); err == nil {
			return s
		}

		var arr []string
		if err := json.Unmarshal(v, &arr); err == nil {
			return strings.Join(arr, " ")
		}
	}
	return ""
}

// codexMessageText extracts text from agent_message / assistant_message events.
// Tries .delta, .text, .message, .content in order.
func codexMessageText(raw map[string]json.RawMessage) string {
	for _, key := range []string{"delta", "text", "message", "content"} {
		v, ok := raw[key]
		if !ok {
			continue
		}
		var s string
		if err := json.Unmarshal(v, &s); err == nil && s != "" {
			return s
		}
	}
	return ""
}

// codexItemCompletedText extracts the final response text from an item.completed event.
// Structure: .item.text or .item.content[].text
func codexItemCompletedText(raw map[string]json.RawMessage) string {
	itemRaw, ok := raw["item"]
	if !ok {
		return ""
	}

	var item struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(itemRaw, &item); err != nil {
		return ""
	}

	if item.Type != "agent_message" && item.Type != "assistant_message" {
		return ""
	}

	if item.Text != "" {
		return item.Text
	}

	var parts []string
	for _, c := range item.Content {
		if c.Text != "" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// parseCodexResult extracts tokens and duration from turn.completed / turn.failed.
func parseCodexResult(raw map[string]json.RawMessage, isError bool) *codexResultEvent {
	re := &codexResultEvent{IsError: isError}

	// duration_ms or durationMs
	if v, ok := raw["duration_ms"]; ok {
		json.Unmarshal(v, &re.DurationMS)
	} else if v, ok := raw["durationMs"]; ok {
		json.Unmarshal(v, &re.DurationMS)
	}

	// usage.input_tokens, usage.output_tokens (with camelCase fallbacks)
	if v, ok := raw["usage"]; ok {
		var usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			// camelCase fallbacks
			InputTokensCC  int `json:"inputTokens"`
			OutputTokensCC int `json:"outputTokens"`
		}
		if err := json.Unmarshal(v, &usage); err == nil {
			re.InputTokens = usage.InputTokens
			re.OutputTokens = usage.OutputTokens
			if re.InputTokens == 0 {
				re.InputTokens = usage.InputTokensCC
			}
			if re.OutputTokens == 0 {
				re.OutputTokens = usage.OutputTokensCC
			}
		}
	}

	return re
}
