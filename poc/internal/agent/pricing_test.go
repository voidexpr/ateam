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

	// Known model
	cost := EstimateCost(table, "o4-mini-2025-04-16", "codex-mini", 1000, 500)
	want := 1000*1.10/1e6 + 500*4.40/1e6
	if math.Abs(cost-want) > 1e-12 {
		t.Errorf("EstimateCost(o4-mini, 1000, 500) = %v, want %v", cost, want)
	}

	// Unknown model, falls back to defaultModel
	cost = EstimateCost(table, "unknown-model", "codex-mini", 1000, 500)
	want = 1000*1.50/1e6 + 500*6.00/1e6
	if math.Abs(cost-want) > 1e-12 {
		t.Errorf("EstimateCost(unknown with default) = %v, want %v", cost, want)
	}

	// Unknown model, unknown default
	if got := EstimateCost(table, "unknown-model", "also-unknown", 1000, 500); got != 0 {
		t.Errorf("EstimateCost(unknown, unknown) = %v, want 0", got)
	}

	// Nil table
	if got := EstimateCost(nil, "o4-mini", "codex-mini", 1000, 500); got != 0 {
		t.Errorf("EstimateCost(nil table) = %v, want 0", got)
	}

	// Empty model string
	if got := EstimateCost(table, "", "", 1000, 500); got != 0 {
		t.Errorf("EstimateCost('', '') = %v, want 0", got)
	}
}
