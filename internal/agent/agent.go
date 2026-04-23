// Package agent defines the Agent interface for executing prompts and producing
// normalized stream events, along with concrete implementations for supported backends.
package agent

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/ateam/internal/container"
)

// Agent executes a prompt and produces normalized stream events.
//
// Concurrency (see CONCURRENCY.md):
//
//   - The Agent on a Runner is effectively read-only once dispatched to a
//     pool; the pool calls CloneWithResolvedTemplates per task.
//   - Run is invoked on the clone, not on the shared original, so any per-
//     run mutation stays local to that goroutine.
type Agent interface {
	Name() string
	// Run starts the agent and returns a channel of normalized events.
	// The agent writes raw output to req.StreamFile for archival.
	Run(ctx context.Context, req Request) <-chan StreamEvent
	// DebugCommandArgs returns the full command and args the agent would execute,
	// including extraArgs. Used for verbose/diagnostic output.
	DebugCommandArgs(extraArgs []string) (command string, args []string)
	// SetModel overrides the model the agent will use.
	// MUTATES — call before the Agent is shared with a pool.
	SetModel(model string)
	// CloneWithResolvedTemplates returns a clone with {{VAR}} placeholders
	// resolved in Args, Env, and other templated string fields.
	// Implementations MUST ensure the returned value's Args and Env share
	// no backing slice/map with the original — the pool relies on this for
	// per-task isolation.
	CloneWithResolvedTemplates(replacer *strings.Replacer) Agent
}

// ModelProvider is optionally implemented by agents that expose their model name.
type ModelProvider interface {
	ModelName() string
}

// Error source values for StreamEvent.ErrorSource / RunSummary.ErrorSource.
// Kept as exported constants so callers don't duplicate string literals.
const (
	ErrorSourceAgentAPI      = "agent_api"      // agent CLI reported is_error=true (e.g. Anthropic/OpenAI API error)
	ErrorSourceAgentProcess  = "agent_process"  // agent subprocess exited non-zero without a result event (crash, OOM, ...)
	ErrorSourceAteamTimeout  = "ateam_timeout"  // ateam killed the run via context deadline
	ErrorSourceAteamInternal = "ateam_internal" // ateam side failure (no result event, not a timeout)
)

// errorEvent builds a populated StreamEvent of type "error" carrying err.
// exitCode is typically -1 for setup failures and the process's real exit
// code once the subprocess has run.
func errorEvent(err error, source string, exitCode int) StreamEvent {
	return StreamEvent{
		Type:        "error",
		Err:         err,
		ExitCode:    exitCode,
		ErrorSource: source,
		ErrorCause:  err.Error(),
	}
}

// Request holds everything an agent needs to execute.
type Request struct {
	Prompt     string
	WorkDir    string
	StreamFile string // agent writes raw stream here (agent-native JSONL)
	StderrFile string
	ExtraArgs  []string             // from --agent-args
	Env        map[string]string    // env vars to set/override
	CmdFactory container.CmdFactory // if set, agent uses this to create subprocesses instead of exec.CommandContext
}

// StreamEvent is the normalized in-memory event type all agents produce.
// Stream files contain raw agent-native JSONL; agents parse their format into these.
type StreamEvent struct {
	Type string // "system", "assistant", "tool_use", "tool_result", "result", "error"

	// system
	SessionID string
	Model     string
	PID       int

	// assistant text
	Text string

	// tool_use
	ToolName  string
	ToolInput string

	// tool_result
	ToolResult string

	// context size (from per-turn usage)
	ContextTokens int
	ContextWindow int

	// result (final)
	Output           string
	Cost             float64
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
	Turns            int
	DurationMS       int64
	IsError          bool
	ExitCode         int
	Err              error

	// ErrorCause is a human-readable description of why the run failed
	// (e.g. "API Error: Stream idle timeout - partial response received").
	// Populated only on failure paths.
	ErrorCause string
	// ErrorSource classifies the origin of the failure. One of
	// "agent_api", "agent_process", "ateam_timeout", "ateam_internal".
	// Populated only on failure paths.
	ErrorSource string
}

// resolveSlice replaces {{VAR}} placeholders in each string element.
func resolveSlice(ss []string, r *strings.Replacer) []string {
	if ss == nil {
		return nil
	}
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = r.Replace(s)
	}
	return out
}

// resolveStringMap replaces {{VAR}} placeholders in map values. Keys are not resolved.
func resolveStringMap(m map[string]string, r *strings.Replacer) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = r.Replace(v)
	}
	return out
}

// buildAgentArgs copies base args, appends --model if non-empty, then appends extra.
func buildAgentArgs(base []string, model string, extra []string) []string {
	args := make([]string, len(base))
	copy(args, base)
	if model != "" {
		args = append(args, "--model", model)
	}
	return append(args, extra...)
}

// buildProcessEnv constructs the process environment for an agent.
// Keys with empty values in agentEnv are excluded from the parent process env.
// Non-empty agentEnv values are added. reqEnv overrides everything.
func buildProcessEnv(agentEnv, reqEnv map[string]string) []string {
	var excludeKeys []string
	for k, v := range agentEnv {
		if v == "" {
			excludeKeys = append(excludeKeys, k)
		}
	}

	env := filterEnv(os.Environ(), excludeKeys...)
	env = upsertEnv(env, agentEnv, true)
	env = upsertEnv(env, reqEnv, false)
	return env
}

func upsertEnv(env []string, updates map[string]string, skipEmpty bool) []string {
	if len(updates) == 0 {
		return env
	}

	var keys []string
	for k, v := range updates {
		if skipEmpty && v == "" {
			continue
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return env
	}

	env = filterEnv(env, keys...)
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+updates[k])
	}
	return env
}

func filterEnv(env []string, exclude ...string) []string {
	excludeSet := make(map[string]bool, len(exclude))
	for _, e := range exclude {
		excludeSet[e] = true
	}
	var result []string
	for _, e := range env {
		if k, _, ok := strings.Cut(e, "="); ok && excludeSet[k] {
			continue
		}
		result = append(result, e)
	}
	return result
}

// Result drains a stream of events and returns the final result.
func Result(events <-chan StreamEvent) StreamEvent {
	var last StreamEvent
	var lastText string

	for ev := range events {
		switch ev.Type {
		case "assistant":
			if ev.Text != "" {
				lastText = ev.Text
			}
		case "result", "error":
			last = ev
		}
	}

	if last.Type == "result" && last.Output == "" {
		last.Output = lastText
	}
	if last.Type == "" {
		last.Type = "error"
		last.Output = lastText
		if last.Err == nil {
			last.Err = fmt.Errorf("agent produced no result event")
		}
	}

	return last
}
