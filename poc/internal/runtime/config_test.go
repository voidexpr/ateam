package runtime

import (
	"os"
	"path/filepath"
	"strings"
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
		profile       string
		wantExtraArgs []string
	}{
		{"cheap", []string{"--model", "sonnet", "--max-budget-usd", "0.50"}},
		{"cheapest", []string{"--model", "haiku", "--max-budget-usd", "0.10"}},
	}

	for _, tt := range tests {
		prof, ac, _, err := cfg.ResolveProfile(tt.profile)
		if err != nil {
			t.Errorf("ResolveProfile(%q): %v", tt.profile, err)
			continue
		}
		if prof.Agent != "claude" {
			t.Errorf("profile %q: expected agent 'claude', got %q", tt.profile, prof.Agent)
		}
		if ac.Command != "claude" {
			t.Errorf("profile %q agent: expected command 'claude', got %q", tt.profile, ac.Command)
		}
		if len(prof.AgentExtraArgs) != len(tt.wantExtraArgs) {
			t.Errorf("profile %q: expected %d agent_extra_args, got %d: %v", tt.profile, len(tt.wantExtraArgs), len(prof.AgentExtraArgs), prof.AgentExtraArgs)
		}
		for i, arg := range prof.AgentExtraArgs {
			if i < len(tt.wantExtraArgs) && arg != tt.wantExtraArgs[i] {
				t.Errorf("profile %q: extra arg %d: expected %q, got %q", tt.profile, i, tt.wantExtraArgs[i], arg)
			}
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
	if ac.Sandbox == "" || !strings.Contains(ac.Sandbox, `"permissions"`) {
		t.Errorf("expected inherited sandbox JSON content, got empty or wrong content")
	}

	// claude-haiku also inherits sandbox
	ac2 := cfg.Agents["claude-haiku"]
	if ac2.Sandbox == "" {
		t.Error("expected inherited sandbox for haiku, got empty")
	}
}

func TestBaseInheritanceOverride(t *testing.T) {
	dir := t.TempDir()

	hcl := `
agent "base-agent" {
  command = "base-cmd"
  model   = "base-model"
  sandbox = "base-settings"
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
	if child.Sandbox != "base-settings" {
		t.Errorf("expected inherited sandbox 'base-settings', got %q", child.Sandbox)
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

	// claude has inline sandbox JSON
	ac := cfg.Agents["claude"]
	if ac.Sandbox == "" {
		t.Fatal("expected non-empty sandbox on claude agent")
	}
	if !strings.Contains(ac.Sandbox, `"permissions"`) {
		t.Errorf("expected sandbox to contain permissions JSON, got %q", ac.Sandbox[:min(len(ac.Sandbox), 80)])
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

func TestSandboxPaths(t *testing.T) {
	dir := t.TempDir()

	hcl := `
agent "test-agent" {
  command      = "test"
  rw_paths     = ["/data/output", "/tmp/scratch"]
  ro_paths     = ["/data/input"]
  denied_paths = ["/etc/secrets"]
}
`
	if err := os.WriteFile(filepath.Join(dir, "runtime.hcl"), []byte(hcl), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load("", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ac := cfg.Agents["test-agent"]
	if len(ac.RWPaths) != 2 || ac.RWPaths[0] != "/data/output" {
		t.Errorf("expected rw_paths [/data/output /tmp/scratch], got %v", ac.RWPaths)
	}
	if len(ac.ROPaths) != 1 || ac.ROPaths[0] != "/data/input" {
		t.Errorf("expected ro_paths [/data/input], got %v", ac.ROPaths)
	}
	if len(ac.DeniedPaths) != 1 || ac.DeniedPaths[0] != "/etc/secrets" {
		t.Errorf("expected denied_paths [/etc/secrets], got %v", ac.DeniedPaths)
	}
}

func TestSandboxPathsInheritance(t *testing.T) {
	dir := t.TempDir()

	hcl := `
agent "parent" {
  command      = "test"
  rw_paths     = ["/data/rw"]
  ro_paths     = ["/data/ro"]
  denied_paths = ["/data/denied"]
}

agent "child" {
  base = "parent"
}

agent "child-override" {
  base     = "parent"
  rw_paths = ["/override/rw"]
}
`
	if err := os.WriteFile(filepath.Join(dir, "runtime.hcl"), []byte(hcl), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load("", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// child inherits all paths
	child := cfg.Agents["child"]
	if len(child.RWPaths) != 1 || child.RWPaths[0] != "/data/rw" {
		t.Errorf("expected inherited rw_paths, got %v", child.RWPaths)
	}
	if len(child.ROPaths) != 1 || child.ROPaths[0] != "/data/ro" {
		t.Errorf("expected inherited ro_paths, got %v", child.ROPaths)
	}
	if len(child.DeniedPaths) != 1 || child.DeniedPaths[0] != "/data/denied" {
		t.Errorf("expected inherited denied_paths, got %v", child.DeniedPaths)
	}

	// child-override replaces rw_paths but inherits ro_paths and denied_paths
	co := cfg.Agents["child-override"]
	if len(co.RWPaths) != 1 || co.RWPaths[0] != "/override/rw" {
		t.Errorf("expected overridden rw_paths, got %v", co.RWPaths)
	}
	if len(co.ROPaths) != 1 || co.ROPaths[0] != "/data/ro" {
		t.Errorf("expected inherited ro_paths, got %v", co.ROPaths)
	}
}

func TestLocals(t *testing.T) {
	dir := t.TempDir()

	hcl := `
locals {
  my_sandbox = <<-EOF
  {"permissions": {"allow": ["Read"]}}
  EOF
}

agent "test-agent" {
  command = "test"
  sandbox = local.my_sandbox
}

container "none" {
  type = "none"
}

profile "test" {
  agent     = "test-agent"
  container = "none"
}
`
	if err := os.WriteFile(filepath.Join(dir, "runtime.hcl"), []byte(hcl), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load("", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ac := cfg.Agents["test-agent"]
	if !strings.Contains(ac.Sandbox, `"permissions"`) {
		t.Errorf("expected sandbox from local, got %q", ac.Sandbox)
	}
}

func TestLocalsDefaultsSandbox(t *testing.T) {
	cfg, err := Load("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ac := cfg.Agents["claude"]
	if ac.Sandbox == "" {
		t.Fatal("expected non-empty sandbox on claude agent (from locals)")
	}
	if !strings.Contains(ac.Sandbox, `"permissions"`) {
		t.Errorf("expected sandbox JSON with permissions, got %q", ac.Sandbox[:min(len(ac.Sandbox), 80)])
	}
}

func TestProfileAgentExtraArgs(t *testing.T) {
	dir := t.TempDir()

	hcl := `
agent "base" {
  command = "test-cmd"
  args    = ["--verbose"]
}

container "none" {
  type = "none"
}

profile "with-extras" {
  agent            = "base"
  container        = "none"
  agent_extra_args = ["--model", "fast", "--budget", "1.00"]
}

profile "no-extras" {
  agent     = "base"
  container = "none"
}
`
	if err := os.WriteFile(filepath.Join(dir, "runtime.hcl"), []byte(hcl), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load("", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	prof := cfg.Profiles["with-extras"]
	if len(prof.AgentExtraArgs) != 4 {
		t.Fatalf("expected 4 agent_extra_args, got %d: %v", len(prof.AgentExtraArgs), prof.AgentExtraArgs)
	}
	if prof.AgentExtraArgs[0] != "--model" || prof.AgentExtraArgs[1] != "fast" {
		t.Errorf("unexpected agent_extra_args: %v", prof.AgentExtraArgs)
	}

	noExtras := cfg.Profiles["no-extras"]
	if len(noExtras.AgentExtraArgs) != 0 {
		t.Errorf("expected no agent_extra_args, got %v", noExtras.AgentExtraArgs)
	}
}

func TestProfileContainerExtraArgs(t *testing.T) {
	dir := t.TempDir()

	hcl := `
agent "mock" {
  type = "builtin"
}

container "docker" {
  type = "docker"
}

profile "docker-profile" {
  agent              = "mock"
  container          = "docker"
  container_extra_args = ["--cpus", "2"]
}
`
	if err := os.WriteFile(filepath.Join(dir, "runtime.hcl"), []byte(hcl), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load("", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	prof := cfg.Profiles["docker-profile"]
	if len(prof.ContainerExtraArgs) != 2 || prof.ContainerExtraArgs[0] != "--cpus" {
		t.Errorf("expected container_extra_args [--cpus 2], got %v", prof.ContainerExtraArgs)
	}
}

func TestConfigDir(t *testing.T) {
	cfg, err := Load("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ac := cfg.Agents["claude-isolated"]
	if ac.ConfigDir != ".claude" {
		t.Errorf("expected config_dir '.claude', got %q", ac.ConfigDir)
	}
	// Should inherit sandbox from claude base
	if ac.Sandbox == "" {
		t.Error("expected inherited sandbox for claude-isolated")
	}
	if ac.Command != "claude" {
		t.Errorf("expected inherited command 'claude', got %q", ac.Command)
	}
}

func TestConfigDirInheritance(t *testing.T) {
	dir := t.TempDir()

	hcl := `
agent "parent" {
  command    = "test"
  config_dir = ".isolated"
}

agent "child" {
  base = "parent"
}

agent "child-override" {
  base       = "parent"
  config_dir = ".other"
}
`
	if err := os.WriteFile(filepath.Join(dir, "runtime.hcl"), []byte(hcl), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load("", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	child := cfg.Agents["child"]
	if child.ConfigDir != ".isolated" {
		t.Errorf("expected inherited config_dir '.isolated', got %q", child.ConfigDir)
	}

	co := cfg.Agents["child-override"]
	if co.ConfigDir != ".other" {
		t.Errorf("expected overridden config_dir '.other', got %q", co.ConfigDir)
	}
}

func TestIsolatedProfile(t *testing.T) {
	cfg, err := Load("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	prof, ok := cfg.Profiles["isolated"]
	if !ok {
		t.Fatal("expected 'isolated' profile in defaults")
	}
	if prof.Agent != "claude-isolated" {
		t.Errorf("expected agent 'claude-isolated', got %q", prof.Agent)
	}
}
