package prompts

import (
	"testing"
)

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"a", 1},                // (1+3)/4 = 1
		{"abcd", 1},             // (4+3)/4 = 1
		{"abcde", 2},            // (5+3)/4 = 2
		{"12345678", 2},         // (8+3)/4 = 2
		{"123456789", 3},        // (9+3)/4 = 3
		{"1234567890123456", 4}, // (16+3)/4 = 4
	}
	for _, tt := range tests {
		got := EstimateTokens(tt.input)
		if got != tt.want {
			t.Errorf("EstimateTokens(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestDisplayPathNonFile(t *testing.T) {
	s := PromptSource{Label: "CLI: --extra-prompt"}
	if got := s.DisplayPath(); got != "CLI: --extra-prompt" {
		t.Errorf("DisplayPath() = %q, want %q", got, "CLI: --extra-prompt")
	}
}

func TestDisplayPathAteamorg(t *testing.T) {
	s := PromptSource{Path: "/home/user/.ateamorg/defaults/report_base_prompt.md"}
	want := ".ateamorg/defaults/report_base_prompt.md"
	if got := s.DisplayPath(); got != want {
		t.Errorf("DisplayPath() = %q, want %q", got, want)
	}
}

func TestDisplayPathAteam(t *testing.T) {
	s := PromptSource{Path: "/projects/myapp/.ateam/roles/security/report.md"}
	want := ".ateam/roles/security/report.md"
	if got := s.DisplayPath(); got != want {
		t.Errorf("DisplayPath() = %q, want %q", got, want)
	}
}

func TestDisplayPathAbsoluteOther(t *testing.T) {
	s := PromptSource{Path: "/some/other/path.md"}
	if got := s.DisplayPath(); got != "/some/other/path.md" {
		t.Errorf("DisplayPath() = %q, want %q", got, "/some/other/path.md")
	}
}
