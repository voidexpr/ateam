package agent

import (
	"math"
	"testing"
)

func TestNormalizeModel(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"o4-mini", "o4-mini"},
		{"o4-mini-2025-04-16", "o4-mini"},
		{"claude-sonnet-4-2025-01-15", "claude-sonnet-4"},
		{"gpt-4.1", "gpt-4.1"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := NormalizeModel(tt.input); got != tt.want {
			t.Errorf("NormalizeModel(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestEstimateCost(t *testing.T) {
	table := PricingTable{
		"o4-mini":    {InputPerToken: 1.10 / 1e6, OutputPerToken: 4.40 / 1e6},
		"codex-mini": {InputPerToken: 1.50 / 1e6, OutputPerToken: 6.00 / 1e6},
	}

	// Known model, no cached tokens
	cost := EstimateCost(table, "o4-mini-2025-04-16", "codex-mini", 1000, 0, 500)
	want := 1000*1.10/1e6 + 500*4.40/1e6
	if math.Abs(cost-want) > 1e-12 {
		t.Errorf("EstimateCost(o4-mini, 1000, 0, 500) = %v, want %v", cost, want)
	}

	// Unknown model, falls back to defaultModel
	cost = EstimateCost(table, "unknown-model", "codex-mini", 1000, 0, 500)
	want = 1000*1.50/1e6 + 500*6.00/1e6
	if math.Abs(cost-want) > 1e-12 {
		t.Errorf("EstimateCost(unknown with default) = %v, want %v", cost, want)
	}

	// Unknown model, unknown default
	if got := EstimateCost(table, "unknown-model", "also-unknown", 1000, 0, 500); got != 0 {
		t.Errorf("EstimateCost(unknown, unknown) = %v, want 0", got)
	}

	// Nil table
	if got := EstimateCost(nil, "o4-mini", "codex-mini", 1000, 0, 500); got != 0 {
		t.Errorf("EstimateCost(nil table) = %v, want 0", got)
	}

	// Empty model string
	if got := EstimateCost(table, "", "", 1000, 0, 500); got != 0 {
		t.Errorf("EstimateCost('', '') = %v, want 0", got)
	}
}

func TestContextWindow(t *testing.T) {
	tests := []struct {
		model string
		want  int
	}{
		// Codex / OpenAI families added alongside the cache-aware pricing.
		{"gpt-5", 400000},
		{"gpt-5-codex", 400000},
		{"gpt-5.3-codex", 400000},
		{"gpt-4o", 128000},
		{"gpt-4o-mini", 128000},
		// Precise OpenAI-published value, not the round 1M.
		{"gpt-4.1", 1047576},
		{"gpt-4.1-mini", 1047576},
		{"o3", 200000},
		{"o4-mini", 200000},
		// Date-suffix normalization should still hit the table.
		{"o4-mini-2025-04-16", 200000},
		// Unknown / empty.
		{"", 0},
		{"unknown-model", 0},
	}
	for _, tt := range tests {
		if got := ContextWindow(tt.model); got != tt.want {
			t.Errorf("ContextWindow(%q) = %d, want %d", tt.model, got, tt.want)
		}
	}
}

func TestEstimateCostCached(t *testing.T) {
	// Model with explicit cached rate (10x discount, like OpenAI's prompt cache).
	table := PricingTable{
		"gpt-5": {
			InputPerToken:       1.25 / 1e6,
			CachedInputPerToken: 0.125 / 1e6,
			OutputPerToken:      10.00 / 1e6,
		},
		// Model without a cached rate — cached tokens charged at full input.
		"legacy": {InputPerToken: 1.00 / 1e6, OutputPerToken: 2.00 / 1e6},
	}

	// 1000 input total, 800 cached, 100 output.
	cost := EstimateCost(table, "gpt-5", "", 1000, 800, 100)
	want := 200*1.25/1e6 + 800*0.125/1e6 + 100*10.00/1e6
	if math.Abs(cost-want) > 1e-12 {
		t.Errorf("EstimateCost(gpt-5 cached) = %v, want %v", cost, want)
	}

	// Model without cached rate falls back to InputPerToken — equivalent to
	// the pre-cache-aware behavior so historical pricing files keep working.
	cost = EstimateCost(table, "legacy", "", 1000, 800, 100)
	want = 1000*1.00/1e6 + 100*2.00/1e6
	if math.Abs(cost-want) > 1e-12 {
		t.Errorf("EstimateCost(legacy no-cache-rate) = %v, want %v", cost, want)
	}

	// Defensive clamp: cached > input shouldn't make uncached negative.
	cost = EstimateCost(table, "gpt-5", "", 100, 200, 0)
	want = 100 * 0.125 / 1e6
	if math.Abs(cost-want) > 1e-12 {
		t.Errorf("EstimateCost(cached>input clamped) = %v, want %v", cost, want)
	}
}
