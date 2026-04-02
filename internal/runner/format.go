package runner

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/ateam/internal/agent"
)

// FormatStreamOpts holds options for FormatStream.
type FormatStreamOpts struct {
	Pricing      agent.PricingTable
	DefaultModel string
}

// FormatStream reads a stream JSONL file and writes a human-readable
// representation to w. Returns any I/O error.
func FormatStream(path string, w io.Writer, opts *FormatStreamOpts) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	var pricing agent.PricingTable
	var defaultModel string
	if opts != nil {
		pricing = opts.Pricing
		defaultModel = opts.DefaultModel
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	turnNum := 0
	turnStarted := false
	model := ""
	hint := formatUnknown
	for scanner.Scan() {
		line := scanner.Bytes()
		events, detected, err := parseDisplayLine(line, hint)
		if err != nil || len(events) == 0 {
			continue
		}
		if hint == formatUnknown {
			hint = detected
		}

		for _, ev := range events {
			switch e := ev.(type) {
			case *SystemLine:
				if model == "" && e.Model != "" {
					model = e.Model
				}
				fmt.Fprintf(w, "── system ──\n")

			case *UserLine:
				turnNum++
				turnStarted = true
				fmt.Fprintf(w, "\n── turn %d ──\n", turnNum)

			case *ToolCallLine:
				fmt.Fprintf(w, "\n▶ %s\n", e.Name)
				detail := e.Detail
				if e.Claude != nil {
					detail = truncate(strings.TrimSpace(string(e.Claude.Input)), 500)
				}
				if detail != "" && detail != "{}" && detail != "null" {
					fmt.Fprintf(w, "  %s\n", detail)
				}

			case *TextLine:
				if !turnStarted {
					turnNum++
					turnStarted = true
					fmt.Fprintf(w, "\n── turn %d ──\n", turnNum)
				}
				if e.Text != "" {
					fmt.Fprintf(w, "%s\n", e.Text)
				}

			case *ToolResultLine:
				content := truncate(strings.TrimSpace(e.Content), 1000)
				if content != "" {
					fmt.Fprintf(w, "◀ %s\n", content)
				}

			case *ResultLine:
				cost := e.Cost
				if cost == 0 && model != "" {
					cost = agent.EstimateCost(pricing, model, defaultModel, e.InputTokens, e.OutputTokens)
				}
				fmt.Fprintf(w, "\n── result ──\n")
				fmt.Fprintf(w, "  Turns:    %d\n", e.Turns)
				fmt.Fprintf(w, "  Cost:     $%.4f\n", cost)
				fmt.Fprintf(w, "  Duration: %s\n", FormatDuration(msToDuration(e.DurationMS)))
				fmt.Fprintf(w, "  Input:    %d tokens\n", e.InputTokens)
				fmt.Fprintf(w, "  Output:   %d tokens\n", e.OutputTokens)
				if e.CacheReadTokens > 0 {
					fmt.Fprintf(w, "  Cache Read:  %d tokens\n", e.CacheReadTokens)
				}
				if e.CacheWriteTokens > 0 {
					fmt.Fprintf(w, "  Cache Write: %d tokens\n", e.CacheWriteTokens)
				}
				if e.IsError {
					fmt.Fprintf(w, "  ERROR:    true\n")
				}

			case *ErrorLine:
				fmt.Fprintf(w, "  ERROR:    %s\n", e.Message)
			}
		}
	}

	return scanner.Err()
}

// knownErrors maps substrings in the last assistant message to short error descriptions.
var knownErrors = []string{
	"Credit balance is too low",
}

// StreamTailError reads the stream JSONL and checks the last assistant message
// for known error patterns. If found, returns "{agentName}: {error}" directly.
// Otherwise returns the last maxMessages assistant text blocks formatted.
// Returns "" if nothing useful is found.
func StreamTailError(path, agentName string, maxMessages int) string {
	if maxMessages <= 0 {
		return ""
	}
	messages := streamTailMessages(path, maxMessages)
	if len(messages) == 0 {
		return ""
	}

	last := messages[len(messages)-1]
	for _, pattern := range knownErrors {
		if strings.Contains(last, pattern) {
			return agentName + ": " + pattern
		}
	}

	var b strings.Builder
	for i, msg := range messages {
		if i > 0 {
			b.WriteString("\n")
		}
		for _, line := range strings.Split(strings.TrimRight(msg, "\n"), "\n") {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// streamTailMessages reads the stream JSONL and returns the last n assistant
// text blocks.
func streamTailMessages(path string, n int) []string {
	if n <= 0 {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	messages := make([]string, 0, n)
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
			if tl, ok := ev.(*TextLine); ok && tl.Text != "" {
				if len(messages) >= n {
					copy(messages, messages[1:])
					messages = messages[:n-1]
				}
				messages = append(messages, truncate(tl.Text, 500))
			}
		}
	}
	return messages
}

func msToDuration(ms int64) time.Duration {
	return time.Duration(ms) * time.Millisecond
}
