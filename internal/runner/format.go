package runner

import (
	"bufio"
	"os"
	"strings"
	"time"
)

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
