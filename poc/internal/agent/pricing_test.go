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
	cost := EstimateCost("o4-mini-2025-04-16", 1000, 500)
	want := 1000*1.10/1e6 + 500*4.40/1e6
	if math.Abs(cost-want) > 1e-12 {
		t.Errorf("EstimateCost(o4-mini, 1000, 500) = %v, want %v", cost, want)
	}

	if got := EstimateCost("unknown-model", 1000, 500); got != 0 {
		t.Errorf("EstimateCost(unknown) = %v, want 0", got)
	}

	if got := EstimateCost("", 1000, 500); got != 0 {
		t.Errorf("EstimateCost('') = %v, want 0", got)
	}
}
