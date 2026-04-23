package agent

import (
	"context"
	"os/exec"
	"testing"
)

// shellFactory returns a CmdFactory that runs the given shell snippet
// via /bin/sh -c, so tests can inject an arbitrary JSONL stream and
// exit status through real OS pipes.
func shellFactory(script string) func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
	return func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "/bin/sh", "-c", script)
	}
}

const fakeClaudeTwoTurnsNoResult = `
printf '{"type":"system","subtype":"init","session_id":"s1","model":"claude-test-model"}\n'
printf '{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":100,"output_tokens":20,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}\n'
printf '{"type":"assistant","message":{"content":[{"type":"text","text":"more"}],"usage":{"input_tokens":50,"output_tokens":30,"cache_creation_input_tokens":0,"cache_read_input_tokens":200}}}\n'
exit 1
`

func TestClaudeAgentAccumulatesTokensOnErrorExit(t *testing.T) {
	pricing := PricingTable{
		"claude-test-model": {InputPerToken: 0.001, OutputPerToken: 0.002},
	}
	a := &ClaudeAgent{
		Command:      "claude",
		DefaultModel: "claude-test-model",
		Pricing:      pricing,
	}

	ch := a.Run(context.Background(), Request{
		Prompt:     "test",
		CmdFactory: shellFactory(fakeClaudeTwoTurnsNoResult),
	})

	var assistantSeen int
	var lastAssistant StreamEvent
	var final StreamEvent
	for ev := range ch {
		switch ev.Type {
		case "assistant":
			assistantSeen++
			// Running totals must be monotonically non-decreasing across
			// assistant events.
			if ev.InputTokens < lastAssistant.InputTokens {
				t.Errorf("InputTokens regressed: %d -> %d", lastAssistant.InputTokens, ev.InputTokens)
			}
			if ev.OutputTokens < lastAssistant.OutputTokens {
				t.Errorf("OutputTokens regressed: %d -> %d", lastAssistant.OutputTokens, ev.OutputTokens)
			}
			lastAssistant = ev
		case "error":
			final = ev
		}
	}

	if assistantSeen != 2 {
		t.Fatalf("expected 2 assistant events, got %d", assistantSeen)
	}
	if lastAssistant.InputTokens != 150 {
		t.Errorf("assistant cumulative InputTokens = %d, want 150", lastAssistant.InputTokens)
	}
	if lastAssistant.OutputTokens != 50 {
		t.Errorf("assistant cumulative OutputTokens = %d, want 50", lastAssistant.OutputTokens)
	}
	// Cost: 150*0.001 + 50*0.002 = 0.25
	if lastAssistant.Cost < 0.249 || lastAssistant.Cost > 0.251 {
		t.Errorf("assistant estimated Cost = %f, want ~0.25", lastAssistant.Cost)
	}
	if lastAssistant.Model != "claude-test-model" {
		t.Errorf("assistant Model = %q, want claude-test-model", lastAssistant.Model)
	}

	if final.Type != "error" {
		t.Fatalf("expected terminal error event, got %+v", final)
	}
	if final.InputTokens != 150 || final.OutputTokens != 50 {
		t.Errorf("error tokens = (%d,%d), want (150,50)", final.InputTokens, final.OutputTokens)
	}
	if final.CacheReadTokens != 200 {
		t.Errorf("error CacheReadTokens = %d, want 200", final.CacheReadTokens)
	}
	if final.Cost < 0.249 || final.Cost > 0.251 {
		t.Errorf("error Cost = %f, want ~0.25", final.Cost)
	}
	if final.Model != "claude-test-model" {
		t.Errorf("error Model = %q, want claude-test-model", final.Model)
	}
	if final.ErrorSource != ErrorSourceAgentProcess {
		t.Errorf("error source = %q, want agent_process", final.ErrorSource)
	}
}

// TestClaudeAgentEstimateNoPricingConfig simulates an old runtime.hcl
// that has no pricing block for the claude agent — Pricing is nil AND
// DefaultModel is empty. The run must not panic and tokens must still
// be captured (cost falls back to 0).
func TestClaudeAgentEstimateNoPricingConfig(t *testing.T) {
	a := &ClaudeAgent{
		Command: "claude",
		// Pricing nil, DefaultModel "", Model "" — old-config scenario.
	}

	ch := a.Run(context.Background(), Request{
		Prompt:     "test",
		CmdFactory: shellFactory(fakeClaudeTwoTurnsNoResult),
	})

	var final StreamEvent
	for ev := range ch {
		if ev.Type == "error" {
			final = ev
		}
	}
	if final.Type != "error" {
		t.Fatal("expected error event")
	}
	if final.InputTokens != 150 || final.OutputTokens != 50 {
		t.Errorf("tokens = (%d,%d), want (150,50)", final.InputTokens, final.OutputTokens)
	}
	if final.Cost != 0 {
		t.Errorf("Cost with no pricing config = %f, want 0", final.Cost)
	}
	// Model comes from the stream's system event even without config.
	if final.Model != "claude-test-model" {
		t.Errorf("Model = %q, want claude-test-model (from system event)", final.Model)
	}
}

// TestClaudeAgentEstimateUnknownModelGivesZeroCost covers the case
// where pricing IS configured, but the runtime model (from the stream
// or config) isn't in the table — e.g. a new model released after the
// last runtime.hcl update, with no defaultModel fallback.
func TestClaudeAgentEstimateUnknownModelGivesZeroCost(t *testing.T) {
	a := &ClaudeAgent{
		Command: "claude",
		// Table has a different model; no defaultModel match either.
		Pricing: PricingTable{
			"some-other-model": {InputPerToken: 0.001, OutputPerToken: 0.002},
		},
	}

	ch := a.Run(context.Background(), Request{
		Prompt:     "test",
		CmdFactory: shellFactory(fakeClaudeTwoTurnsNoResult),
	})

	var final StreamEvent
	for ev := range ch {
		if ev.Type == "error" {
			final = ev
		}
	}
	if final.Type != "error" {
		t.Fatal("expected error event")
	}
	if final.InputTokens != 150 || final.OutputTokens != 50 {
		t.Errorf("tokens = (%d,%d), want (150,50)", final.InputTokens, final.OutputTokens)
	}
	if final.Cost != 0 {
		t.Errorf("Cost with unknown model = %f, want 0", final.Cost)
	}
}

func TestClaudeAgentEstimateNilPricingGivesZeroCost(t *testing.T) {
	a := &ClaudeAgent{
		Command:      "claude",
		DefaultModel: "claude-test-model",
		// Pricing left nil
	}

	ch := a.Run(context.Background(), Request{
		Prompt:     "test",
		CmdFactory: shellFactory(fakeClaudeTwoTurnsNoResult),
	})

	var final StreamEvent
	for ev := range ch {
		if ev.Type == "error" {
			final = ev
		}
	}

	if final.Type != "error" {
		t.Fatal("expected error event")
	}
	if final.InputTokens != 150 || final.OutputTokens != 50 {
		t.Errorf("tokens = (%d,%d), want (150,50)", final.InputTokens, final.OutputTokens)
	}
	if final.Cost != 0 {
		t.Errorf("Cost with nil pricing = %f, want 0", final.Cost)
	}
}
