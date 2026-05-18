package agent

import (
	"bufio"
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
	Effort       string            // optional reasoning effort (passed as --effort flag)
	MaxBudgetUSD string            // optional spend cap (passed as --max-budget-usd flag)
	DefaultModel string            // assumed model for pricing when stream doesn't report one
	Pricing      PricingTable      // cost estimation lookup table (used to estimate cost when no result event arrives)
	Env          map[string]string // env vars to set (empty string = exclude from parent env)
}

func (c *ClaudeAgent) Name() string { return NameClaude }

func (c *ClaudeAgent) ModelName() string {
	if c.Model != "" {
		return c.Model
	}
	return c.DefaultModel
}

func (c *ClaudeAgent) SetModel(model string) { c.Model = model }

func (c *ClaudeAgent) SetEffort(effort string) { c.Effort = effort }

func (c *ClaudeAgent) SetMaxBudgetUSD(value string) { c.MaxBudgetUSD = value }

func (c *ClaudeAgent) AgentEnv() map[string]string { return c.Env }

func (c *ClaudeAgent) CloneWithResolvedTemplates(replacer *strings.Replacer) Agent {
	clone := *c
	clone.Args = resolveSlice(c.Args, replacer)
	clone.Env = resolveStringMap(c.Env, replacer)
	clone.Pricing = c.Pricing.Clone()
	return &clone
}

func (c *ClaudeAgent) DebugCommandArgs(extraArgs []string) (string, []string) {
	command := c.Command
	if command == "" {
		command = "claude"
	}
	return command, claudeArgs(c.Args, c.Model, c.Effort, c.MaxBudgetUSD, extraArgs)
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
			ch <- errorEvent(fmt.Errorf("cannot create config dir %s: %w", configDir, err), ErrorSourceAteamInternal, -1)
			return
		}
		if !hasSettingsArg(req.ExtraArgs) {
			ch <- errorEvent(fmt.Errorf("CLAUDE_CONFIG_DIR is set (%s) but no --settings specified; isolated claude requires sandbox settings", configDir), ErrorSourceAteamInternal, -1)
			return
		}
	}

	// ExtraArgs may include --settings for sandbox, model overrides, etc.
	args := claudeArgs(c.Args, c.Model, c.Effort, c.MaxBudgetUSD, req.ExtraArgs)

	command := c.Command
	if command == "" {
		command = "claude"
	}

	var cmd *exec.Cmd
	if req.CmdFactory != nil {
		cmd = req.CmdFactory(ctx, command, args...)
	} else {
		cmd = exec.CommandContext(ctx, command, args...)
		configureProcessLifecycle(cmd)
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

	var lastAssistantText string
	var (
		inputTokens, outputTokens          int
		cacheReadTokens, cacheCreateTokens int
		resolvedModel                      string
	)
	// estimate returns the running cumulative fields to attach to any
	// StreamEvent emitted from inside the scan loop or after cmd.Wait().
	estimate := func() StreamEvent {
		return StreamEvent{
			Model:            firstNonEmpty(resolvedModel, c.ModelName()),
			InputTokens:      inputTokens,
			OutputTokens:     outputTokens,
			CacheReadTokens:  cacheReadTokens,
			CacheWriteTokens: cacheCreateTokens,
			Cost: EstimateCost(c.Pricing,
				firstNonEmpty(resolvedModel, c.ModelName()),
				c.DefaultModel, inputTokens, cacheReadTokens, outputTokens),
		}
	}

	for scanner.Scan() {
		line := scanner.Bytes()

		if streamWriter != nil {
			streamWriter.Write(line)
			streamWriter.WriteByte('\n')
		}

		typ, ev, parseErr := parseClaudeLine(line)
		if parseErr != nil {
			// parseClaudeLine recovers panics from encoding/json and
			// surfaces them as errors — never silently swallow them so
			// an operator can tell a run produced garbage lines. The
			// live renderer's std-stream redirect (cmd/std_redirect.go)
			// captures stderr and interleaves it above the bars during
			// pool runs, so this write is safe even mid-frame.
			fmt.Fprintf(os.Stderr, "Warning: skipping malformed claude JSONL line: %v\n", parseErr)
			continue
		}
		if ev == nil {
			continue
		}

		switch typ {
		case "system":
			sys := ev.(*streamutil.SystemEvent)
			if sys.Model != "" {
				resolvedModel = sys.Model
			}
			ch <- StreamEvent{Type: "system", SessionID: sys.SessionID, Model: sys.Model}

		case "assistant":
			ast := ev.(*streamutil.AssistantEvent)
			u := ast.Message.Usage
			ctxTokens := u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
			inputTokens += u.InputTokens
			outputTokens += u.OutputTokens
			cacheReadTokens += u.CacheReadInputTokens
			cacheCreateTokens += u.CacheCreationInputTokens
			cum := estimate()
			var textParts []string
			// firstEmission carries IsModelResponse so the runner can count
			// one turn per assistant message regardless of block layout.
			firstEmission := true
			markFirst := func(ev *StreamEvent) {
				if firstEmission {
					ev.IsModelResponse = true
					firstEmission = false
				}
			}
			for _, block := range ast.Message.Content {
				switch block.Type {
				case "text":
					textParts = append(textParts, block.Text)
				case "thinking":
					// Emit a dedicated "thinking" event so downstream consumers
					// (progress UI, stall detection) get a heartbeat during
					// extended-thinking turns where the model reasons silently
					// for minutes between tool calls. The runner treats this
					// event distinctly from "assistant" so thinking content is
					// not promoted into the run's Output field.
					if block.Thinking != "" {
						thinkEv := cum
						thinkEv.Type = "thinking"
						thinkEv.Text = block.Thinking
						thinkEv.ContextTokens = ctxTokens
						markFirst(&thinkEv)
						ch <- thinkEv
					}
				case "tool_use":
					toolEv := cum
					toolEv.Type = "tool_use"
					toolEv.ToolName = block.Name
					toolEv.ToolInput = strings.TrimSpace(string(block.Input))
					toolEv.ContextTokens = ctxTokens
					markFirst(&toolEv)
					ch <- toolEv
				}
			}
			if len(textParts) > 0 {
				text := strings.Join(textParts, "")
				lastAssistantText = text
				textEv := cum
				textEv.Type = "assistant"
				textEv.Text = text
				textEv.ContextTokens = ctxTokens
				markFirst(&textEv)
				ch <- textEv
			}

		case "tool_result":
			tr := ev.(*streamutil.ToolResultEvent)
			ch <- StreamEvent{Type: "tool_result", ToolResult: tr.Content.String()}

		case "result":
			res := ev.(*streamutil.ResultEvent)
			evOut := StreamEvent{
				Type:             "result",
				Model:            firstNonEmpty(resolvedModel, c.ModelName()),
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
			if res.IsError {
				evOut.ErrorSource = ErrorSourceAgentAPI
				evOut.ErrorCause = firstNonEmpty(res.Result, res.Subtype, lastAssistantText)
			}
			ch <- evOut
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
		// Only emit error if we haven't sent a result event already.
		// Attach the running token totals + estimated cost so the DB
		// row reflects partial usage instead of zeroes.
		ev := errorEvent(cmdErr, ErrorSourceAgentProcess, exitCode)
		ev.DurationMS = time.Since(startedAt).Milliseconds()
		cum := estimate()
		ev.Model = cum.Model
		ev.InputTokens = cum.InputTokens
		ev.OutputTokens = cum.OutputTokens
		ev.CacheReadTokens = cum.CacheReadTokens
		ev.CacheWriteTokens = cum.CacheWriteTokens
		ev.Cost = cum.Cost
		ch <- ev
	}
}

// firstNonEmpty returns the first non-empty string from ss.
func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
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
