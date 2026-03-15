package runner

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// StreamFormatter formats JSONL stream lines for human consumption.
// It is stateful — tracks turn number, tool count, etc.
type StreamFormatter struct {
	Verbose    bool
	Color      bool
	Prefix     string // for multiplexed tail: "[42:security/run] "
	TurnNum    int
	ToolCount  int
	TextCount  int
	EventCount int
	hasResult  bool
}

func (f *StreamFormatter) HasResult() bool { return f.hasResult }

// FormatLine processes a single JSONL line and returns formatted output.
// Returns empty string for unknown/skipped events.
func (f *StreamFormatter) FormatLine(line []byte) string {
	typ, ev, err := parseStreamLine(line)
	if err != nil || ev == nil {
		return ""
	}
	f.EventCount++

	switch typ {
	case "system":
		return f.fmtSystem(ev.(*systemEvent))
	case "user":
		return f.fmtUser()
	case "assistant":
		return f.fmtAssistant(ev.(*assistantEvent))
	case "tool_result":
		if f.Verbose {
			return f.fmtToolResult(ev.(*toolResultEvent))
		}
		return ""
	case "result":
		return f.fmtResult(ev.(*resultEvent))
	default:
		return ""
	}
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

func (f *StreamFormatter) fmtSystem(ev *systemEvent) string {
	if ev.Subtype != "init" {
		return ""
	}
	var b strings.Builder
	b.WriteString(f.Prefix)
	b.WriteString(f.dim(fmt.Sprintf("--- session %s  model=%s", short(ev.SessionID, 12), ev.Model)))
	if ev.ClaudeCodeVersion != "" {
		b.WriteString(f.dim(fmt.Sprintf("  v%s", ev.ClaudeCodeVersion)))
	}
	b.WriteString("\n")
	if f.Verbose && ev.Cwd != "" {
		b.WriteString(f.Prefix)
		b.WriteString(f.dim(fmt.Sprintf("    cwd: %s", ev.Cwd)))
		b.WriteString("\n")
	}
	return b.String()
}

func (f *StreamFormatter) fmtUser() string {
	f.TurnNum++
	return fmt.Sprintf("\n%s%s\n", f.Prefix, f.boldMagenta(fmt.Sprintf("=== Turn %d ===", f.TurnNum)))
}

func (f *StreamFormatter) fmtAssistant(ev *assistantEvent) string {
	var b strings.Builder

	hasTools := false
	hasText := false

	for _, block := range ev.Message.Content {
		switch block.Type {
		case "tool_use":
			hasTools = true
			f.ToolCount++
			detail := toolDetail(block.Name, block.Input)
			if f.Verbose {
				b.WriteString(fmt.Sprintf("%s%s\n", f.Prefix,
					f.cyan(fmt.Sprintf("  tool #%d: ", f.ToolCount))+f.boldCyan(block.Name)))
				input := strings.TrimSpace(string(block.Input))
				if input != "" && input != "{}" && input != "null" {
					for _, line := range strings.Split(input, "\n") {
						b.WriteString(fmt.Sprintf("%s           %s\n", f.Prefix, line))
					}
				}
			} else {
				if detail != "" {
					detail = truncate(detail, 100)
					b.WriteString(fmt.Sprintf("%s%s %s\n", f.Prefix,
						f.cyan(fmt.Sprintf("  tool #%d: ", f.ToolCount))+f.boldCyan(block.Name),
						f.dim(detail)))
				} else {
					b.WriteString(fmt.Sprintf("%s%s\n", f.Prefix,
						f.cyan(fmt.Sprintf("  tool #%d: ", f.ToolCount))+f.boldCyan(block.Name)))
				}
			}

		case "text":
			if block.Text == "" {
				continue
			}
			hasText = true
			f.TextCount++
			if f.Verbose {
				b.WriteString(fmt.Sprintf("%s%s\n", f.Prefix,
					f.yellow(fmt.Sprintf("  text #%d:", f.TextCount))))
				for _, line := range strings.Split(block.Text, "\n") {
					b.WriteString(fmt.Sprintf("%s    %s\n", f.Prefix, line))
				}
			} else {
				preview := singleLineText(block.Text)
				preview = truncate(preview, 120)
				b.WriteString(fmt.Sprintf("%s%s %s\n", f.Prefix,
					f.yellow(fmt.Sprintf("  text #%d:", f.TextCount)),
					f.dim(preview)))
			}

		case "thinking":
			if f.Verbose && block.Text != "" {
				b.WriteString(f.Prefix + f.dim("  thinking:") + "\n")
				for _, line := range strings.Split(block.Text, "\n") {
					b.WriteString(fmt.Sprintf("%s    %s\n", f.Prefix, f.dim(line)))
				}
			}
		}
	}

	if !hasTools && !hasText {
		b.WriteString(f.Prefix + f.dim("  ... thinking") + "\n")
	}

	return b.String()
}

func (f *StreamFormatter) fmtToolResult(ev *toolResultEvent) string {
	content := strings.TrimSpace(ev.Content)
	if content == "" {
		return ""
	}
	content = truncate(content, 500)
	return fmt.Sprintf("%s%s\n", f.Prefix, f.dim("  result: "+content))
}

func (f *StreamFormatter) fmtResult(ev *resultEvent) string {
	f.hasResult = true
	cost := ev.TotalCostUSD
	if cost == 0 {
		cost = ev.CostUSD
	}

	durSec := ev.DurationMS / 1000
	durStr := fmt.Sprintf("%dm %ds", durSec/60, durSec%60)

	status := "ok"
	if ev.IsError {
		status = "error"
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("\n%s%s\n", f.Prefix, f.boldGreen("=== Result ===")))
	b.WriteString(fmt.Sprintf("%s  Status:    %s\n", f.Prefix, status))
	b.WriteString(fmt.Sprintf("%s  Duration:  %s\n", f.Prefix, durStr))
	b.WriteString(fmt.Sprintf("%s  Cost:      $%.2f\n", f.Prefix, cost))
	b.WriteString(fmt.Sprintf("%s  Turns:     %d\n", f.Prefix, ev.NumTurns))
	b.WriteString(fmt.Sprintf("%s  Tokens:    in=%d out=%d cache_read=%d\n", f.Prefix,
		ev.Usage.InputTokens, ev.Usage.OutputTokens, ev.Usage.CacheReadInputTokens))
	b.WriteString(fmt.Sprintf("%s  Events:    %d (tools=%d, text=%d)\n", f.Prefix,
		f.EventCount, f.ToolCount, f.TextCount))
	return b.String()
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

func singleLineText(s string) string {
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
