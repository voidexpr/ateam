package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMockAgentName(t *testing.T) {
	m := &MockAgent{}
	if m.Name() != "mock" {
		t.Fatalf("expected name 'mock', got %q", m.Name())
	}
}

func TestMockAgentEmitsEvents(t *testing.T) {
	m := &MockAgent{Response: "hello world", Cost: 0.05}

	dir := t.TempDir()
	streamFile := filepath.Join(dir, "stream.jsonl")

	req := Request{
		Prompt:     "test prompt",
		StreamFile: streamFile,
	}

	events := m.Run(context.Background(), req)

	var types []string
	var resultOutput string
	for ev := range events {
		types = append(types, ev.Type)
		if ev.Type == "result" {
			resultOutput = ev.Output
		}
	}

	if len(types) != 3 {
		t.Fatalf("expected 3 events, got %d: %v", len(types), types)
	}
	if types[0] != "system" {
		t.Errorf("expected first event 'system', got %q", types[0])
	}
	if types[1] != "assistant" {
		t.Errorf("expected second event 'assistant', got %q", types[1])
	}
	if types[2] != "result" {
		t.Errorf("expected third event 'result', got %q", types[2])
	}
	if resultOutput != "hello world" {
		t.Errorf("expected output 'hello world', got %q", resultOutput)
	}

	// Verify stream file was written
	data, err := os.ReadFile(streamFile)
	if err != nil {
		t.Fatalf("cannot read stream file: %v", err)
	}
	if len(data) == 0 {
		t.Error("stream file is empty")
	}
}

func TestMockAgentRecordsRequests(t *testing.T) {
	m := &MockAgent{Response: "ok"}

	req := Request{Prompt: "prompt 1"}
	for range m.Run(context.Background(), req) {
	}

	req2 := Request{Prompt: "prompt 2"}
	for range m.Run(context.Background(), req2) {
	}

	if len(m.Requests) != 2 {
		t.Fatalf("expected 2 recorded requests, got %d", len(m.Requests))
	}
	if m.Requests[0].Prompt != "prompt 1" {
		t.Errorf("expected first prompt 'prompt 1', got %q", m.Requests[0].Prompt)
	}
	if m.Requests[1].Prompt != "prompt 2" {
		t.Errorf("expected second prompt 'prompt 2', got %q", m.Requests[1].Prompt)
	}
}

func TestMockAgentError(t *testing.T) {
	m := &MockAgent{Err: errors.New("simulated failure")}

	events := m.Run(context.Background(), Request{Prompt: "fail"})

	var lastType string
	var lastErr error
	for ev := range events {
		lastType = ev.Type
		if ev.Err != nil {
			lastErr = ev.Err
		}
	}

	if lastType != "error" {
		t.Errorf("expected last event type 'error', got %q", lastType)
	}
	if lastErr == nil || lastErr.Error() != "simulated failure" {
		t.Errorf("expected 'simulated failure' error, got %v", lastErr)
	}
}

func TestMockAgentContextCancellation(t *testing.T) {
	m := &MockAgent{Response: "slow", Delay: 5 * time.Second}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	events := m.Run(ctx, Request{Prompt: "cancel me"})

	var gotError bool
	for ev := range events {
		if ev.Type == "error" {
			gotError = true
		}
	}

	if !gotError {
		t.Error("expected error event from cancelled context")
	}
}
