package runner

import (
	"bufio"
	"fmt"
	"html"
	"io"
	"os"
	"strings"
	"time"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/display"
)

// HTMLStreamFormatter formats JSONL stream lines as HTML.
// Mirrors StreamFormatter but outputs styled HTML instead of ANSI codes.
type HTMLStreamFormatter struct {
	Verbose      bool
	Model        string
	DefaultModel string
	Pricing      agent.PricingTable
	// SessionStart, when set, is rendered on the session header and used
	// (with result.DurationMS) to compute the session end clock.
	SessionStart time.Time
	TurnNum      int
	ToolCount    int
	TextCount    int
	EventCount   int
	hasResult    bool
	format       streamFormat
}

// FormatFile reads a JSONL file and writes formatted HTML to w.
func (f *HTMLStreamFormatter) FormatFile(path string, w io.Writer) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	fmt.Fprint(w, `<div class="stream-log">`)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		events, detected, err := parseDisplayLine(scanner.Bytes(), f.format)
		if err != nil || len(events) == 0 {
			continue
		}
		if f.format == formatUnknown {
			f.format = detected
		}
		for _, ev := range events {
			if out := f.formatEvent(ev); out != "" {
				fmt.Fprint(w, out)
			}
		}
	}
	fmt.Fprint(w, `</div>`)
	return scanner.Err()
}

func (f *HTMLStreamFormatter) formatEvent(ev DisplayEvent) string {
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

func (f *HTMLStreamFormatter) fmtSystem(e *SystemLine) string {
	if f.Model == "" && e.Model != "" {
		f.Model = e.Model
	}
	if e.SessionID == "" && e.Model == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<div class="sl-session">`)
	fmt.Fprintf(&b, "--- session %s  model=%s",
		esc(short(e.SessionID, 12)), esc(e.Model))
	if e.Version != "" {
		fmt.Fprintf(&b, "  v%s", esc(e.Version))
	}
	if !f.SessionStart.IsZero() {
		fmt.Fprintf(&b, "  started=%s", esc(f.SessionStart.Format("2006-01-02 15:04:05")))
	}
	if f.Verbose && e.Cwd != "" {
		fmt.Fprintf(&b, "\n    cwd: %s", esc(e.Cwd))
	}
	b.WriteString("</div>\n")
	return b.String()
}

func (f *HTMLStreamFormatter) fmtUser() string {
	f.TurnNum++
	return fmt.Sprintf(`<div class="sl-turn">=== Turn %d ===</div>`+"\n", f.TurnNum)
}

func (f *HTMLStreamFormatter) fmtToolCall(e *ToolCallLine) string {
	f.ToolCount++
	var b strings.Builder
	b.WriteString(`<div class="sl-tool">`)
	fmt.Fprintf(&b, `<span class="sl-tool-num">tool #%d: </span>`, f.ToolCount)
	fmt.Fprintf(&b, `<span class="sl-tool-name">%s</span>`, esc(e.Name))

	if f.Verbose && e.Claude != nil {
		input := strings.TrimSpace(string(e.Claude.Input))
		if input != "" && input != "{}" && input != "null" {
			fmt.Fprintf(&b, "\n<pre class=\"sl-tool-input\">%s</pre>", esc(input))
		}
	} else {
		detail := truncate(e.Detail, 100)
		if detail != "" {
			fmt.Fprintf(&b, ` <span class="sl-dim">%s</span>`, esc(detail))
		}
	}
	if suffix := f.usageSuffixHTML(e.Usage); suffix != "" {
		fmt.Fprintf(&b, " %s", suffix)
	}
	b.WriteString("</div>\n")
	return b.String()
}

func (f *HTMLStreamFormatter) fmtText(e *TextLine) string {
	f.TextCount++
	klass := "sl-text"
	if looksLikeAPIError(e.Text) {
		klass = "sl-text sl-error"
	}
	var b strings.Builder
	if f.Verbose {
		fmt.Fprintf(&b, `<div class="%s"><span class="sl-text-label">text #%d:</span>`, klass, f.TextCount)
		fmt.Fprintf(&b, `<pre class="sl-text-body">%s</pre>`, esc(e.Text))
	} else {
		preview := esc(truncate(SingleLineText(e.Text), 120))
		fmt.Fprintf(&b, `<div class="%s"><span class="sl-text-label">text #%d:</span> <span class="sl-dim">%s</span>`,
			klass, f.TextCount, preview)
	}
	if suffix := f.usageSuffixHTML(e.Usage); suffix != "" {
		fmt.Fprintf(&b, " %s", suffix)
	}
	b.WriteString("</div>\n")
	return b.String()
}

func (f *HTMLStreamFormatter) fmtThinking(e *ThinkingLine) string {
	var b strings.Builder
	if f.Verbose {
		fmt.Fprintf(&b, "<div class=\"sl-thinking\"><span class=\"sl-dim\">thinking:</span>\n<pre class=\"sl-thinking-body\">%s</pre>",
			esc(e.Text))
	} else {
		preview := esc(truncate(SingleLineText(e.Text), 120))
		fmt.Fprintf(&b, `<div class="sl-thinking"><span class="sl-dim">thinking: %s</span>`, preview)
	}
	if suffix := f.usageSuffixHTML(e.Usage); suffix != "" {
		fmt.Fprintf(&b, " %s", suffix)
	}
	b.WriteString("</div>\n")
	return b.String()
}

func (f *HTMLStreamFormatter) fmtToolResult(e *ToolResultLine) string {
	bytes, lines := toolResultSize(e.Content)
	if bytes == 0 {
		return ""
	}
	body := fmt.Sprintf("  result: %s, %d lines", display.FmtBytes(bytes), lines)
	if e.IsError {
		return fmt.Sprintf(`<div class="sl-error">%s (error)</div>`+"\n", esc(body))
	}
	return fmt.Sprintf(`<div class="sl-dim">%s</div>`+"\n", esc(body))
}

// usageSuffixHTML renders the per-message usage chunks (verbose only)
// as an inline span so consumers can style sl-usage in CSS.
func (f *HTMLStreamFormatter) usageSuffixHTML(u *MessageUsage) string {
	if !f.Verbose {
		return ""
	}
	parts := usageParts(u, f.Model)
	if len(parts) == 0 {
		return ""
	}
	return `<span class="sl-usage sl-dim">[` + esc(strings.Join(parts, " ")) + `]</span>`
}

func (f *HTMLStreamFormatter) fmtResult(e *ResultLine) string {
	f.hasResult = true

	cost := e.Cost
	if cost == 0 && f.Model != "" {
		cost = agent.EstimateCost(f.Pricing, f.Model, f.DefaultModel, e.InputTokens, e.OutputTokens)
	}

	durSec := e.DurationMS / 1000
	durStr := fmt.Sprintf("%dm %ds", durSec/60, durSec%60)

	statusHTML := "ok"
	if e.IsError {
		statusHTML = `<span class="sl-error">error</span>`
	}

	estimated := ""
	if cost > 0 && e.Cost == 0 {
		estimated = ` <span class="sl-dim">(estimated)</span>`
	}

	var b strings.Builder
	b.WriteString(`<div class="sl-result">`)
	b.WriteString(`<div class="sl-result-header">=== Result ===</div>`)
	fmt.Fprintf(&b, "<div>  Status:    %s</div>", statusHTML)
	fmt.Fprintf(&b, "<div>  Duration:  %s</div>", esc(durStr))
	if !f.SessionStart.IsZero() && e.DurationMS > 0 {
		end := f.SessionStart.Add(time.Duration(e.DurationMS) * time.Millisecond)
		fmt.Fprintf(&b, "<div>  Started:   %s</div>", esc(f.SessionStart.Format("2006-01-02 15:04:05")))
		fmt.Fprintf(&b, "<div>  Ended:     %s</div>", esc(end.Format("2006-01-02 15:04:05")))
	}
	fmt.Fprintf(&b, "<div>  Cost:      $%.2f%s</div>", cost, estimated)
	fmt.Fprintf(&b, "<div>  Turns:     %d</div>", e.Turns)
	fmt.Fprintf(&b, "<div>  Tokens:    in=%d out=%d cache_read=%d cache_write=%d</div>",
		e.InputTokens, e.OutputTokens, e.CacheReadTokens, e.CacheWriteTokens)
	fmt.Fprintf(&b, "<div>  Events:    %d (tools=%d, text=%d)</div>",
		f.EventCount, f.ToolCount, f.TextCount)
	b.WriteString("</div>\n")
	return b.String()
}

func (f *HTMLStreamFormatter) fmtError(e *ErrorLine) string {
	return fmt.Sprintf(`<div class="sl-error">  error: %s</div>`+"\n", esc(e.Message))
}

// fmtRateLimit renders a rate_limit_event as a dim div.
// Verbose adds the absolute reset clock and the disable reason.
func (f *HTMLStreamFormatter) fmtRateLimit(e *RateLimitLine) string {
	header, extras := rateLimitSummary(e)
	var b strings.Builder
	fmt.Fprintf(&b, `<div class="sl-rate-limit sl-dim">--- %s</div>`+"\n", esc(header))
	if f.Verbose {
		for _, line := range extras {
			fmt.Fprintf(&b, `<div class="sl-rate-limit sl-dim">    %s</div>`+"\n", esc(line))
		}
	}
	return b.String()
}

func esc(s string) string {
	return html.EscapeString(s)
}
