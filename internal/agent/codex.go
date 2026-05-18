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

	"github.com/ateam/internal/streamutil"
)

// CodexAgent executes prompts using the OpenAI Codex CLI.
// Invocation: codex exec --json [args...] "prompt"
// All configured args are passed to the `exec` subcommand because most
// codex flags relevant to non-interactive runs (--sandbox, --skip-git-repo-check,
// -c key=value, --model, ...) are exec-scoped. Top-level-only flags like
// --ask-for-approval don't apply: `codex exec` is non-interactive by design.
// The prompt is passed as a positional argument, not stdin.
type CodexAgent struct {
	Command      string            // e.g. "codex"
	Args         []string          // base args passed after `exec`, e.g. ["--sandbox", "workspace-write", "--skip-git-repo-check"]
	Model        string            // optional model override (passed as --model flag)
	Effort       string            // optional reasoning effort (passed as -c model_reasoning_effort=...)
	MaxBudgetUSD string            // stored but not enforced — codex CLI has no native budget cap
	DefaultModel string            // assumed model for pricing when stream doesn't report one
	Pricing      PricingTable      // cost estimation lookup table
	Env          map[string]string // env vars to set (empty string = exclude from parent env)
}

func (c *CodexAgent) Name() string { return NameCodex }

func (c *CodexAgent) ModelName() string {
	if c.Model != "" {
		return c.Model
	}
	return c.DefaultModel
}

func (c *CodexAgent) SetModel(model string) { c.Model = model }

func (c *CodexAgent) SetEffort(effort string) { c.Effort = effort }

func (c *CodexAgent) SetMaxBudgetUSD(value string) { c.MaxBudgetUSD = value }

func (c *CodexAgent) AgentEnv() map[string]string { return c.Env }

func (c *CodexAgent) CloneWithResolvedTemplates(replacer *strings.Replacer) Agent {
	clone := *c
	clone.Args = resolveSlice(c.Args, replacer)
	clone.Env = resolveStringMap(c.Env, replacer)
	clone.Pricing = c.Pricing.Clone()
	return &clone
}

func (c *CodexAgent) DebugCommandArgs(extraArgs []string) (string, []string) {
	command := c.Command
	if command == "" {
		command = "codex"
	}
	args := append([]string{"exec", "--json"}, codexFlagArgs(c.Args, c.Model, c.Effort, extraArgs)...)
	return command, args
}

func (c *CodexAgent) Run(ctx context.Context, req Request) <-chan StreamEvent {
	ch := make(chan StreamEvent, 64)
	go c.run(ctx, req, ch)
	return ch
}

func (c *CodexAgent) run(ctx context.Context, req Request, ch chan<- StreamEvent) {
	defer close(ch)

	// codex exec --json <args> <prompt> — the codex one-shot invocation.
	// All args (base, model, effort, extra) go after the `exec` subcommand
	// because exec-only flags like --skip-git-repo-check reject if placed
	// before the subcommand.
	args := append([]string{"exec", "--json"}, codexFlagArgs(c.Args, c.Model, c.Effort, req.ExtraArgs)...)
	args = append(args, req.Prompt)

	command := c.Command
	if command == "" {
		command = "codex"
	}

	var cmd *exec.Cmd
	if req.CmdFactory != nil {
		cmd = req.CmdFactory(ctx, command, args...)
	} else {
		cmd = exec.CommandContext(ctx, command, args...)
		configureProcessLifecycle(cmd)
	}
	if req.WorkDir != "" && cmd.Dir == "" && req.CmdFactory == nil {
		cmd.Dir = req.WorkDir
	}
	if cmd.Env == nil {
		cmd.Env = buildProcessEnv(c.Env, req.Env)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		ch <- errorEvent(err, ErrorSourceAgentProcess, -1)
		return
	}

	stderrWriters, streamWriter, closers := setupStreamFiles(req)
	for _, c := range closers {
		defer c.Close()
	}
	cmd.Stderr = io.MultiWriter(stderrWriters...)

	if err := cmd.Start(); err != nil {
		ch <- errorEvent(err, ErrorSourceAgentProcess, -1)
		return
	}
	ch <- StreamEvent{Type: "system", PID: cmd.Process.Pid}

	startedAt := time.Now()

	scanner := bufio.NewScanner(stdoutPipe)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var lastText strings.Builder // accumulates agent_message_delta for final output
	var itemText string          // last item.completed text (preferred over deltas)
	var lastStreamErr string

	for scanner.Scan() {
		line := scanner.Bytes()

		if streamWriter != nil {
			streamWriter.Write(line)
			streamWriter.WriteByte('\n')
		}

		typ, ev, parseErr := ParseCodexLine(line)
		if parseErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: skipping malformed codex JSONL line: %v\n", parseErr)
			continue
		}
		if ev == nil {
			continue
		}

		switch typ {
		case "system":
			sys := ev.(*CodexSystemEvent)
			ch <- StreamEvent{Type: "system", SessionID: sys.SessionID}

		case "thinking":
			te := ev.(*CodexTextEvent)
			ch <- StreamEvent{Type: "thinking", Text: te.Text}

		case "tool_use":
			te := ev.(*CodexToolUseEvent)
			ch <- StreamEvent{
				Type:      "tool_use",
				ToolName:  te.ToolName,
				ToolInput: te.ToolInput,
			}

		case "assistant":
			te := ev.(*CodexTextEvent)
			if te.Text != "" {
				lastText.WriteString(te.Text)
				ch <- StreamEvent{Type: "assistant", Text: te.Text}
			}

		case "item_completed":
			te := ev.(*CodexTextEvent)
			if te.Text != "" {
				itemText = te.Text
				// IsModelResponse marks the finalized assistant message for
				// the runner's turn counter (delta-stream "assistant" events
				// above are skipped to avoid over-counting one response).
				ch <- StreamEvent{Type: "assistant", Text: te.Text, IsModelResponse: true}
			}

		case "result":
			re := ev.(*CodexResultEvent)
			output := itemText
			if output == "" {
				output = lastText.String()
			}
			model := c.ModelName()
			if re.Model != "" {
				model = re.Model
			}
			// Turns=1 because `codex exec --json` is single-turn by design;
			// there's no per-turn count to read out of the stream.
			evOut := StreamEvent{
				Type:            "result",
				Output:          output,
				Model:           model,
				Cost:            EstimateCost(c.Pricing, model, c.DefaultModel, re.InputTokens, re.CacheReadTokens, re.OutputTokens),
				InputTokens:     re.InputTokens,
				OutputTokens:    re.OutputTokens,
				CacheReadTokens: re.CacheReadTokens,
				DurationMS:      re.DurationMS,
				Turns:           1,
				IsError:         re.IsError,
			}
			if re.IsError {
				evOut.ErrorSource = ErrorSourceAgentAPI
				evOut.ErrorCause = firstNonEmpty(re.ErrorMessage, lastStreamErr, output)
			}
			ch <- evOut

		case "error":
			ee := ev.(*CodexErrorEvent)
			lastStreamErr = ee.Message
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
		ev := errorEvent(cmdErr, ErrorSourceAgentProcess, exitCode)
		ev.DurationMS = time.Since(startedAt).Milliseconds()
		if lastStreamErr != "" {
			ev.ErrorCause = lastStreamErr
		}
		ch <- ev
	}
}

// CodexSystemEvent represents a thread.started / turn.started event.
// SessionID is set only for thread.started — that's the UUID `codex resume`
// expects.
type CodexSystemEvent struct {
	SessionID string
}

// CodexToolUseEvent represents a tool invocation in Codex JSONL output.
//
// RawJSON is the full original event bytes — used by the verbose formatter
// to show the entire payload (e.g. an apply_patch diff) rather than the
// one-line ToolInput summary. Holds an independent copy of the line bytes
// so it survives the scanner's buffer reuse; callers must not mutate it.
type CodexToolUseEvent struct {
	ToolName  string
	ToolInput string
	RawJSON   json.RawMessage
}

// CodexTextEvent represents an assistant text chunk in Codex JSONL output.
type CodexTextEvent struct {
	Text string
}

// CodexResultEvent represents the final result in Codex JSONL output.
type CodexResultEvent struct {
	Model           string
	InputTokens     int
	OutputTokens    int
	CacheReadTokens int
	DurationMS      int64
	IsError         bool
	// ErrorMessage carries the human-readable reason from a turn.failed
	// event (empty for turn.completed).
	ErrorMessage string
}

// CodexErrorEvent represents an error in Codex JSONL output.
type CodexErrorEvent struct {
	Message string
}

// ParseCodexLine parses a single JSONL line from codex exec --json output.
//
// Recovers from panics inside encoding/json (same defense as
// streamutil.ParseClaudeLine) so a single pathological line can't tear
// down the whole run.
func ParseCodexLine(line []byte) (typ string, ev any, err error) {
	defer func() {
		if r := recover(); r != nil {
			typ = ""
			ev = nil
			err = fmt.Errorf("panic in codex JSONL parser (line len=%d): %v", len(line), r)
		}
	}()

	line = streamutil.TrimBOM(line)
	if len(line) == 0 {
		return "", nil, nil
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(line, &raw); err != nil {
		return "", nil, err
	}

	var eventType string
	if t, ok := raw["type"]; ok {
		_ = json.Unmarshal(t, &eventType)
	}
	if eventType == "" {
		return "", nil, nil
	}

	// toolUse builds a tool_use event with an independent copy of the line
	// (the scanner buffer is reused on the next read).
	toolUse := func(name, input string) (string, any, error) {
		return "tool_use", &CodexToolUseEvent{
			ToolName:  name,
			ToolInput: input,
			RawJSON:   bytes.Clone(line),
		}, nil
	}

	switch eventType {
	case "turn.started":
		return "system", &CodexSystemEvent{}, nil

	case "thread.started":
		var tid string
		if v, ok := raw["thread_id"]; ok {
			_ = json.Unmarshal(v, &tid)
		}
		return "system", &CodexSystemEvent{SessionID: tid}, nil

	case "agent_reasoning_delta":
		// Deltas drive the stall-watchdog heartbeat. The matching final
		// agent_reasoning event has the same concatenated text — dropped
		// below to avoid double-emit on replay and duplicate progress live.
		text := firstStringField(raw, "delta", "text", "reasoning")
		if text == "" {
			return "", nil, nil
		}
		return "thinking", &CodexTextEvent{Text: text}, nil

	case "agent_reasoning":
		return "", nil, nil

	case "item.started":
		return parseCodexItemStarted(raw, toolUse)

	case "item.completed":
		return parseCodexItemCompleted(raw)

	case "command_execution":
		// Standalone command_execution events (output/completion updates).
		// Not a new tool call — skip.
		return "", nil, nil

	case "agent_message_delta":
		var delta string
		if d, ok := raw["delta"]; ok {
			_ = json.Unmarshal(d, &delta)
		}
		return "assistant", &CodexTextEvent{Text: delta}, nil

	case "agent_message", "assistant_message":
		text := codexMessageText(raw)
		return "assistant", &CodexTextEvent{Text: text}, nil

	case "turn.completed":
		return "result", parseCodexResult(raw, false), nil

	case "turn.failed":
		return "result", parseCodexResult(raw, true), nil

	case "error":
		var msg string
		if m, ok := raw["message"]; ok {
			_ = json.Unmarshal(m, &msg)
		}
		if msg == "" {
			msg = "unknown error"
		}
		return "error", &CodexErrorEvent{Message: msg}, nil

	default:
		// Any *_begin event is a tool call — forward-compatible with
		// future codex tool types (covers exec_command, web_search,
		// mcp_tool_call, custom_tool_call, patch_apply, apply_patch).
		if strings.HasSuffix(eventType, "_begin") {
			return toolUse(strings.TrimSuffix(eventType, "_begin"), codexToolDetail(raw))
		}
		return "", nil, nil
	}
}

// parseCodexItemStarted handles item.started events. The item.type determines
// whether this is a tool call (command_execution) or something else.
// toolUse builds the resulting event with the original line bytes attached.
func parseCodexItemStarted(raw map[string]json.RawMessage, toolUse func(name, input string) (string, any, error)) (string, any, error) {
	itemRaw, ok := raw["item"]
	if !ok {
		return "", nil, nil
	}
	var item struct {
		Type    string `json:"type"`
		Command string `json:"command"`
		Name    string `json:"name"`
		Query   string `json:"query"`
	}
	if err := json.Unmarshal(itemRaw, &item); err != nil {
		return "", nil, nil
	}
	switch item.Type {
	case "command_execution":
		return toolUse("command_execution", item.Command)
	case "web_search":
		return toolUse("web_search", item.Query)
	case "mcp_tool_call", "custom_tool_call", "patch_apply", "apply_patch":
		detail := item.Name
		if detail == "" {
			detail = item.Command
		}
		return toolUse(item.Type, detail)
	}
	return "", nil, nil
}

// parseCodexItemCompleted handles item.completed events. These carry the final
// text of agent messages and tool completion data.
func parseCodexItemCompleted(raw map[string]json.RawMessage) (string, any, error) {
	text := codexItemCompletedText(raw)
	if text != "" {
		return "item_completed", &CodexTextEvent{Text: text}, nil
	}
	return "", nil, nil
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
	return firstStringField(raw, "delta", "text", "message", "content")
}

// firstStringField returns the first key's string value that unmarshals
// successfully and is non-empty.
func firstStringField(raw map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
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

// codexErrorMessage pulls the human-readable error text out of a turn.failed
// event. Codex nests it as either error.message (object) or error_message (flat).
func codexErrorMessage(raw map[string]json.RawMessage) string {
	if v, ok := raw["error"]; ok {
		var flat string
		if err := json.Unmarshal(v, &flat); err == nil && flat != "" {
			return flat
		}
		var nested struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		}
		if err := json.Unmarshal(v, &nested); err == nil {
			if nested.Message != "" {
				return nested.Message
			}
			if nested.Type != "" {
				return nested.Type
			}
		}
	}
	if v, ok := raw["error_message"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err == nil && s != "" {
			return s
		}
	}
	return ""
}

func parseCodexResult(raw map[string]json.RawMessage, isError bool) *CodexResultEvent {
	re := &CodexResultEvent{IsError: isError}

	if v, ok := raw["model"]; ok {
		_ = json.Unmarshal(v, &re.Model)
	}

	if isError {
		re.ErrorMessage = codexErrorMessage(raw)
	}

	// duration_ms or durationMs
	if v, ok := raw["duration_ms"]; ok {
		_ = json.Unmarshal(v, &re.DurationMS)
	} else if v, ok := raw["durationMs"]; ok {
		_ = json.Unmarshal(v, &re.DurationMS)
	}

	// usage.input_tokens, usage.output_tokens, usage.cached_input_tokens
	// (with camelCase fallbacks).
	if v, ok := raw["usage"]; ok {
		var usage struct {
			InputTokens       int `json:"input_tokens"`
			OutputTokens      int `json:"output_tokens"`
			CachedInputTokens int `json:"cached_input_tokens"`
			// camelCase fallbacks
			InputTokensCC       int `json:"inputTokens"`
			OutputTokensCC      int `json:"outputTokens"`
			CachedInputTokensCC int `json:"cachedInputTokens"`
		}
		if err := json.Unmarshal(v, &usage); err == nil {
			re.InputTokens = usage.InputTokens
			re.OutputTokens = usage.OutputTokens
			re.CacheReadTokens = usage.CachedInputTokens
			if re.InputTokens == 0 {
				re.InputTokens = usage.InputTokensCC
			}
			if re.OutputTokens == 0 {
				re.OutputTokens = usage.OutputTokensCC
			}
			if re.CacheReadTokens == 0 {
				re.CacheReadTokens = usage.CachedInputTokensCC
			}
		}
	}

	return re
}
