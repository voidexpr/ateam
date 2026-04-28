package runner

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/display"
)

// StreamFormatter formats JSONL stream lines for human consumption.
// It is stateful — tracks turn number, tool count, etc.
type StreamFormatter struct {
	Verbose      bool
	Color        bool
	Model        string             // for cost estimation when not reported natively
	DefaultModel string             // fallback model for pricing lookup
	Pricing      agent.PricingTable // cost estimation table (nil = use native cost only)
	Prefix       string             // for multiplexed tail: "[42:security/run] "
	// SessionStart, when set, is rendered as an absolute clock on the
	// session header (fmtSystem) and as the end clock on fmtResult
	// (computed as SessionStart + result.DurationMS). Optional — zero
	// value disables both.
	SessionStart time.Time
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
		return f.fmtToolResult(e)
	case *ResultLine:
		return f.fmtResult(e)
	case *ErrorLine:
		return f.fmtError(e)
	case *RateLimitLine:
		return f.fmtRateLimit(e)
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
	if !f.SessionStart.IsZero() {
		b.WriteString(f.dim(fmt.Sprintf("  started=%s", f.SessionStart.Format("2006-01-02 15:04:05"))))
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
		fmt.Fprintf(&b, "%s%s\n", f.Prefix, header)
		input := strings.TrimSpace(string(e.Claude.Input))
		if input != "" && input != "{}" && input != "null" {
			for _, line := range strings.Split(input, "\n") {
				fmt.Fprintf(&b, "%s           %s\n", f.Prefix, line)
			}
		}
		if suffix := f.usageSuffix(e.Usage); suffix != "" {
			fmt.Fprintf(&b, "%s           %s\n", f.Prefix, suffix)
		}
		return b.String()
	}
	detail := truncate(e.Detail, 100)
	var line string
	if detail != "" {
		line = fmt.Sprintf("%s%s %s\n", f.Prefix, header, f.dim(detail))
	} else {
		line = fmt.Sprintf("%s%s\n", f.Prefix, header)
	}
	if suffix := f.usageSuffix(e.Usage); suffix != "" {
		line += fmt.Sprintf("%s           %s\n", f.Prefix, suffix)
	}
	return line
}

func (f *StreamFormatter) fmtText(e *TextLine) string {
	f.TextCount++
	asError := looksLikeAPIError(e.Text)
	colorBody := func(s string) string {
		if asError {
			return f.red(s)
		}
		return s
	}
	var b strings.Builder
	if f.Verbose {
		fmt.Fprintf(&b, "%s%s\n", f.Prefix,
			f.yellow(fmt.Sprintf("  text #%d:", f.TextCount)))
		for _, line := range strings.Split(e.Text, "\n") {
			fmt.Fprintf(&b, "%s    %s\n", f.Prefix, colorBody(line))
		}
	} else {
		preview := SingleLineText(e.Text)
		preview = truncate(preview, 120)
		fmt.Fprintf(&b, "%s%s %s\n", f.Prefix,
			f.yellow(fmt.Sprintf("  text #%d:", f.TextCount)),
			colorBody(preview))
	}
	if suffix := f.usageSuffix(e.Usage); suffix != "" {
		fmt.Fprintf(&b, "%s    %s\n", f.Prefix, suffix)
	}
	return b.String()
}

func (f *StreamFormatter) fmtThinking(e *ThinkingLine) string {
	var b strings.Builder
	if f.Verbose {
		b.WriteString(f.Prefix + f.dim("  thinking:") + "\n")
		for _, line := range strings.Split(e.Text, "\n") {
			fmt.Fprintf(&b, "%s    %s\n", f.Prefix, f.dim(line))
		}
	} else {
		// Show a single-line preview so turns made up only of thinking
		// don't render empty.
		preview := SingleLineText(e.Text)
		preview = truncate(preview, 120)
		fmt.Fprintf(&b, "%s%s %s\n", f.Prefix, f.dim("  thinking:"), f.dim(preview))
	}
	if suffix := f.usageSuffix(e.Usage); suffix != "" {
		fmt.Fprintf(&b, "%s    %s\n", f.Prefix, suffix)
	}
	return b.String()
}

func (f *StreamFormatter) fmtToolResult(e *ToolResultLine) string {
	bytes, lines := toolResultSize(e.Content)
	if bytes == 0 {
		return ""
	}
	body := fmt.Sprintf("  result: %s, %d lines", display.FmtBytes(bytes), lines)
	if e.IsError {
		body += " (error)"
		return fmt.Sprintf("%s%s\n", f.Prefix, f.red(body))
	}
	return fmt.Sprintf("%s%s\n", f.Prefix, f.dim(body))
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
		status = f.red("error")
	}

	var b strings.Builder
	fmt.Fprintf(&b, "\n%s%s\n", f.Prefix, f.boldGreen("=== Result ==="))
	fmt.Fprintf(&b, "%s  Status:    %s\n", f.Prefix, status)
	fmt.Fprintf(&b, "%s  Duration:  %s\n", f.Prefix, durStr)
	if !f.SessionStart.IsZero() && e.DurationMS > 0 {
		end := f.SessionStart.Add(time.Duration(e.DurationMS) * time.Millisecond)
		fmt.Fprintf(&b, "%s  Started:   %s\n", f.Prefix, f.SessionStart.Format("2006-01-02 15:04:05"))
		fmt.Fprintf(&b, "%s  Ended:     %s\n", f.Prefix, end.Format("2006-01-02 15:04:05"))
	}
	fmt.Fprintf(&b, "%s  Cost:      $%.2f\n", f.Prefix, cost)
	if cost > 0 && e.Cost == 0 {
		fmt.Fprintf(&b, "%s              %s\n", f.Prefix, f.dim("(estimated)"))
	}
	fmt.Fprintf(&b, "%s  Turns:     %d\n", f.Prefix, e.Turns)
	fmt.Fprintf(&b, "%s  Tokens:    in=%d out=%d cache_read=%d cache_write=%d\n", f.Prefix,
		e.InputTokens, e.OutputTokens, e.CacheReadTokens, e.CacheWriteTokens)
	fmt.Fprintf(&b, "%s  Events:    %d (tools=%d, text=%d)\n", f.Prefix,
		f.EventCount, f.ToolCount, f.TextCount)
	return b.String()
}

// fmtRateLimit renders a rate_limit_event as a single dimmed line.
// Verbose adds the absolute reset clock and the disable reason.
func (f *StreamFormatter) fmtRateLimit(e *RateLimitLine) string {
	header, extras := rateLimitSummary(e)
	out := fmt.Sprintf("%s%s\n", f.Prefix, f.dim("--- "+header))
	if f.Verbose {
		for _, line := range extras {
			out += fmt.Sprintf("%s%s\n", f.Prefix, f.dim("    "+line))
		}
	}
	return out
}

// usageSuffix renders the per-message usage line shown after assistant
// blocks in verbose mode. Returns "" when verbose is off or there's no
// usage data to show.
func (f *StreamFormatter) usageSuffix(u *MessageUsage) string {
	if !f.Verbose {
		return ""
	}
	parts := usageParts(u, f.Model)
	if len(parts) == 0 {
		return ""
	}
	return f.dim("[" + strings.Join(parts, " ") + "]")
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
