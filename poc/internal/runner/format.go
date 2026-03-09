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

func msToDuration(ms int64) time.Duration {
	return time.Duration(ms) * time.Millisecond
}
