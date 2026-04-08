package prompts

import (
	"os"
	"path/filepath"
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

func TestTraceRolePromptSourcesIncludesRolePrompt(t *testing.T) {
	base := t.TempDir()
	orgDir := filepath.Join(base, "org")
	projectDir := filepath.Join(base, "project")
	roleID := "security"

	roleDir := filepath.Join(orgDir, "defaults", "roles", roleID)
	if err := os.MkdirAll(roleDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(roleDir, ReportPromptFile), []byte("role prompt content"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(projectDir, "roles", roleID), 0755); err != nil {
		t.Fatal(err)
	}

	sources := TraceRolePromptSources(orgDir, projectDir, roleID, base, "", ProjectInfoParams{}, false)

	if len(sources) == 0 {
		t.Fatal("expected at least one source, got none")
	}

	var found bool
	for _, s := range sources {
		if s.Content == "role prompt content" {
			found = true
		}
	}
	if !found {
		t.Errorf("role prompt content not found in sources: %v", sources)
	}
}

func TestTraceRolePromptSourcesExtraPromptIsLast(t *testing.T) {
	base := t.TempDir()
	orgDir := filepath.Join(base, "org")
	projectDir := filepath.Join(base, "project")
	roleID := "testing_basic"

	roleDir := filepath.Join(orgDir, "defaults", "roles", roleID)
	os.MkdirAll(roleDir, 0755)
	os.WriteFile(filepath.Join(roleDir, ReportPromptFile), []byte("base content"), 0644)
	os.MkdirAll(filepath.Join(projectDir, "roles", roleID), 0755)

	sources := TraceRolePromptSources(orgDir, projectDir, roleID, base, "my extra prompt", ProjectInfoParams{}, false)

	if len(sources) == 0 {
		t.Fatal("expected sources, got none")
	}
	last := sources[len(sources)-1]
	if last.Label != "CLI: --extra-prompt" {
		t.Errorf("expected last source label 'CLI: --extra-prompt', got %q", last.Label)
	}
	if last.Content != "my extra prompt" {
		t.Errorf("expected extra content 'my extra prompt', got %q", last.Content)
	}
}

func TestTraceRolePromptSourcesSkipPreviousReport(t *testing.T) {
	base := t.TempDir()
	orgDir := filepath.Join(base, "org")
	projectDir := filepath.Join(base, "project")
	roleID := "security"

	roleDir := filepath.Join(orgDir, "defaults", "roles", roleID)
	os.MkdirAll(roleDir, 0755)
	os.WriteFile(filepath.Join(roleDir, ReportPromptFile), []byte("role content"), 0644)

	projRoleDir := filepath.Join(projectDir, "roles", roleID)
	os.MkdirAll(projRoleDir, 0755)
	reportPath := filepath.Join(projRoleDir, ReportFile)
	os.WriteFile(reportPath, []byte("previous report"), 0644)

	// skipPreviousReport=true: report file should not appear in sources.
	sources := TraceRolePromptSources(orgDir, projectDir, roleID, base, "", ProjectInfoParams{}, true)
	for _, s := range sources {
		if s.Path == reportPath {
			t.Error("expected previous report excluded when skipPreviousReport=true")
		}
	}

	// skipPreviousReport=false: report file should appear in sources.
	sources = TraceRolePromptSources(orgDir, projectDir, roleID, base, "", ProjectInfoParams{}, false)
	var found bool
	for _, s := range sources {
		if s.Path == reportPath {
			found = true
		}
	}
	if !found {
		t.Error("expected previous report included when skipPreviousReport=false")
	}
}
