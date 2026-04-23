// Package streamutil defines shared JSONL event types and parsing utilities for agent output streams.
package streamutil

import "encoding/json"

// Claude JSONL event types shared between agent and runner packages.
// Superset of fields from both consumers — the JSON decoder ignores absent fields.

type TypedEvent struct {
	Type string `json:"type"`
}

type SystemEvent struct {
	Subtype           string `json:"subtype"`
	SessionID         string `json:"session_id"`
	Model             string `json:"model"`
	Cwd               string `json:"cwd"`
	ClaudeCodeVersion string `json:"claude_code_version"`
}

type AssistantEvent struct {
	Message struct {
		Content []ContentBlock `json:"content"`
		Usage   struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

type ContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type UserEvent struct{}

type ToolResultEvent struct {
	Content string `json:"content"`
}

type ResultEvent struct {
	TotalCostUSD float64 `json:"total_cost_usd"`
	CostUSD      float64 `json:"cost_usd"`
	DurationMS   int64   `json:"duration_ms"`
	NumTurns     int     `json:"num_turns"`
	IsError      bool    `json:"is_error"`
	// Subtype distinguishes success vs. failure kinds: "success",
	// "error_during_execution", "error_max_turns", etc.
	Subtype string `json:"subtype"`
	// Result carries the human-readable final text; when IsError is true
	// this is the API error message (e.g. "Stream idle timeout - partial
	// response received").
	Result string `json:"result"`
	// TerminalReason is set by Claude for stream-terminated runs, e.g.
	// "completed", "interrupted".
	TerminalReason string `json:"terminal_reason"`
	Usage          struct {
		InputTokens           int `json:"input_tokens"`
		OutputTokens          int `json:"output_tokens"`
		CacheReadInputTokens  int `json:"cache_read_input_tokens"`
		CacheWriteInputTokens int `json:"cache_write_input_tokens"`
	} `json:"usage"`
	ModelUsage map[string]struct {
		ContextWindow   int `json:"contextWindow"`
		MaxOutputTokens int `json:"maxOutputTokens"`
	} `json:"modelUsage"`
}

// MaxContextWindow returns the largest contextWindow across all models in ModelUsage.
func (r *ResultEvent) MaxContextWindow() int {
	var max int
	for _, mu := range r.ModelUsage {
		if mu.ContextWindow > max {
			max = mu.ContextWindow
		}
	}
	return max
}

// ParseClaudeLine parses a single JSONL line from Claude's stream-json output.
// Returns the event type, the parsed struct, and any error.
// Unknown types return ("", nil, nil).
func ParseClaudeLine(line []byte) (string, any, error) {
	line = TrimBOM(line)
	if len(line) == 0 {
		return "", nil, nil
	}

	var typed TypedEvent
	if err := json.Unmarshal(line, &typed); err != nil {
		return "", nil, err
	}

	switch typed.Type {
	case "system":
		var ev SystemEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			return typed.Type, nil, err
		}
		return typed.Type, &ev, nil

	case "user":
		return typed.Type, &UserEvent{}, nil

	case "assistant":
		var ev AssistantEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			return typed.Type, nil, err
		}
		return typed.Type, &ev, nil

	case "tool_result":
		var ev ToolResultEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			return typed.Type, nil, err
		}
		return typed.Type, &ev, nil

	case "result":
		var ev ResultEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			return typed.Type, nil, err
		}
		return typed.Type, &ev, nil

	default:
		return "", nil, nil
	}
}
