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

	for _, name := range []string{"claude", "claude-sonnet", "claude-haiku", "codex", "mock"} {
		if _, ok := cfg.Agents[name]; !ok {
			t.Errorf("expected %q agent in defaults", name)
		}
	}
	if _, ok := cfg.Containers["none"]; !ok {
		t.Error("expected 'none' container in defaults")
	}
	for _, name := range []string{"default", "cheap", "cheapest", "codex", "test"} {
		if _, ok := cfg.Profiles[name]; !ok {
			t.Errorf("expected %q profile in defaults", name)
		}
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

func TestOrgDefaultsOverride(t *testing.T) {
	orgDir := t.TempDir()
	defaultsDir := filepath.Join(orgDir, "defaults")
	if err := os.MkdirAll(defaultsDir, 0755); err != nil {
		t.Fatal(err)
	}

	// org/defaults/runtime.hcl overrides embedded defaults
	defaultsHCL := `
agent "claude" {
  command = "claude"
  args    = ["-p", "--output-format", "stream-json"]
  model   = "sonnet"
}
`
	if err := os.WriteFile(filepath.Join(defaultsDir, "runtime.hcl"), []byte(defaultsHCL), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load("", orgDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ac := cfg.Agents["claude"]
	if ac.Model != "sonnet" {
		t.Errorf("expected model 'sonnet' from org defaults, got %q", ac.Model)
	}
}

func TestOrgDefaultsThenOrgOverride(t *testing.T) {
	orgDir := t.TempDir()
	defaultsDir := filepath.Join(orgDir, "defaults")
	if err := os.MkdirAll(defaultsDir, 0755); err != nil {
		t.Fatal(err)
	}

	// org/defaults sets model to sonnet
	defaultsHCL := `
agent "claude" {
  command = "claude"
  args    = ["-p"]
  model   = "sonnet"
}
`
	// org root overrides model to opus
	orgHCL := `
agent "claude" {
  command = "claude"
  args    = ["-p"]
  model   = "opus"
}
`
	if err := os.WriteFile(filepath.Join(defaultsDir, "runtime.hcl"), []byte(defaultsHCL), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orgDir, "runtime.hcl"), []byte(orgHCL), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load("", orgDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ac := cfg.Agents["claude"]
	if ac.Model != "opus" {
		t.Errorf("expected org override model 'opus', got %q", ac.Model)
	}
}

func TestOrgDefaultsSymlink(t *testing.T) {
	orgDir := t.TempDir()
	defaultsDir := filepath.Join(orgDir, "defaults")
	if err := os.MkdirAll(defaultsDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create the actual file elsewhere
	srcDir := t.TempDir()
	srcHCL := `
agent "claude" {
  command = "claude"
  args    = ["-p"]
  model   = "haiku"
}
`
	srcPath := filepath.Join(srcDir, "runtime.hcl")
	if err := os.WriteFile(srcPath, []byte(srcHCL), 0644); err != nil {
		t.Fatal(err)
	}

	// Symlink from org/defaults/runtime.hcl -> srcPath
	linkPath := filepath.Join(defaultsDir, "runtime.hcl")
	if err := os.Symlink(srcPath, linkPath); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load("", orgDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ac := cfg.Agents["claude"]
	if ac.Model != "haiku" {
		t.Errorf("expected model 'haiku' from symlinked defaults, got %q", ac.Model)
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

func TestCodexAgentConfig(t *testing.T) {
	cfg, err := Load("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ac := cfg.Agents["codex"]
	if ac.Type != "codex" {
		t.Errorf("expected codex agent type 'codex', got %q", ac.Type)
	}
	if ac.Command != "codex" {
		t.Errorf("expected command 'codex', got %q", ac.Command)
	}
}

func TestClaudeAgentEnv(t *testing.T) {
	cfg, err := Load("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ac := cfg.Agents["claude"]
	if ac.Env == nil {
		t.Fatal("expected env map on claude agent, got nil")
	}
	v, ok := ac.Env["CLAUDECODE"]
	if !ok {
		t.Error("expected CLAUDECODE key in claude agent env")
	}
	if v != "" {
		t.Errorf("expected CLAUDECODE to be empty string (unset), got %q", v)
	}
}

func TestCheapProfiles(t *testing.T) {
	cfg, err := Load("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tests := []struct {
		profile string
		agent   string
	}{
		{"cheap", "claude-sonnet"},
		{"cheapest", "claude-haiku"},
	}

	for _, tt := range tests {
		prof, ac, _, err := cfg.ResolveProfile(tt.profile)
		if err != nil {
			t.Errorf("ResolveProfile(%q): %v", tt.profile, err)
			continue
		}
		if prof.Agent != tt.agent {
			t.Errorf("profile %q: expected agent %q, got %q", tt.profile, tt.agent, prof.Agent)
		}
		if ac.Command != "claude" {
			t.Errorf("profile %q agent: expected command 'claude', got %q", tt.profile, ac.Command)
		}
	}
}

func TestOrgOverrideEnv(t *testing.T) {
	dir := t.TempDir()

	hcl := `
agent "claude" {
  command = "claude"
  args    = ["-p", "--verbose"]
  env = {
    CLAUDECODE = ""
    CUSTOM_VAR = "hello"
  }
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
	if ac.Env["CUSTOM_VAR"] != "hello" {
		t.Errorf("expected CUSTOM_VAR=hello, got %q", ac.Env["CUSTOM_VAR"])
	}
	if ac.Env["CLAUDECODE"] != "" {
		t.Errorf("expected CLAUDECODE='', got %q", ac.Env["CLAUDECODE"])
	}
}

func TestBaseInheritance(t *testing.T) {
	cfg, err := Load("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// claude-sonnet inherits from claude via base
	ac := cfg.Agents["claude-sonnet"]
	if ac.Command != "claude" {
		t.Errorf("expected inherited command 'claude', got %q", ac.Command)
	}
	if ac.Env == nil || ac.Env["CLAUDECODE"] != "" {
		t.Errorf("expected inherited env with CLAUDECODE='', got %v", ac.Env)
	}
	if ac.Sandbox != "ateam_claude_sandbox_extra_settings.json" {
		t.Errorf("expected inherited sandbox, got %q", ac.Sandbox)
	}

	// claude-haiku also inherits
	ac2 := cfg.Agents["claude-haiku"]
	if ac2.Sandbox != "ateam_claude_sandbox_extra_settings.json" {
		t.Errorf("expected inherited sandbox for haiku, got %q", ac2.Sandbox)
	}
}

func TestBaseInheritanceOverride(t *testing.T) {
	dir := t.TempDir()

	hcl := `
agent "base-agent" {
  command = "base-cmd"
  model   = "base-model"
  sandbox = "base.json"
  env = {
    FOO = "bar"
  }
}

agent "child-agent" {
  base  = "base-agent"
  model = "child-model"
}
`
	if err := os.WriteFile(filepath.Join(dir, "runtime.hcl"), []byte(hcl), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load("", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	child := cfg.Agents["child-agent"]
	if child.Command != "base-cmd" {
		t.Errorf("expected inherited command 'base-cmd', got %q", child.Command)
	}
	if child.Model != "child-model" {
		t.Errorf("expected overridden model 'child-model', got %q", child.Model)
	}
	if child.Sandbox != "base.json" {
		t.Errorf("expected inherited sandbox 'base.json', got %q", child.Sandbox)
	}
	if child.Env == nil || child.Env["FOO"] != "bar" {
		t.Errorf("expected inherited env FOO=bar, got %v", child.Env)
	}
}

func TestBaseInheritanceCircular(t *testing.T) {
	dir := t.TempDir()

	hcl := `
agent "a" {
  base = "b"
}
agent "b" {
  base = "a"
}
`
	if err := os.WriteFile(filepath.Join(dir, "runtime.hcl"), []byte(hcl), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load("", dir)
	if err == nil {
		t.Error("expected error for circular base reference")
	}
}

func TestSandboxAttribute(t *testing.T) {
	cfg, err := Load("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// claude has sandbox set
	ac := cfg.Agents["claude"]
	if ac.Sandbox != "ateam_claude_sandbox_extra_settings.json" {
		t.Errorf("expected sandbox 'ateam_claude_sandbox_extra_settings.json', got %q", ac.Sandbox)
	}

	// codex has no sandbox
	codex := cfg.Agents["codex"]
	if codex.Sandbox != "" {
		t.Errorf("expected empty sandbox for codex, got %q", codex.Sandbox)
	}

	// mock has no sandbox
	mock := cfg.Agents["mock"]
	if mock.Sandbox != "" {
		t.Errorf("expected empty sandbox for mock, got %q", mock.Sandbox)
	}
}
