// Package streamutil defines shared JSONL event types and parsing utilities for agent output streams.
package streamutil

import (
	"encoding/json"
	"fmt"
)

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
		Usage   AssistantUsage `json:"usage"`
	} `json:"message"`
}

// AssistantUsage matches claude's per-message usage payload. The
// cache_creation sub-object is present on Sonnet 4.6 and later and breaks
// the totals down by ephemeral TTL bucket.
type AssistantUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreation            struct {
		Ephemeral1hInputTokens int `json:"ephemeral_1h_input_tokens"`
		Ephemeral5mInputTokens int `json:"ephemeral_5m_input_tokens"`
	} `json:"cache_creation"`
}

type ContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// Thinking holds the reasoning content for type="thinking" blocks
	// (extended-thinking turns from Sonnet/Opus). The body lives under
	// "thinking", not "text".
	Thinking string `json:"thinking,omitempty"`
	// ID is set on tool_use blocks (e.g. "toolu_…") and used to pair
	// with tool_result blocks via ToolUseID.
	ID string `json:"id,omitempty"`
	// ToolUseID/Content/IsError are set on tool_result blocks (claude
	// emits these nested under user events, not as top-level events).
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

// UserEvent captures the message content blocks claude emits under
// type="user" — usually a tool_result feeding back into the next turn.
type UserEvent struct {
	Message struct {
		Content []ContentBlock `json:"content"`
	} `json:"message"`
}

// ToolResultEvent is retained for legacy/standalone tool_result lines.
// Modern claude streams nest tool results inside UserEvent.Message.Content.
type ToolResultEvent struct {
	Content   string `json:"content"`
	ToolUseID string `json:"tool_use_id"`
}

// RateLimitEvent surfaces rate-limit decisions claude reports inline.
// "status" is "allowed" or "throttled"; rateLimitType is "five_hour" or
// "one_minute"; overageStatus indicates whether usage-based overage is
// permitted on the account.
type RateLimitEvent struct {
	RateLimitInfo struct {
		Status                string `json:"status"`
		ResetsAt              int64  `json:"resetsAt"`
		RateLimitType         string `json:"rateLimitType"`
		OverageStatus         string `json:"overageStatus"`
		OverageDisabledReason string `json:"overageDisabledReason"`
		IsUsingOverage        bool   `json:"isUsingOverage"`
	} `json:"rate_limit_info"`
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
//
// Recovers from panics inside encoding/json (observed on Go 1.26.2 with
// certain large payloads) and surfaces them as errors so a single
// corrupt or stdlib-tripping line doesn't tear down the whole run. The
// raw line is still on disk in _stream.jsonl for post-mortem debugging.
func ParseClaudeLine(line []byte) (typ string, ev any, err error) {
	defer func() {
		if r := recover(); r != nil {
			typ = ""
			ev = nil
			err = fmt.Errorf("panic in claude JSONL parser (line len=%d): %v", len(line), r)
		}
	}()

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
		var ev UserEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			return typed.Type, nil, err
		}
		return typed.Type, &ev, nil

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

	case "rate_limit_event":
		var ev RateLimitEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			return typed.Type, nil, err
		}
		return typed.Type, &ev, nil

	default:
		return "", nil, nil
	}
}
