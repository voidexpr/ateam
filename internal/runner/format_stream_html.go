package runner

import (
	"bufio"
	"fmt"
	"html"
	"io"
	"os"
	"strings"

	"github.com/ateam/internal/agent"
)

// HTMLStreamFormatter formats JSONL stream lines as HTML.
// Mirrors StreamFormatter but outputs styled HTML instead of ANSI codes.
type HTMLStreamFormatter struct {
	Verbose      bool
	Model        string
	DefaultModel string
	Pricing      agent.PricingTable
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
	b.WriteString("</div>\n")
	return b.String()
}

func (f *HTMLStreamFormatter) fmtText(e *TextLine) string {
	f.TextCount++
	var b strings.Builder
	if f.Verbose {
		fmt.Fprintf(&b, `<div class="sl-text"><span class="sl-text-label">text #%d:</span>`, f.TextCount)
		fmt.Fprintf(&b, `<pre class="sl-text-body">%s</pre></div>`+"\n", esc(e.Text))
	} else {
		preview := esc(truncate(SingleLineText(e.Text), 120))
		fmt.Fprintf(&b, `<div class="sl-text"><span class="sl-text-label">text #%d:</span> <span class="sl-dim">%s</span></div>`+"\n",
			f.TextCount, preview)
	}
	return b.String()
}

func (f *HTMLStreamFormatter) fmtThinking(e *ThinkingLine) string {
	if !f.Verbose {
		return ""
	}
	return fmt.Sprintf("<div class=\"sl-thinking\"><span class=\"sl-dim\">thinking:</span>\n<pre class=\"sl-thinking-body\">%s</pre></div>\n",
		esc(e.Text))
}

func (f *HTMLStreamFormatter) fmtToolResult(e *ToolResultLine) string {
	content := strings.TrimSpace(e.Content)
	if content == "" {
		return ""
	}
	content = truncate(content, 500)
	return fmt.Sprintf(`<div class="sl-dim">  result: %s</div>`+"\n", esc(content))
}

func (f *HTMLStreamFormatter) fmtResult(e *ResultLine) string {
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

	estimated := ""
	if cost > 0 && e.Cost == 0 {
		estimated = ` <span class="sl-dim">(estimated)</span>`
	}

	var b strings.Builder
	b.WriteString(`<div class="sl-result">`)
	b.WriteString(`<div class="sl-result-header">=== Result ===</div>`)
	fmt.Fprintf(&b, "<div>  Status:    %s</div>", esc(status))
	fmt.Fprintf(&b, "<div>  Duration:  %s</div>", esc(durStr))
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

func esc(s string) string {
	return html.EscapeString(s)
}
