package runner

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// FormatStream reads a stream JSONL file and writes a human-readable
// representation to w. Returns any I/O error.
func FormatStream(path string, w io.Writer) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	turnNum := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		typ, ev, err := parseStreamLine(line)
		if err != nil || ev == nil {
			continue
		}

		switch typ {
		case "system":
			sys := ev.(*systemEvent)
			fmt.Fprintf(w, "── system (%s) ──\n", sys.Subtype)

		case "assistant":
			turnNum++
			ast := ev.(*assistantEvent)
			fmt.Fprintf(w, "\n── turn %d ──\n", turnNum)
			for _, block := range ast.Message.Content {
				switch block.Type {
				case "text":
					if block.Text != "" {
						fmt.Fprintf(w, "%s\n", block.Text)
					}
				case "tool_use":
					input := truncate(strings.TrimSpace(string(block.Input)), 500)
					fmt.Fprintf(w, "\n▶ %s\n", block.Name)
					if input != "" && input != "{}" && input != "null" {
						fmt.Fprintf(w, "  %s\n", input)
					}
				}
			}

		case "tool_result":
			tr := ev.(*toolResultEvent)
			content := truncate(strings.TrimSpace(tr.Content), 1000)
			if content != "" {
				fmt.Fprintf(w, "◀ %s\n", content)
			}

		case "result":
			res := ev.(*resultEvent)
			fmt.Fprintf(w, "\n── result ──\n")
			fmt.Fprintf(w, "  Turns:    %d\n", res.NumTurns)
			fmt.Fprintf(w, "  Cost:     $%.4f\n", res.TotalCostUSD)
			fmt.Fprintf(w, "  Duration: %s\n", FormatDuration(msToDuration(res.DurationMS)))
			fmt.Fprintf(w, "  Input:    %d tokens\n", res.Usage.InputTokens)
			fmt.Fprintf(w, "  Output:   %d tokens\n", res.Usage.OutputTokens)
			if res.IsError {
				fmt.Fprintf(w, "  ERROR:    true\n")
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
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var messages []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		typ, ev, err := parseStreamLine(scanner.Bytes())
		if err != nil || ev == nil || typ != "assistant" {
			continue
		}
		text := extractReportText(ev.(*assistantEvent))
		if text == "" {
			continue
		}
		messages = append(messages, truncate(text, 500))
	}

	if len(messages) > n {
		messages = messages[len(messages)-n:]
	}
	return messages
}

func msToDuration(ms int64) time.Duration {
	return time.Duration(ms) * time.Millisecond
}
