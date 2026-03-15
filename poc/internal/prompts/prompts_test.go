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

// setupMinimalRole creates the minimum structure for AssembleRolePrompt to work:
// a role prompt file at defaults level.
func setupMinimalRole(t *testing.T, orgDir, projectDir, roleID string) {
	t.Helper()
	roleDir := filepath.Join(orgDir, "defaults", "roles", roleID)
	os.MkdirAll(roleDir, 0755)
	os.WriteFile(filepath.Join(roleDir, ReportPromptFile), []byte("role prompt"), 0644)

	roleProjectDir := filepath.Join(projectDir, "roles", roleID)
	os.MkdirAll(roleProjectDir, 0755)
}

func TestAssembleRolePromptIncludesPreviousReport(t *testing.T) {
	base := t.TempDir()
	orgDir := filepath.Join(base, "org")
	projectDir := filepath.Join(base, "project")
	roleID := "security"

	setupMinimalRole(t, orgDir, projectDir, roleID)

	reportPath := filepath.Join(projectDir, "roles", roleID, ReportFile)
	os.WriteFile(reportPath, []byte("previous findings here"), 0644)

	result, err := AssembleRolePrompt(orgDir, projectDir, roleID, base, "", ProjectInfoParams{}, false)
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

func TestAssembleRolePromptSkipPreviousReport(t *testing.T) {
	base := t.TempDir()
	orgDir := filepath.Join(base, "org")
	projectDir := filepath.Join(base, "project")
	roleID := "security"

	setupMinimalRole(t, orgDir, projectDir, roleID)

	reportPath := filepath.Join(projectDir, "roles", roleID, ReportFile)
	os.WriteFile(reportPath, []byte("previous findings here"), 0644)

	result, err := AssembleRolePrompt(orgDir, projectDir, roleID, base, "", ProjectInfoParams{}, true)
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

func TestAssembleRolePromptNoPreviousReportFile(t *testing.T) {
	base := t.TempDir()
	orgDir := filepath.Join(base, "org")
	projectDir := filepath.Join(base, "project")
	roleID := "security"

	setupMinimalRole(t, orgDir, projectDir, roleID)

	// No report.md exists — should succeed without "Previous Report"
	result, err := AssembleRolePrompt(orgDir, projectDir, roleID, base, "", ProjectInfoParams{}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "# Previous Report") {
		t.Error("should not contain Previous Report when no report file exists")
	}
}

func TestEnabledRoleIDsAllowlist(t *testing.T) {
	configRoles := map[string]string{
		"alpha":   "on",
		"beta":    "off",
		"gamma":   "enabled",
		"delta":   "disabled",
		"epsilon": "weird_value",
	}
	allKnown := []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta"}

	got := enabledRoleIDs(configRoles, allKnown)

	// alpha: "on" → included; beta: "off" → excluded; gamma: "enabled" → included;
	// delta: "disabled" → excluded; epsilon: "weird_value" → excluded (allowlist);
	// zeta: not in configRoles → included (enabled by default).
	want := map[string]bool{"alpha": true, "gamma": true, "zeta": true}
	if len(got) != len(want) {
		t.Fatalf("enabledRoleIDs returned %v, want keys %v", got, want)
	}
	for _, id := range got {
		if !want[id] {
			t.Errorf("enabledRoleIDs included unexpected role %q", id)
		}
	}
}

func TestEnabledRoleIDsNilConfig(t *testing.T) {
	allKnown := []string{"a", "b", "c"}
	got := enabledRoleIDs(nil, allKnown)
	if len(got) != len(allKnown) {
		t.Errorf("enabledRoleIDs(nil) = %v, want all %v", got, allKnown)
	}
}

func TestResolveRoleListAllExpansionUsesAllowlist(t *testing.T) {
	configRoles := map[string]string{
		"security":   "on",
		"automation": "off",
	}
	// "all" should expand only to roles with status "on" or "enabled", plus
	// embedded roles not listed in configRoles (which default to enabled).
	roles, err := ResolveRoleList([]string{"all"}, configRoles)
	if err != nil {
		t.Fatalf("ResolveRoleList: %v", err)
	}
	for _, r := range roles {
		if r == "automation" {
			t.Errorf("'automation' (status 'off') should not appear in 'all' expansion")
		}
	}
	found := false
	for _, r := range roles {
		if r == "security" {
			found = true
		}
	}
	if !found {
		t.Errorf("'security' (status 'on') should appear in 'all' expansion")
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
