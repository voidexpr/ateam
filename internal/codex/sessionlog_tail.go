package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"
)

// TailOnEvent is called by TailSessionLog for each translated line it
// emits. Each line is a single codex-exec-stream-shape JSONL record
// (ending in '\n'), suitable for writing straight into stream.jsonl —
// ateam tail / ateam cat / parse_stream.go already understand this shape.
type TailOnEvent func(line []byte)

// TailSessionLog watches `<codexHome>/sessions/` for the rollout JSONL
// belonging to the codex-tmux run identified by (workdir, since, marker),
// then reads it as it grows and emits translated events via onEvent.
//
// Until the rollout file exists, polls FindSessionLog every poll cadence
// (codex writes session_meta within ~1s of startup). Once open, polls
// the file for new lines on the same cadence — codex flushes its rollout
// frequently enough that we observe events within a second.
//
// Returns when ctx is canceled. Survives transient read errors by
// looping; bails on a genuine open error after the path was discovered.
func TailSessionLog(ctx context.Context, codexHome, workdir string, since time.Time, marker string, onEvent TailOnEvent) {
	const poll = 500 * time.Millisecond

	var (
		f     *os.File
		rdr   *bufio.Reader
		state tailState
		// partial buffers a half-line that arrived between polls (e.g.
		// codex flushed mid-record). We prepend it to the next read.
		partial []byte
	)
	defer func() {
		if f != nil {
			_ = f.Close()
		}
	}()

	for {
		if err := ctx.Err(); err != nil {
			return
		}

		if f == nil {
			path, _ := FindSessionLog(codexHome, workdir, since, marker)
			if path == "" {
				if sleepCtx(ctx, poll) != nil {
					return
				}
				continue
			}
			opened, err := os.Open(path)
			if err != nil {
				// Either codex hasn't created it yet (race) or
				// permissions; try again next tick.
				if sleepCtx(ctx, poll) != nil {
					return
				}
				continue
			}
			f = opened
			rdr = bufio.NewReader(f)
		}

		for {
			chunk, err := rdr.ReadBytes('\n')
			if len(chunk) > 0 {
				if len(partial) > 0 {
					chunk = append(partial, chunk...)
					partial = partial[:0]
				}
				// Only translate if we have a full line (ends with \n).
				if chunk[len(chunk)-1] == '\n' {
					if out := translateSessionLine(chunk, &state); out != nil {
						onEvent(out)
					}
				} else {
					// Partial line — save for next tick.
					partial = append(partial[:0], chunk...)
				}
			}
			if err == nil {
				continue
			}
			if errors.Is(err, io.EOF) {
				break
			}
			// Read error; give up. The post-run ReadSessionStats path
			// will fall back to a one-shot read of the same file.
			return
		}
		if sleepCtx(ctx, poll) != nil {
			return
		}
	}
}

// tailState carries the fields a session-log record can fill in but a
// later record needs (e.g. model name from turn_context arrives before
// task_complete, which wants to record it in `turn.completed.model`).
type tailState struct {
	Model    string
	ThreadID string
}

// translateSessionLine maps one codex rollout JSONL record to one
// codex-exec-stream JSONL line (matching parse_stream.go's formatCodex
// shape) or returns nil to drop the record. The translator is
// intentionally narrow — it only emits what ateam tail / cat needs to
// render progress, not the whole codex transcript.
func translateSessionLine(line []byte, state *tailState) []byte {
	var rec rolloutRecord
	if err := json.Unmarshal(line, &rec); err != nil {
		return nil
	}
	switch rec.Type {
	case "session_meta":
		var meta sessionMeta
		if err := json.Unmarshal(rec.Payload, &meta); err != nil {
			return nil
		}
		state.ThreadID = meta.ID
		return marshalLine(map[string]any{
			"type":      "thread.started",
			"thread_id": meta.ID,
		})

	case "turn_context":
		// Squirrel away the model name so the synthetic turn.completed
		// reports it correctly. No user-visible event.
		var tc turnContext
		if err := json.Unmarshal(rec.Payload, &tc); err == nil && tc.Model != "" {
			state.Model = tc.Model
		}
		return nil

	case "event_msg":
		var pe sessionEventPayload
		if err := json.Unmarshal(rec.Payload, &pe); err != nil {
			return nil
		}
		switch pe.Type {
		case "task_started":
			return marshalLine(map[string]any{"type": "turn.started"})

		case "agent_message":
			if pe.Message == "" {
				return nil
			}
			return marshalLine(map[string]any{
				"type":    "agent_message",
				"message": pe.Message,
			})

		case "task_complete":
			usage := pe.Info.TotalTokenUsage
			return marshalLine(map[string]any{
				"type":        "turn.completed",
				"model":       state.Model,
				"duration_ms": pe.DurationMS,
				"usage": map[string]any{
					"input_tokens":        usage.InputTokens,
					"output_tokens":       usage.OutputTokens,
					"cached_input_tokens": usage.CachedInputTokens,
				},
			})

		case "token_count":
			// Cumulative usage; we report the final usage at
			// task_complete to avoid emitting many turn.completed
			// look-alikes (parse_stream would treat each as
			// terminal).
			return nil
		}
	}
	return nil
}

// sessionEventPayload is the union of event_msg payloads we translate.
// Fields irrelevant to a given pe.Type stay at zero values.
type sessionEventPayload struct {
	Type               string         `json:"type"`
	Message            string         `json:"message"`
	ModelContextWindow int            `json:"model_context_window"`
	Info               tokenCountInfo `json:"info"`
	DurationMS         int64          `json:"duration_ms"`
	TimeToFirstTokenMS int64          `json:"time_to_first_token_ms"`
	LastAgentMessage   string         `json:"last_agent_message"`
}

func marshalLine(fields map[string]any) []byte {
	b, err := json.Marshal(fields)
	if err != nil {
		return nil
	}
	return append(b, '\n')
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
