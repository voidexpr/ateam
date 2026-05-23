package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestTranslateSessionLineEachVariant locks in the mapping from codex
// session log records to codex-exec-stream JSONL records. Each subtest
// verifies one variant.
func TestTranslateSessionLineEachVariant(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want map[string]any
	}{
		{
			name: "session_meta -> thread.started",
			in:   `{"type":"session_meta","payload":{"id":"abc-123","cwd":"/x","timestamp":"2026-05-22T18:00:00Z","originator":"codex-tui","cli_version":"0.133.0"}}`,
			want: map[string]any{"type": "thread.started", "thread_id": "abc-123"},
		},
		{
			name: "event_msg task_started -> turn.started",
			in:   `{"type":"event_msg","payload":{"type":"task_started","model_context_window":258400}}`,
			want: map[string]any{"type": "turn.started"},
		},
		{
			name: "event_msg agent_message -> agent_message",
			in:   `{"type":"event_msg","payload":{"type":"agent_message","message":"Looking at the diff…"}}`,
			want: map[string]any{"type": "agent_message", "message": "Looking at the diff…"},
		},
		{
			name: "event_msg task_complete -> turn.completed with usage",
			in:   `{"type":"event_msg","payload":{"type":"task_complete","duration_ms":12345,"info":{"total_token_usage":{"input_tokens":1000,"output_tokens":50,"cached_input_tokens":200,"total_tokens":1050}}}}`,
			want: map[string]any{
				"type":        "turn.completed",
				"model":       "gpt-5.5",
				"duration_ms": float64(12345),
				"usage": map[string]any{
					"input_tokens":        float64(1000),
					"output_tokens":       float64(50),
					"cached_input_tokens": float64(200),
				},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// task_complete needs `state.Model` to be set; prime via a
			// preceding turn_context (the real wire order codex uses).
			state := tailState{}
			translateSessionLine([]byte(`{"type":"turn_context","payload":{"model":"gpt-5.5"}}`), &state)
			out := translateSessionLine([]byte(tc.in), &state)
			if out == nil {
				t.Fatalf("translator returned nil for %s", tc.name)
			}
			var got map[string]any
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("not JSON: %v\n%s", err, out)
			}
			for k, want := range tc.want {
				if !equalLoose(got[k], want) {
					t.Errorf("%s: got[%q] = %v, want %v\nfull: %v", tc.name, k, got[k], want, got)
				}
			}
		})
	}
}

// TestTranslateSessionLineSkipsNoise verifies records that shouldn't
// surface to ateam tail are dropped (token_count, response_item, unknown
// types).
func TestTranslateSessionLineSkipsNoise(t *testing.T) {
	noise := []string{
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":99}}}}`,
		`{"type":"event_msg","payload":{"type":"user_message","message":"hi"}}`,
		`{"type":"response_item","payload":{"type":"reasoning"}}`,
		`{"type":"unknown_record","payload":{}}`,
		`malformed json`,
	}
	state := tailState{}
	for _, n := range noise {
		if out := translateSessionLine([]byte(n), &state); out != nil {
			t.Errorf("expected nil for %q, got %s", n, out)
		}
	}
}

// TestTailSessionLogEndToEnd: write a multi-event rollout file while a
// TailSessionLog goroutine is running, and collect the translated events.
// Verifies (a) we discover the file via cwd+marker matching, (b) lines
// written after we started tailing get picked up, (c) events come out in
// codex-exec-stream shape.
func TestTailSessionLogEndToEnd(t *testing.T) {
	home := t.TempDir()
	workdir := t.TempDir()
	day := time.Now().UTC()
	sessions := filepath.Join(home, "sessions", day.Format("2006"), day.Format("01"), day.Format("02"))
	if err := os.MkdirAll(sessions, 0700); err != nil {
		t.Fatal(err)
	}
	rolloutPath := filepath.Join(sessions, "rollout-"+day.Format("2006-01-02T15-04-05")+"-test.jsonl")

	// Start the tailer in the background. Marker matches the agent_message
	// we'll write, exercising the disambiguation path too.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var (
		mu       sync.Mutex
		received [][]byte
	)
	tailDone := make(chan struct{})
	go func() {
		TailSessionLog(ctx, home, workdir, time.Now().Add(-time.Second), "[ateam-exec-99]", func(line []byte) {
			mu.Lock()
			received = append(received, append([]byte(nil), line...))
			mu.Unlock()
		})
		close(tailDone)
	}()

	// Write the rollout file in three appended writes — exercises the
	// "open + read EOF + sleep + read more" cycle in TailSessionLog.
	writeAppend := func(s string) {
		f, err := os.OpenFile(rolloutPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			t.Fatalf("open append: %v", err)
		}
		if _, err := f.WriteString(s); err != nil {
			t.Fatalf("write: %v", err)
		}
		_ = f.Close()
	}

	// 1) session_meta with workdir-matching cwd and our EXEC marker in
	//    the user_message so FindSessionLog's marker filter selects it.
	writeAppend(`{"timestamp":"` + day.Format(time.RFC3339Nano) + `","type":"session_meta","payload":{"id":"sess-99","cwd":"` + workdir + `","timestamp":"` + day.Format(time.RFC3339Nano) + `","originator":"codex-tui","cli_version":"0.133.0"}}` + "\n")
	writeAppend(`{"type":"event_msg","payload":{"type":"user_message","message":"Please look [ateam-exec-99]"}}` + "\n")

	// Give tailer a chance to discover + open + read up to here.
	time.Sleep(900 * time.Millisecond)

	// 2) turn_context (model) and agent_message — must surface as
	//    turn-started shape later.
	writeAppend(`{"type":"turn_context","payload":{"model":"gpt-5.5"}}` + "\n")
	writeAppend(`{"type":"event_msg","payload":{"type":"task_started"}}` + "\n")
	writeAppend(`{"type":"event_msg","payload":{"type":"agent_message","message":"Hello world."}}` + "\n")

	time.Sleep(900 * time.Millisecond)

	// 3) task_complete with usage.
	writeAppend(`{"type":"event_msg","payload":{"type":"task_complete","duration_ms":12345,"info":{"total_token_usage":{"input_tokens":100,"output_tokens":7,"cached_input_tokens":40}}}}` + "\n")

	time.Sleep(900 * time.Millisecond)
	cancel()
	<-tailDone

	mu.Lock()
	defer mu.Unlock()

	// Expected: thread.started + turn.started + agent_message + turn.completed.
	// turn_context is internal-only; user_message and the leading
	// timestamp lines are dropped.
	if len(received) < 4 {
		t.Fatalf("got %d events, want >=4:\n%s", len(received), joinLines(received))
	}
	types := make([]string, 0, len(received))
	for _, ln := range received {
		var ev map[string]any
		_ = json.Unmarshal(ln, &ev)
		types = append(types, asString(ev["type"]))
	}
	want := []string{"thread.started", "turn.started", "agent_message", "turn.completed"}
	for _, w := range want {
		found := false
		for _, ty := range types {
			if ty == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing %q in emitted types: %v\n%s", w, types, joinLines(received))
		}
	}
}

func joinLines(bs [][]byte) string {
	parts := make([]string, len(bs))
	for i, b := range bs {
		parts[i] = string(b)
	}
	return strings.Join(parts, "")
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func equalLoose(a, b any) bool {
	// json.Unmarshal turns nested objects into map[string]any too.
	if am, aok := a.(map[string]any); aok {
		bm, bok := b.(map[string]any)
		if !bok || len(am) != len(bm) {
			return false
		}
		for k, av := range am {
			if !equalLoose(av, bm[k]) {
				return false
			}
		}
		return true
	}
	// nil/missing comparisons
	if a == nil && b == nil {
		return true
	}
	return a == b
}
