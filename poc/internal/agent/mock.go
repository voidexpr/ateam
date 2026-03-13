package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"sync"
	"time"
)

// MockAgent is a test agent that returns canned responses.
type MockAgent struct {
	Response string
	Cost     float64
	Err      error
	Delay    time.Duration

	mu       sync.Mutex
	Requests []Request
}

func (m *MockAgent) Name() string { return "mock" }

func (m *MockAgent) DebugCommandArgs(extraArgs []string) (string, []string) {
	return "mock", nil
}

func (m *MockAgent) Run(ctx context.Context, req Request) <-chan StreamEvent {
	m.mu.Lock()
	m.Requests = append(m.Requests, req)
	m.mu.Unlock()

	ch := make(chan StreamEvent, 8)
	go m.run(ctx, req, ch)
	return ch
}

func (m *MockAgent) run(ctx context.Context, req Request, ch chan<- StreamEvent) {
	defer close(ch)

	if m.Delay > 0 {
		select {
		case <-time.After(m.Delay):
		case <-ctx.Done():
			ch <- StreamEvent{Type: "error", Err: ctx.Err(), ExitCode: -1}
			return
		}
	}

	if m.Err != nil {
		ch <- StreamEvent{Type: "error", Err: m.Err, ExitCode: 1}
		return
	}

	startedAt := time.Now()

	ch <- StreamEvent{Type: "system", SessionID: "mock-session"}

	response := m.Response
	if response == "" {
		response = "mock response"
	}

	ch <- StreamEvent{Type: "assistant", Text: response}

	// Write valid claude-format JSONL to stream file for FormatStream compatibility
	if req.StreamFile != "" {
		m.writeStreamFile(req.StreamFile, response)
	}

	ch <- StreamEvent{
		Type:        "result",
		Output:      response,
		Cost:        m.Cost,
		Turns:       1,
		DurationMS:  time.Since(startedAt).Milliseconds(),
		InputTokens: 100,
		OutputTokens: 50,
	}
}

// writeStreamFile writes claude-compatible JSONL so FormatStream can read it.
func (m *MockAgent) writeStreamFile(path string, response string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	defer w.Flush()

	writeLine := func(v any) {
		data, _ := json.Marshal(v)
		w.Write(data)
		w.WriteByte('\n')
	}

	writeLine(map[string]any{
		"type":       "system",
		"subtype":    "init",
		"session_id": "mock-session",
	})

	writeLine(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": response},
			},
		},
	})

	writeLine(map[string]any{
		"type":           "result",
		"total_cost_usd": m.Cost,
		"cost_usd":       m.Cost,
		"duration_ms":    1,
		"num_turns":      1,
		"is_error":       false,
		"usage": map[string]any{
			"input_tokens":            100,
			"output_tokens":           50,
			"cache_read_input_tokens": 0,
		},
	})
}
