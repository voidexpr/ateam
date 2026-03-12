package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load("", "")
	if err != nil {
		t.Fatalf("unexpected error loading defaults: %v", err)
	}

	if _, ok := cfg.Agents["claude"]; !ok {
		t.Error("expected 'claude' agent in defaults")
	}
	if _, ok := cfg.Agents["mock"]; !ok {
		t.Error("expected 'mock' agent in defaults")
	}
	if _, ok := cfg.Containers["none"]; !ok {
		t.Error("expected 'none' container in defaults")
	}
	if _, ok := cfg.Profiles["default"]; !ok {
		t.Error("expected 'default' profile in defaults")
	}
	if _, ok := cfg.Profiles["test"]; !ok {
		t.Error("expected 'test' profile in defaults")
	}
}

func TestLoadDefaultProfile(t *testing.T) {
	cfg, err := Load("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	prof := cfg.Profiles["default"]
	if prof.Agent != "claude" {
		t.Errorf("expected default profile agent 'claude', got %q", prof.Agent)
	}
	if prof.Container != "none" {
		t.Errorf("expected default profile container 'none', got %q", prof.Container)
	}
}

func TestLoadTestProfile(t *testing.T) {
	cfg, err := Load("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	prof := cfg.Profiles["test"]
	if prof.Agent != "mock" {
		t.Errorf("expected test profile agent 'mock', got %q", prof.Agent)
	}
}

func TestResolveProfile(t *testing.T) {
	cfg, err := Load("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	prof, ac, cc, err := cfg.ResolveProfile("default")
	if err != nil {
		t.Fatalf("unexpected error resolving profile: %v", err)
	}
	if prof.Name != "default" {
		t.Errorf("expected profile name 'default', got %q", prof.Name)
	}
	if ac.Name != "claude" {
		t.Errorf("expected agent name 'claude', got %q", ac.Name)
	}
	if cc.Name != "none" {
		t.Errorf("expected container name 'none', got %q", cc.Name)
	}
}

func TestResolveUnknownProfile(t *testing.T) {
	cfg, err := Load("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, _, _, err = cfg.ResolveProfile("nonexistent")
	if err == nil {
		t.Error("expected error for unknown profile")
	}
}

func TestOrgOverride(t *testing.T) {
	dir := t.TempDir()

	hcl := `
agent "claude" {
  command = "custom-claude"
  args    = ["-p", "--verbose"]
  model   = "opus"
}
`
	if err := os.WriteFile(filepath.Join(dir, "runtime.hcl"), []byte(hcl), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load("", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ac := cfg.Agents["claude"]
	if ac.Command != "custom-claude" {
		t.Errorf("expected command 'custom-claude', got %q", ac.Command)
	}
	if ac.Model != "opus" {
		t.Errorf("expected model 'opus', got %q", ac.Model)
	}
	if len(ac.Args) != 2 || ac.Args[0] != "-p" {
		t.Errorf("unexpected args: %v", ac.Args)
	}
}

func TestProjectOverride(t *testing.T) {
	orgDir := t.TempDir()
	projDir := t.TempDir()

	orgHCL := `
profile "custom" {
  agent     = "claude"
  container = "none"
}
`
	projHCL := `
profile "custom" {
  agent     = "mock"
  container = "none"
}
`
	if err := os.WriteFile(filepath.Join(orgDir, "runtime.hcl"), []byte(orgHCL), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, "runtime.hcl"), []byte(projHCL), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(projDir, orgDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Project-level should override org-level
	prof := cfg.Profiles["custom"]
	if prof.Agent != "mock" {
		t.Errorf("expected project override to set agent 'mock', got %q", prof.Agent)
	}
}

func TestClaudeAgentConfig(t *testing.T) {
	cfg, err := Load("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ac := cfg.Agents["claude"]
	if ac.Command != "claude" {
		t.Errorf("expected command 'claude', got %q", ac.Command)
	}
	if len(ac.Args) < 2 {
		t.Errorf("expected claude to have base args, got %v", ac.Args)
	}
}

func TestMockAgentConfig(t *testing.T) {
	cfg, err := Load("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ac := cfg.Agents["mock"]
	if ac.Type != "builtin" {
		t.Errorf("expected mock agent type 'builtin', got %q", ac.Type)
	}
}
