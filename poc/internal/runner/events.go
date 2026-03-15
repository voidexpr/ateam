package runner

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
)

const (
	PhaseInit       = "init"
	PhaseThinking   = "thinking"
	PhaseTool       = "tool"
	PhaseToolResult = "tool_result"
	PhaseDone       = "done"
	PhaseError      = "error"
)

type typedEvent struct {
	Type string `json:"type"`
}

type systemEvent struct {
	Subtype          string `json:"subtype"`
	SessionID        string `json:"session_id"`
	Model            string `json:"model"`
	Cwd              string `json:"cwd"`
	ClaudeCodeVersion string `json:"claude_code_version"`
}

type assistantEvent struct {
	Message struct {
		Content []contentBlock `json:"content"`
	} `json:"message"`
}

type contentBlock struct {
	Type  string          `json:"type"` // "text" or "tool_use"
	Text  string          `json:"text,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type userEvent struct{}

type toolResultEvent struct {
	Content string `json:"content"`
}

type resultEvent struct {
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

// parseStreamLine unmarshals a single JSONL line from claude's stream-json output.
// Returns the event type, the parsed struct, and any error.
// Unknown types return ("", nil, nil).
func parseStreamLine(line []byte) (string, any, error) {
	line = trimBOM(line)
	if len(line) == 0 {
		return "", nil, nil
	}

	var typed typedEvent
	if err := json.Unmarshal(line, &typed); err != nil {
		return "", nil, err
	}

	switch typed.Type {
	case "system":
		var ev systemEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			return typed.Type, nil, err
		}
		return typed.Type, &ev, nil

	case "user":
		return typed.Type, &userEvent{}, nil

	case "assistant":
		var ev assistantEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			return typed.Type, nil, err
		}
		return typed.Type, &ev, nil

	case "tool_result":
		var ev toolResultEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			return typed.Type, nil, err
		}
		return typed.Type, &ev, nil

	case "result":
		var ev resultEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			return typed.Type, nil, err
		}
		return typed.Type, &ev, nil

	default:
		return "", nil, nil
	}
}

// extractReportText concatenates all text blocks from the assistant message's
// content array, skipping tool_use blocks. Returns "" if ev is nil.
func extractReportText(ev *assistantEvent) string {
	if ev == nil {
		return ""
	}
	var parts []string
	for _, block := range ev.Message.Content {
		if block.Type == "text" && block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "")
}

// scanStreamFileForResult reads a stream JSONL file and returns the last
// result event found, or nil if none exists. Used as a fallback to extract
// cost/usage data when the event channel was closed before the result arrived.
func scanStreamFileForResult(path string) *resultEvent {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var last *resultEvent
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		typ, ev, err := parseStreamLine(scanner.Bytes())
		if err != nil || ev == nil {
			continue
		}
		if typ == "result" {
			res := ev.(*resultEvent)
			last = res
		}
	}
	return last
}

func trimBOM(b []byte) []byte {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return b[3:]
	}
	return b
}
