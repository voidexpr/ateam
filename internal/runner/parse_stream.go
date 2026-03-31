package runner

import (
	"encoding/json"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/streamutil"
)

// DisplayEvent is the normalized display type all stream parsers produce.
// The formatter switches on these types — one code path, no per-agent branching.
type DisplayEvent interface{ displayEvent() }

type SystemLine struct {
	SessionID string
	Model     string
	Version   string
	Cwd       string
}

type UserLine struct{}

type ToolCallLine struct {
	Name   string
	Detail string
	Claude *ToolCallClaudeExt
}

type ToolCallClaudeExt struct {
	Input json.RawMessage
}

type TextLine struct {
	Text string
}

type ThinkingLine struct {
	Text string
}

type ToolResultLine struct {
	Content string
}

type ResultLine struct {
	Cost            float64
	DurationMS      int64
	Turns           int
	InputTokens     int
	OutputTokens    int
	CacheReadTokens int
	IsError         bool
}

type ErrorLine struct {
	Message string
}

func (*SystemLine) displayEvent()     {}
func (*UserLine) displayEvent()       {}
func (*ToolCallLine) displayEvent()   {}
func (*TextLine) displayEvent()       {}
func (*ThinkingLine) displayEvent()   {}
func (*ToolResultLine) displayEvent() {}
func (*ResultLine) displayEvent()     {}
func (*ErrorLine) displayEvent()      {}

// streamFormat identifies the JSONL format.
type streamFormat int

const (
	formatUnknown streamFormat = iota
	formatClaude
	formatCodex
)

// parseDisplayLine parses any JSONL line into normalized DisplayEvents.
// hint is sticky — once detected, pass it for subsequent lines.
func parseDisplayLine(line []byte, hint streamFormat) ([]DisplayEvent, streamFormat, error) {
	line = streamutil.TrimBOM(line)
	if len(line) == 0 {
		return nil, hint, nil
	}

	if hint == formatUnknown {
		hint = detectFormat(line)
	}

	switch hint {
	case formatClaude:
		evs, err := parseClaudeDisplay(line)
		return evs, hint, err
	case formatCodex:
		evs, err := parseCodexDisplay(line)
		return evs, hint, err
	default:
		// Try Claude first, fall back to Codex
		if evs, err := parseClaudeDisplay(line); len(evs) > 0 && err == nil {
			return evs, formatClaude, nil
		}
		if evs, err := parseCodexDisplay(line); len(evs) > 0 && err == nil {
			return evs, formatCodex, nil
		}
		return nil, hint, nil
	}
}

// detectFormat peeks at the JSON to determine the stream format.
func detectFormat(line []byte) streamFormat {
	var peek struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &peek); err != nil {
		return formatUnknown
	}
	switch peek.Type {
	case "system", "assistant", "user", "tool_result", "result":
		return formatClaude
	case "turn.started", "thread.started",
		"exec_command_begin", "web_search_begin", "mcp_tool_call_begin",
		"custom_tool_call_begin", "patch_apply_begin", "apply_patch_begin",
		"agent_message_delta", "agent_message", "assistant_message",
		"item.completed", "turn.completed", "turn.failed":
		return formatCodex
	default:
		return formatUnknown
	}
}

func parseClaudeDisplay(line []byte) ([]DisplayEvent, error) {
	typ, ev, err := parseStreamLine(line)
	if err != nil || ev == nil {
		return nil, err
	}

	switch typ {
	case "system":
		sys := ev.(*systemEvent)
		if sys.Subtype != "init" {
			return nil, nil
		}
		return []DisplayEvent{&SystemLine{
			SessionID: sys.SessionID,
			Model:     sys.Model,
			Version:   sys.ClaudeCodeVersion,
			Cwd:       sys.Cwd,
		}}, nil

	case "user":
		return []DisplayEvent{&UserLine{}}, nil

	case "assistant":
		ast := ev.(*assistantEvent)
		var events []DisplayEvent
		for _, block := range ast.Message.Content {
			switch block.Type {
			case "tool_use":
				events = append(events, &ToolCallLine{
					Name:   block.Name,
					Detail: toolDetail(block.Name, block.Input),
					Claude: &ToolCallClaudeExt{Input: block.Input},
				})
			case "text":
				if block.Text != "" {
					events = append(events, &TextLine{Text: block.Text})
				}
			case "thinking":
				if block.Text != "" {
					events = append(events, &ThinkingLine{Text: block.Text})
				}
			}
		}
		return events, nil

	case "tool_result":
		tr := ev.(*toolResultEvent)
		return []DisplayEvent{&ToolResultLine{Content: tr.Content}}, nil

	case "result":
		res := ev.(*resultEvent)
		cost := res.TotalCostUSD
		if cost == 0 {
			cost = res.CostUSD
		}
		return []DisplayEvent{&ResultLine{
			Cost:            cost,
			DurationMS:      res.DurationMS,
			Turns:           res.NumTurns,
			InputTokens:     res.Usage.InputTokens,
			OutputTokens:    res.Usage.OutputTokens,
			CacheReadTokens: res.Usage.CacheReadInputTokens,
			IsError:         res.IsError,
		}}, nil
	}

	return nil, nil
}

func parseCodexDisplay(line []byte) ([]DisplayEvent, error) {
	typ, ev, err := agent.ParseCodexLine(line)
	if err != nil || ev == nil {
		return nil, err
	}

	switch typ {
	case "system":
		return []DisplayEvent{&SystemLine{}}, nil

	case "tool_use":
		te := ev.(*agent.CodexToolUseEvent)
		return []DisplayEvent{&ToolCallLine{
			Name:   te.ToolName,
			Detail: te.ToolInput,
		}}, nil

	case "assistant", "item_completed":
		te := ev.(*agent.CodexTextEvent)
		if te.Text != "" {
			return []DisplayEvent{&TextLine{Text: te.Text}}, nil
		}
		return nil, nil

	case "result":
		re := ev.(*agent.CodexResultEvent)
		return []DisplayEvent{&ResultLine{
			DurationMS:   re.DurationMS,
			InputTokens:  re.InputTokens,
			OutputTokens: re.OutputTokens,
			Turns:        1,
			IsError:      re.IsError,
		}}, nil

	case "error":
		ee := ev.(*agent.CodexErrorEvent)
		return []DisplayEvent{&ErrorLine{Message: ee.Message}}, nil
	}

	return nil, nil
}
