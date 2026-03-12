package agent

import (
	"context"
	"fmt"
)

// Agent executes a prompt and produces normalized stream events.
type Agent interface {
	Name() string
	// Run starts the agent and returns a channel of normalized events.
	// The agent writes raw output to req.StreamFile for archival.
	Run(ctx context.Context, req Request) <-chan StreamEvent
}

// Request holds everything an agent needs to execute.
type Request struct {
	Prompt     string
	WorkDir    string
	StreamFile string            // agent writes raw stream here (agent-native JSONL)
	StderrFile string
	Sandbox    SandboxRules
	ExtraArgs  []string          // from --agent-args
	Env        map[string]string // env vars to set/override
}

// SandboxRules describe filesystem access constraints.
// Agent implementations translate these to their native format.
type SandboxRules struct {
	AllowWriteDirs []string
	DenyWriteDirs  []string
	AllowReadDirs  []string
}

// StreamEvent is the normalized in-memory event type all agents produce.
// Stream files contain raw agent-native JSONL; agents parse their format into these.
type StreamEvent struct {
	Type string // "system", "assistant", "tool_use", "tool_result", "result", "error"

	// system
	SessionID string
	Model     string

	// assistant text
	Text string

	// tool_use
	ToolName  string
	ToolInput string

	// tool_result
	ToolResult string

	// result (final)
	Output          string
	Cost            float64
	InputTokens     int
	OutputTokens    int
	CacheReadTokens int
	Turns           int
	DurationMS      int64
	IsError         bool
	ExitCode        int
	Err             error
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
