package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadWith3LevelFallback(t *testing.T) {
	base := t.TempDir()

	projectPath := filepath.Join(base, "project", "report_prompt.md")
	orgPath := filepath.Join(base, "org", "report_prompt.md")
	defaultPath := filepath.Join(base, "defaults", "report_prompt.md")

	os.MkdirAll(filepath.Dir(projectPath), 0755)
	os.MkdirAll(filepath.Dir(orgPath), 0755)
	os.MkdirAll(filepath.Dir(defaultPath), 0755)

	// Only default exists
	os.WriteFile(defaultPath, []byte("default"), 0644)
	got, err := readWith3LevelFallback(projectPath, orgPath, defaultPath, "test")
	if err != nil {
		t.Fatalf("default only: %v", err)
	}
	if got != "default" {
		t.Errorf("default only: got %q, want %q", got, "default")
	}

	// Org override exists
	os.WriteFile(orgPath, []byte("org"), 0644)
	got, _ = readWith3LevelFallback(projectPath, orgPath, defaultPath, "test")
	if got != "org" {
		t.Errorf("org override: got %q, want %q", got, "org")
	}

	// Project override exists
	os.WriteFile(projectPath, []byte("project"), 0644)
	got, _ = readWith3LevelFallback(projectPath, orgPath, defaultPath, "test")
	if got != "project" {
		t.Errorf("project override: got %q, want %q", got, "project")
	}
}

// setupMinimalAgent creates the minimum structure for AssembleAgentPrompt to work:
// a role prompt file at defaults level.
func setupMinimalAgent(t *testing.T, orgDir, projectDir, agentID string) {
	t.Helper()
	roleDir := filepath.Join(orgDir, "defaults", "agents", agentID)
	os.MkdirAll(roleDir, 0755)
	os.WriteFile(filepath.Join(roleDir, ReportPromptFile), []byte("role prompt"), 0644)

	agentDir := filepath.Join(projectDir, "agents", agentID)
	os.MkdirAll(agentDir, 0755)
}

func TestAssembleAgentPromptIncludesPreviousReport(t *testing.T) {
	base := t.TempDir()
	orgDir := filepath.Join(base, "org")
	projectDir := filepath.Join(base, "project")
	agentID := "security"

	setupMinimalAgent(t, orgDir, projectDir, agentID)

	reportPath := filepath.Join(projectDir, "agents", agentID, FullReportFile)
	os.WriteFile(reportPath, []byte("previous findings here"), 0644)

	result, err := AssembleAgentPrompt(orgDir, projectDir, agentID, base, "", ProjectInfoParams{}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "# Previous Report") {
		t.Error("expected '# Previous Report' section in prompt")
	}
	if !strings.Contains(result, "It might be outdated but it will give you some context") {
		t.Error("expected context instructions in Previous Report header")
	}
	if !strings.Contains(result, "previous findings here") {
		t.Errorf("expected previous report content in prompt, got:\n%s", result)
	}
}

func TestAssembleAgentPromptSkipPreviousReport(t *testing.T) {
	base := t.TempDir()
	orgDir := filepath.Join(base, "org")
	projectDir := filepath.Join(base, "project")
	agentID := "security"

	setupMinimalAgent(t, orgDir, projectDir, agentID)

	reportPath := filepath.Join(projectDir, "agents", agentID, FullReportFile)
	os.WriteFile(reportPath, []byte("previous findings here"), 0644)

	result, err := AssembleAgentPrompt(orgDir, projectDir, agentID, base, "", ProjectInfoParams{}, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "# Previous Report") {
		t.Error("previous report should be excluded when skipPreviousReport=true")
	}
	if strings.Contains(result, "previous findings here") {
		t.Error("previous report content should be excluded when skipPreviousReport=true")
	}
}

func TestAssembleAgentPromptNoPreviousReportFile(t *testing.T) {
	base := t.TempDir()
	orgDir := filepath.Join(base, "org")
	projectDir := filepath.Join(base, "project")
	agentID := "security"

	setupMinimalAgent(t, orgDir, projectDir, agentID)

	// No full_report.md exists — should succeed without "Previous Report"
	result, err := AssembleAgentPrompt(orgDir, projectDir, agentID, base, "", ProjectInfoParams{}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "# Previous Report") {
		t.Error("should not contain Previous Report when no report file exists")
	}
}

func TestReadWith3LevelFallbackNoneExist(t *testing.T) {
	base := t.TempDir()
	_, err := readWith3LevelFallback(
		filepath.Join(base, "a"),
		filepath.Join(base, "b"),
		filepath.Join(base, "c"),
		"test",
	)
	if err == nil {
		t.Fatal("expected error when no files exist")
	}
}
