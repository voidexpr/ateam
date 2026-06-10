package runner

import (
	"io"
	"os"
	"time"
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

	turns           int         // one per usage-bearing assistant message
	toolCount       int         // total tool_use blocks seen
	lastTool        string      // name of the most recent tool call
	contextTokens   int         // context size of the latest assistant message (input + cache)
	cumInputTokens  int         // cumulative input tokens across messages
	cumOutputTokens int         // cumulative output tokens across messages
	result          *ResultLine // non-nil once the terminal result event was seen
}

func NewProgressScanner(path string) *ProgressScanner {
	return &ProgressScanner{Path: path}
}

// Poll reads bytes appended to the stream file since the last call and
// folds complete lines into the counters. A missing or unchanged file is
// a no-op, so it is safe to poll before the agent has created the file.
func (s *ProgressScanner) Poll() {
	if s.Path == "" || s.result != nil {
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
			s.toolCount++
			s.lastTool = e.Name
			usage = e.Usage
		case *TextLine:
			usage = e.Usage
		case *ThinkingLine:
			usage = e.Usage
		case *ResultLine:
			s.result = e
			if e.Turns > s.turns {
				s.turns = e.Turns
			}
		}
		if usage != nil && !counted {
			counted = true
			s.turns++
			s.cumInputTokens += usage.InputTokens
			s.cumOutputTokens += usage.OutputTokens
			s.contextTokens = usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens
		}
	}
}

// Progress adapts the accumulated counters into the RunProgress shape the
// pool row formatters consume. execID and elapsed belong to the caller; the
// scanner only knows what the stream told it.
func (s *ProgressScanner) Progress(execID int64, elapsed time.Duration) RunProgress {
	phase := PhaseInit
	if s.lastTool != "" {
		phase = PhaseTool
	}
	return RunProgress{
		ExecID:                 execID,
		Phase:                  phase,
		ToolName:               s.lastTool,
		ToolCount:              s.toolCount,
		TurnCount:              s.turns,
		Elapsed:                elapsed,
		ContextTokens:          s.contextTokens,
		CumulativeInputTokens:  s.cumInputTokens,
		CumulativeOutputTokens: s.cumOutputTokens,
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
