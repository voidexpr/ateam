package runner

import (
	"bufio"
	"os"
	"strings"

	"github.com/ateam/internal/streamutil"
)

const (
	PhaseInit       = "init"
	PhaseThinking   = "thinking"
	PhaseTool       = "tool"
	PhaseToolResult = "tool_result"
	PhaseDone       = "done"
	PhaseError      = "error"
	PhaseStall      = "stall"
)

// Type aliases for the shared streamutil types, used throughout the runner package.
type (
	systemEvent     = streamutil.SystemEvent
	assistantEvent  = streamutil.AssistantEvent
	contentBlock    = streamutil.ContentBlock
	toolResultEvent = streamutil.ToolResultEvent
	resultEvent     = streamutil.ResultEvent
)

// parseStreamLine delegates to the shared streamutil parser.
var parseStreamLine = streamutil.ParseClaudeLine

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

// scanStreamFileForFinalText reads a stream JSONL file and returns the
// concatenated text of the last assistant message. Returns "" if the file
// has no text content. Handles both Claude (one assistant JSONL line may
// hold multiple text blocks) and Codex (one assistant/item_completed line
// per text emission).
func scanStreamFileForFinalText(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	var last string
	hint := formatUnknown
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		events, detected, err := parseDisplayLine(scanner.Bytes(), hint)
		if err != nil || len(events) == 0 {
			continue
		}
		if hint == formatUnknown {
			hint = detected
		}
		var parts []string
		for _, ev := range events {
			if tl, ok := ev.(*TextLine); ok && tl.Text != "" {
				parts = append(parts, tl.Text)
			}
		}
		if len(parts) > 0 {
			last = strings.Join(parts, "")
		}
	}
	return last
}

// scanStreamFileForResult reads a stream JSONL file and returns the last
// ResultLine found, or nil if none exists. Handles both Claude and Codex formats.
// Used as a fallback to extract cost/usage data when the event channel was
// closed before the result arrived.
func scanStreamFileForResult(path string) *ResultLine {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var last *ResultLine
	hint := formatUnknown
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		events, detected, err := parseDisplayLine(scanner.Bytes(), hint)
		if err != nil || len(events) == 0 {
			continue
		}
		if hint == formatUnknown {
			hint = detected
		}
		for _, ev := range events {
			if rl, ok := ev.(*ResultLine); ok {
				last = rl
			}
		}
	}
	return last
}
