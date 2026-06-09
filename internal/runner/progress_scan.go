package runner

import (
	"io"
	"os"
)

// ProgressScanner incrementally reads an agent stream JSONL file and
// accumulates progress counters. It mirrors the accounting the runner
// performs on live StreamEvents in ExecutePrepared, but works from the
// on-disk stream so observers (`ateam top`) can attach to runs they did
// not start, at any point in the run's lifetime.
type ProgressScanner struct {
	Path    string
	offset  int64
	partial []byte
	format  streamFormat

	Turns           int         // one per usage-bearing assistant message
	ToolCount       int         // total tool_use blocks seen
	LastTool        string      // name of the most recent tool call
	ContextTokens   int         // context size of the latest assistant message (input + cache)
	CumInputTokens  int         // cumulative input tokens across messages
	CumOutputTokens int         // cumulative output tokens across messages
	Result          *ResultLine // non-nil once the terminal result event was seen
}

func NewProgressScanner(path string) *ProgressScanner {
	return &ProgressScanner{Path: path}
}

// Poll reads bytes appended to the stream file since the last call and
// folds complete lines into the counters. A missing or unchanged file is
// a no-op, so it is safe to poll before the agent has created the file.
func (s *ProgressScanner) Poll() {
	if s.Path == "" || s.Result != nil {
		return
	}
	for _, line := range readNewLines(s.Path, &s.offset, &s.partial) {
		s.consume(line)
	}
}

func (s *ProgressScanner) consume(line []byte) {
	events, detected, err := parseDisplayLine(line, s.format)
	if err != nil || len(events) == 0 {
		return
	}
	if s.format == formatUnknown {
		s.format = detected
	}
	// The same usage payload repeats on every block of a multi-block
	// assistant message (claude charges per message), and one JSONL line
	// holds one message — count it once per line, mirroring the live
	// accounting in agent/claude.go.
	counted := false
	for _, ev := range events {
		var usage *MessageUsage
		switch e := ev.(type) {
		case *ToolCallLine:
			s.ToolCount++
			s.LastTool = e.Name
			usage = e.Usage
		case *TextLine:
			usage = e.Usage
		case *ThinkingLine:
			usage = e.Usage
		case *ResultLine:
			s.Result = e
			if e.Turns > s.Turns {
				s.Turns = e.Turns
			}
		}
		if usage != nil && !counted {
			counted = true
			s.Turns++
			s.CumInputTokens += usage.InputTokens
			s.CumOutputTokens += usage.OutputTokens
			s.ContextTokens = usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens
		}
	}
}

// readNewLines reads bytes appended to path since *offset, advancing the
// offset and carrying any trailing partial line in *partial across calls.
// Returns the complete lines read. Shared by Tailer and ProgressScanner.
func readNewLines(path string, offset *int64, partial *[]byte) [][]byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.Size() <= *offset {
		return nil
	}

	if *offset > 0 {
		if _, err := f.Seek(*offset, io.SeekStart); err != nil {
			return nil
		}
	}

	buf := make([]byte, info.Size()-*offset)
	n, _ := f.Read(buf)
	if n == 0 {
		return nil
	}
	buf = buf[:n]
	*offset += int64(n)

	if len(*partial) > 0 {
		buf = append(*partial, buf...)
		*partial = nil
	}

	lines, remainder := splitLines(buf)
	if len(remainder) > 0 {
		*partial = append([]byte(nil), remainder...)
	}
	return lines
}
