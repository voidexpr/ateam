package runner

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ateam/internal/agent"
)

// StreamFormatter formats JSONL stream lines for human consumption.
// It is stateful — tracks turn number, tool count, etc.
type StreamFormatter struct {
	Verbose      bool
	Color        bool
	Model        string            // for cost estimation when not reported natively
	DefaultModel string            // fallback model for pricing lookup
	Pricing      agent.PricingTable // cost estimation table (nil = use native cost only)
	Prefix       string            // for multiplexed tail: "[42:security/run] "
	TurnNum      int
	ToolCount    int
	TextCount    int
	EventCount   int
	hasResult    bool
	format       streamFormat
}

func (f *StreamFormatter) HasResult() bool { return f.hasResult }

// FormatLine processes a single JSONL line and returns formatted output.
// Returns empty string for unknown/skipped events.
func (f *StreamFormatter) FormatLine(line []byte) string {
	events, detected, err := parseDisplayLine(line, f.format)
	if err != nil || len(events) == 0 {
		return ""
	}
	if f.format == formatUnknown {
		f.format = detected
	}

	var b strings.Builder
	for _, ev := range events {
		b.WriteString(f.formatEvent(ev))
	}
	return b.String()
}

// FormatFile reads a JSONL file and writes formatted output to w.
func (f *StreamFormatter) FormatFile(path string, w io.Writer) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if out := f.FormatLine(scanner.Bytes()); out != "" {
			fmt.Fprint(w, out)
		}
	}
	return scanner.Err()
}

func (f *StreamFormatter) formatEvent(ev DisplayEvent) string {
	f.EventCount++
	switch e := ev.(type) {
	case *SystemLine:
		return f.fmtSystem(e)
	case *UserLine:
		return f.fmtUser()
	case *ToolCallLine:
		return f.fmtToolCall(e)
	case *TextLine:
		return f.fmtText(e)
	case *ThinkingLine:
		return f.fmtThinking(e)
	case *ToolResultLine:
		if f.Verbose {
			return f.fmtToolResult(e)
		}
		return ""
	case *ResultLine:
		return f.fmtResult(e)
	case *ErrorLine:
		return f.fmtError(e)
	}
	return ""
}

func (f *StreamFormatter) fmtSystem(e *SystemLine) string {
	// Auto-populate Model from stream if not set externally
	if f.Model == "" && e.Model != "" {
		f.Model = e.Model
	}
	if e.SessionID == "" && e.Model == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString(f.Prefix)
	b.WriteString(f.dim(fmt.Sprintf("--- session %s  model=%s", short(e.SessionID, 12), e.Model)))
	if e.Version != "" {
		b.WriteString(f.dim(fmt.Sprintf("  v%s", e.Version)))
	}
	b.WriteString("\n")
	if f.Verbose && e.Cwd != "" {
		b.WriteString(f.Prefix)
		b.WriteString(f.dim(fmt.Sprintf("    cwd: %s", e.Cwd)))
		b.WriteString("\n")
	}
	return b.String()
}

func (f *StreamFormatter) fmtUser() string {
	f.TurnNum++
	return fmt.Sprintf("\n%s%s\n", f.Prefix, f.boldMagenta(fmt.Sprintf("=== Turn %d ===", f.TurnNum)))
}

func (f *StreamFormatter) fmtToolCall(e *ToolCallLine) string {
	f.ToolCount++
	header := f.cyan(fmt.Sprintf("  tool #%d: ", f.ToolCount)) + f.boldCyan(e.Name)
	if f.Verbose && e.Claude != nil {
		var b strings.Builder
		b.WriteString(fmt.Sprintf("%s%s\n", f.Prefix, header))
		input := strings.TrimSpace(string(e.Claude.Input))
		if input != "" && input != "{}" && input != "null" {
			for _, line := range strings.Split(input, "\n") {
				b.WriteString(fmt.Sprintf("%s           %s\n", f.Prefix, line))
			}
		}
		return b.String()
	}
	detail := truncate(e.Detail, 100)
	if detail != "" {
		return fmt.Sprintf("%s%s %s\n", f.Prefix, header, f.dim(detail))
	}
	return fmt.Sprintf("%s%s\n", f.Prefix, header)
}

func (f *StreamFormatter) fmtText(e *TextLine) string {
	f.TextCount++
	if f.Verbose {
		var b strings.Builder
		b.WriteString(fmt.Sprintf("%s%s\n", f.Prefix,
			f.yellow(fmt.Sprintf("  text #%d:", f.TextCount))))
		for _, line := range strings.Split(e.Text, "\n") {
			b.WriteString(fmt.Sprintf("%s    %s\n", f.Prefix, line))
		}
		return b.String()
	}
	preview := SingleLineText(e.Text)
	preview = truncate(preview, 120)
	return fmt.Sprintf("%s%s %s\n", f.Prefix,
		f.yellow(fmt.Sprintf("  text #%d:", f.TextCount)),
		f.dim(preview))
}

func (f *StreamFormatter) fmtThinking(e *ThinkingLine) string {
	if !f.Verbose {
		return ""
	}
	var b strings.Builder
	b.WriteString(f.Prefix + f.dim("  thinking:") + "\n")
	for _, line := range strings.Split(e.Text, "\n") {
		b.WriteString(fmt.Sprintf("%s    %s\n", f.Prefix, f.dim(line)))
	}
	return b.String()
}

func (f *StreamFormatter) fmtToolResult(e *ToolResultLine) string {
	content := strings.TrimSpace(e.Content)
	if content == "" {
		return ""
	}
	content = truncate(content, 500)
	return fmt.Sprintf("%s%s\n", f.Prefix, f.dim("  result: "+content))
}

func (f *StreamFormatter) fmtResult(e *ResultLine) string {
	f.hasResult = true

	cost := e.Cost
	if cost == 0 && f.Model != "" {
		cost = agent.EstimateCost(f.Pricing, f.Model, f.DefaultModel, e.InputTokens, e.OutputTokens)
	}

	durSec := e.DurationMS / 1000
	durStr := fmt.Sprintf("%dm %ds", durSec/60, durSec%60)

	status := "ok"
	if e.IsError {
		status = "error"
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("\n%s%s\n", f.Prefix, f.boldGreen("=== Result ===")))
	b.WriteString(fmt.Sprintf("%s  Status:    %s\n", f.Prefix, status))
	b.WriteString(fmt.Sprintf("%s  Duration:  %s\n", f.Prefix, durStr))
	b.WriteString(fmt.Sprintf("%s  Cost:      $%.2f\n", f.Prefix, cost))
	if cost > 0 && e.Cost == 0 {
		b.WriteString(fmt.Sprintf("%s              %s\n", f.Prefix, f.dim("(estimated)")))
	}
	b.WriteString(fmt.Sprintf("%s  Turns:     %d\n", f.Prefix, e.Turns))
	b.WriteString(fmt.Sprintf("%s  Tokens:    in=%d out=%d cache_read=%d\n", f.Prefix,
		e.InputTokens, e.OutputTokens, e.CacheReadTokens))
	b.WriteString(fmt.Sprintf("%s  Events:    %d (tools=%d, text=%d)\n", f.Prefix,
		f.EventCount, f.ToolCount, f.TextCount))
	return b.String()
}

func (f *StreamFormatter) fmtError(e *ErrorLine) string {
	return fmt.Sprintf("%s%s\n", f.Prefix, f.red("  error: "+e.Message))
}

// toolDetail extracts a short description from the tool input.
func toolDetail(name string, input json.RawMessage) string {
	var m map[string]any
	if err := json.Unmarshal(input, &m); err != nil {
		return ""
	}

	str := func(key string) string {
		if v, ok := m[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
		return ""
	}

	switch name {
	case "Bash":
		cmd := str("command")
		if idx := strings.IndexByte(cmd, '\n'); idx >= 0 {
			cmd = cmd[:idx]
		}
		return truncate(cmd, 120)
	case "Read", "Edit", "Write":
		return str("file_path")
	case "Glob", "Grep":
		return str("pattern")
	case "Agent":
		return truncate(str("prompt"), 80)
	case "WebFetch":
		return str("url")
	case "WebSearch":
		return str("query")
	case "ToolSearch":
		return str("query")
	case "Skill":
		return str("skill_name")
	default:
		return ""
	}
}

// ANSI color helpers

func (f *StreamFormatter) dim(s string) string {
	if !f.Color {
		return s
	}
	return "\033[2m" + s + "\033[0m"
}

func (f *StreamFormatter) boldMagenta(s string) string {
	if !f.Color {
		return s
	}
	return "\033[1m\033[35m" + s + "\033[0m"
}

func (f *StreamFormatter) cyan(s string) string {
	if !f.Color {
		return s
	}
	return "\033[36m" + s + "\033[0m"
}

func (f *StreamFormatter) boldCyan(s string) string {
	if !f.Color {
		return s
	}
	return "\033[1m\033[36m" + s + "\033[0m"
}

func (f *StreamFormatter) yellow(s string) string {
	if !f.Color {
		return s
	}
	return "\033[33m" + s + "\033[0m"
}

func (f *StreamFormatter) boldGreen(s string) string {
	if !f.Color {
		return s
	}
	return "\033[1m\033[32m" + s + "\033[0m"
}

func (f *StreamFormatter) red(s string) string {
	if !f.Color {
		return s
	}
	return "\033[31m" + s + "\033[0m"
}

// SingleLineText collapses a multi-line string into a single trimmed line.
func SingleLineText(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", "")
	return strings.TrimSpace(s)
}

func short(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
