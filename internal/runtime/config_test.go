package runtime

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("unexpected error loading defaults: %v", err)
	}

	for _, name := range []string{"claude", "claude-sonnet", "claude-haiku", "codex", "codex-tmux", "mock"} {
		if _, ok := cfg.Agents[name]; !ok {
			t.Errorf("expected %q agent in defaults", name)
		}
	}
	for _, name := range []string{"none", "docker"} {
		if _, ok := cfg.Containers[name]; !ok {
			t.Errorf("expected %q container in defaults", name)
		}
	}
	for _, name := range []string{"default", "cheap", "cheapest", "docker", "codex", "codex-tmux", "test"} {
		if _, ok := cfg.Profiles[name]; !ok {
			t.Errorf("expected %q profile in defaults", name)
		}
	}
}

// TestDefaultClaudeAgentsShareHeadlessEnv pins the env that keeps headless
// claude sessions from backgrounding long commands (see local.claude_env in
// defaults/ateam.hcl) on every claude agent, whether it inherits via
// base = "claude" or references the local directly.
func TestDefaultClaudeAgentsShareHeadlessEnv(t *testing.T) {
	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("unexpected error loading defaults: %v", err)
	}
	want := map[string]string{
		"CLAUDECODE":                           "",
		"CLAUDE_CODE_DISABLE_BACKGROUND_TASKS": "1",
	}
	for _, name := range []string{"claude", "claude-sonnet", "claude-haiku", "claude-auto", "claude-no-sandbox"} {
		ac, ok := cfg.Agents[name]
		if !ok {
			t.Errorf("expected %q agent in defaults", name)
			continue
		}
		for k, v := range want {
			if got, ok := ac.Env[k]; !ok || got != v {
				t.Errorf("agent %q env[%s] = %q (present=%v), want %q", name, k, got, ok, v)
			}
		}
	}
}

func TestLoadDefaultProfile(t *testing.T) {
	cfg, err := Load(nil)
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
	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	prof := cfg.Profiles["test"]
	if prof.Agent != "mock" {
		t.Errorf("expected test profile agent 'mock', got %q", prof.Agent)
	}
}

func TestResolveProfile(t *testing.T) {
	cfg, err := Load(nil)
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
	cfg, err := Load(nil)
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
	if err := os.WriteFile(filepath.Join(dir, ConfigFile), []byte(hcl), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load([]string{dir})
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

// org/defaults/runtime.hcl overrides embedded defaults

// org/defaults sets model to sonnet

// org root overrides model to opus

// Create the actual file elsewhere

// Symlink from org/defaults/runtime.hcl -> srcPath

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
	if err := os.WriteFile(filepath.Join(orgDir, ConfigFile), []byte(orgHCL), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, ConfigFile), []byte(projHCL), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load([]string{orgDir, projDir})
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
	cfg, err := Load(nil)
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
	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ac := cfg.Agents["mock"]
	if ac.Type != "builtin" {
		t.Errorf("expected mock agent type 'builtin', got %q", ac.Type)
	}
}

func TestCodexAgentConfig(t *testing.T) {
	cfg, err := Load(nil)
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

func TestCodexTmuxAgentConfig(t *testing.T) {
	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ac := cfg.Agents["codex-tmux"]
	if ac.Type != "codex-tmux" {
		t.Errorf("expected codex-tmux agent type 'codex-tmux', got %q", ac.Type)
	}
	if ac.Command != "codex" {
		t.Errorf("expected command 'codex', got %q", ac.Command)
	}
	if ac.Model != "gpt-5.5" {
		t.Errorf("expected model 'gpt-5.5', got %q", ac.Model)
	}
	if ac.Effort != "xhigh" {
		t.Errorf("expected effort 'xhigh', got %q", ac.Effort)
	}
	wantArgs := []string{"--no-alt-screen", "-s", "workspace-write", "-a", "never", "-c", "check_for_update_on_startup=false", "--disable", "apps", "--disable", "plugins"}
	if !slices.Equal(ac.Args, wantArgs) {
		t.Errorf("unexpected codex-tmux args: got %v want %v", ac.Args, wantArgs)
	}
	if ac.StartTimeout != "15s" || ac.BusyTimeout != "20m" || ac.QuiescenceWindow != "2s" {
		t.Errorf("unexpected timeouts: start=%q busy=%q quiescence=%q", ac.StartTimeout, ac.BusyTimeout, ac.QuiescenceWindow)
	}
	if ac.TmuxWidth != 300 || ac.TmuxHeight != 100 {
		t.Errorf("unexpected tmux size: %dx%d", ac.TmuxWidth, ac.TmuxHeight)
	}
	if len(ac.RequiredEnv) != 0 {
		t.Errorf("expected codex-tmux not to require env by default, got %v", ac.RequiredEnv)
	}
	if len(ac.ArgsOutsideContainer) != 0 {
		t.Errorf("expected codex-tmux not to inherit exec-only outside args, got %v", ac.ArgsOutsideContainer)
	}
	if ac.Pricing == nil || ac.Pricing.DefaultModel != "gpt-5.4" {
		t.Errorf("expected codex-tmux to inherit codex pricing, got %+v", ac.Pricing)
	}
}

func TestClaudeAgentEnv(t *testing.T) {
	cfg, err := Load(nil)
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
	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tests := []struct {
		profile, agent, model, budget string
	}{
		{"cheap", "claude-sonnet", "sonnet", "0.50"},
		{"cheapest", "claude-haiku", "haiku", "0.10"},
	}

	for _, tt := range tests {
		prof, ac, _, err := cfg.ResolveProfile(tt.profile)
		if err != nil {
			t.Errorf("ResolveProfile(%q): %v", tt.profile, err)
			continue
		}
		if prof.Agent != tt.agent || len(prof.AgentExtraArgs) != 0 {
			t.Errorf("profile %q: got agent %q extra args %v, want agent %q and no extra args", tt.profile, prof.Agent, prof.AgentExtraArgs, tt.agent)
		}
		if ac.Command != "claude" || ac.Model != tt.model || ac.MaxBudgetUSD != tt.budget {
			t.Errorf("profile %q agent: got command=%q model=%q budget=%q", tt.profile, ac.Command, ac.Model, ac.MaxBudgetUSD)
		}
		if embeddedModelFlag(ac.Args) != "" {
			t.Errorf("profile %q agent: built-in args must not embed a model flag: %v", tt.profile, ac.Args)
		}
	}
}

func TestBuiltinAgentsHaveNoModelInArgs(t *testing.T) {
	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for name, ac := range cfg.Agents {
		if tok := embeddedModelFlag(ac.Args); tok != "" {
			t.Errorf("agent %q embeds %s in args", name, tok)
		}
	}
	for name, p := range cfg.Profiles {
		if tok := embeddedModelFlag(p.AgentExtraArgs); tok != "" {
			t.Errorf("profile %q embeds %s in agent_extra_args", name, tok)
		}
	}
	if cfg.ExpensiveOutputPerMTok == nil || *cfg.ExpensiveOutputPerMTok <= 0 {
		t.Errorf("expected a positive built-in expensive_output_per_mtok, got %v", cfg.ExpensiveOutputPerMTok)
	}
}

func TestModelWarnings(t *testing.T) {
	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	warningsFor := func(profile, cliModel string) []string {
		t.Helper()
		prof, ac, _, err := cfg.ResolveProfile(profile)
		if err != nil {
			t.Fatalf("ResolveProfile(%q): %v", profile, err)
		}
		return cfg.ModelWarnings(prof, ac, cliModel)
	}
	joined := func(ws []string) string { return strings.Join(ws, "\n") }

	// default profile: claude with nothing pinned
	ws := warningsFor("default", "")
	if len(ws) != 1 || !strings.Contains(ws[0], "no model pinned") || !strings.Contains(ws[0], `agent "claude"`) {
		t.Errorf("default profile: got %q", joined(ws))
	}
	// --model on the CLI silences the unpinned warning; haiku is cheap
	if ws := warningsFor("default", "haiku"); len(ws) != 0 {
		t.Errorf("default + --model haiku: got %q", joined(ws))
	}
	// --model fable is above the threshold
	ws = warningsFor("default", "claude-fable-5-1")
	if len(ws) != 1 || !strings.Contains(ws[0], "$50.00") || !strings.Contains(ws[0], "--model") {
		t.Errorf("default + --model fable: got %q", joined(ws))
	}
	// cheap: sonnet pinned in the built-in layer, under the threshold
	if ws := warningsFor("cheap", ""); len(ws) != 0 {
		t.Errorf("cheap: got %q", joined(ws))
	}
	// mock agent: nothing to say
	if ws := warningsFor("test", ""); len(ws) != 0 {
		t.Errorf("test: got %q", joined(ws))
	}
}

func TestModelWarningsFromLayers(t *testing.T) {
	orgDir := writeLayer(t, t.TempDir(), `
expensive_output_per_mtok = 15

agent "pinned" {
  base  = "claude"
  model = "claude-opus-5-5"
}

profile "pinned" {
  agent     = "pinned"
  container = "none"
}

agent "legacy" {
  base = "claude"
  args = ["-p", "--model", "opus"]
}

profile "legacy" {
  agent            = "legacy"
  container        = "none"
  agent_extra_args = ["--effort", "high"]
}
`)
	projDir := t.TempDir()
	cfg, err := Load([]string{orgDir, projDir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	orgFile := filepath.Join(orgDir, ConfigFile)

	prof, ac, _, _ := cfg.ResolveProfile("pinned")
	ws := cfg.ModelWarnings(prof, ac, "")
	if len(ws) != 1 || !strings.Contains(ws[0], "$20.00") || !strings.Contains(ws[0], "expensive_output_per_mtok = 15") || !strings.Contains(ws[0], orgFile) {
		t.Errorf("pinned: got %q", strings.Join(ws, "\n"))
	}

	prof, ac, _, _ = cfg.ResolveProfile("legacy")
	ws = cfg.ModelWarnings(prof, ac, "")
	if len(ws) != 3 {
		t.Fatalf("legacy: expected args, extra_args and price warnings, got %q", strings.Join(ws, "\n"))
	}
	if !strings.Contains(ws[0], "args embed --model") || !strings.Contains(ws[0], "model attribute") {
		t.Errorf("legacy args warning: %q", ws[0])
	}
	if !strings.Contains(ws[1], "agent_extra_args embed --effort") || !strings.Contains(ws[1], "effort attribute") {
		t.Errorf("legacy extra args warning: %q", ws[1])
	}
	if !strings.Contains(ws[2], `"opus"`) {
		t.Errorf("legacy price warning should name the args model: %q", ws[2])
	}

	// Disabled threshold: no price warning, unpinned still warns.
	writeLayer(t, projDir, `
expensive_output_per_mtok = 0

agent "unpinned" {
  base = "claude-no-sandbox"
}

profile "unpinned" {
  agent     = "unpinned"
  container = "none"
}
`)
	cfg, err = Load([]string{orgDir, projDir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	prof, ac, _, _ = cfg.ResolveProfile("pinned")
	if ws := cfg.ModelWarnings(prof, ac, ""); len(ws) != 0 {
		t.Errorf("threshold 0: got %q", strings.Join(ws, "\n"))
	}
	prof, ac, _, _ = cfg.ResolveProfile("unpinned")
	ws = cfg.ModelWarnings(prof, ac, "")
	if len(ws) != 1 || !strings.Contains(ws[0], "no model pinned") || !strings.Contains(ws[0], filepath.Join(projDir, ConfigFile)) {
		t.Errorf("unpinned: got %q", strings.Join(ws, "\n"))
	}
}

func TestEditHintPointsAtNearestLayer(t *testing.T) {
	projDir := t.TempDir()
	cfg, err := Load([]string{projDir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	hint := cfg.editHint("claude")
	if !strings.Contains(hint, filepath.Join(projDir, ConfigFile)) || !strings.Contains(hint, "create it") {
		t.Errorf("hint with no user layers should point at the missing project file: %q", hint)
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
	if err := os.WriteFile(filepath.Join(dir, ConfigFile), []byte(hcl), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load([]string{dir})
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
	cfg, err := Load(nil)
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
	if err := os.WriteFile(filepath.Join(dir, ConfigFile), []byte(hcl), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load([]string{dir})
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

func TestEffortFieldRoundTripAndInherits(t *testing.T) {
	dir := t.TempDir()

	hcl := `
agent "base-agent" {
  command = "base-cmd"
  effort  = "high"
}

agent "child-inherit" {
  base = "base-agent"
}

agent "child-override" {
  base   = "base-agent"
  effort = "low"
}
`
	if err := os.WriteFile(filepath.Join(dir, ConfigFile), []byte(hcl), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load([]string{dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := cfg.Agents["base-agent"].Effort; got != "high" {
		t.Errorf("base effort: got %q, want %q", got, "high")
	}
	if got := cfg.Agents["child-inherit"].Effort; got != "high" {
		t.Errorf("child-inherit effort: got %q, want inherited %q", got, "high")
	}
	if got := cfg.Agents["child-override"].Effort; got != "low" {
		t.Errorf("child-override effort: got %q, want %q", got, "low")
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
	if err := os.WriteFile(filepath.Join(dir, ConfigFile), []byte(hcl), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load([]string{dir})
	if err == nil {
		t.Error("expected error for circular base reference")
	}
}

func TestSandboxAttribute(t *testing.T) {
	cfg, err := Load(nil)
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
	if err := os.WriteFile(filepath.Join(dir, ConfigFile), []byte(hcl), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load([]string{dir})
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
	if err := os.WriteFile(filepath.Join(dir, ConfigFile), []byte(hcl), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load([]string{dir})
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
	if err := os.WriteFile(filepath.Join(dir, ConfigFile), []byte(hcl), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load([]string{dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ac := cfg.Agents["test-agent"]
	if !strings.Contains(ac.Sandbox, `"permissions"`) {
		t.Errorf("expected sandbox from local, got %q", ac.Sandbox)
	}
}

func TestLocalsDefaultsSandbox(t *testing.T) {
	cfg, err := Load(nil)
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
	if err := os.WriteFile(filepath.Join(dir, ConfigFile), []byte(hcl), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load([]string{dir})
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
	if err := os.WriteFile(filepath.Join(dir, ConfigFile), []byte(hcl), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load([]string{dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	prof := cfg.Profiles["docker-profile"]
	if len(prof.ContainerExtraArgs) != 2 || prof.ContainerExtraArgs[0] != "--cpus" {
		t.Errorf("expected container_extra_args [--cpus 2], got %v", prof.ContainerExtraArgs)
	}
}

func TestConfigDir(t *testing.T) {
	cfg, err := Load(nil)
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
	if err := os.WriteFile(filepath.Join(dir, ConfigFile), []byte(hcl), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load([]string{dir})
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

func TestDockerContainerConfig(t *testing.T) {
	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cc, ok := cfg.Containers["docker"]
	if !ok {
		t.Fatal("expected 'docker' container in defaults")
	}
	if cc.Type != "docker" {
		t.Errorf("expected type 'docker', got %q", cc.Type)
	}
	if cc.Dockerfile != "Dockerfile" {
		t.Errorf("expected dockerfile 'Dockerfile', got %q", cc.Dockerfile)
	}
	if len(cc.ForwardEnv) < 2 {
		t.Errorf("expected at least 2 forward_env entries, got %v", cc.ForwardEnv)
	}
	hasKey := false
	for _, e := range cc.ForwardEnv {
		if e == "ANTHROPIC_API_KEY" {
			hasKey = true
		}
	}
	if !hasKey {
		t.Errorf("expected ANTHROPIC_API_KEY in forward_env, got %v", cc.ForwardEnv)
	}
}

func TestPricingBlockParsed(t *testing.T) {
	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Codex should have pricing from embedded defaults
	codex := cfg.Agents["codex"]
	if codex.Pricing == nil {
		t.Fatal("expected non-nil pricing on codex agent")
	}
	if codex.Pricing.DefaultModel != "gpt-5.4" {
		t.Errorf("expected default_model 'gpt-5.4', got %q", codex.Pricing.DefaultModel)
	}
	if len(codex.Pricing.Models) == 0 {
		t.Fatal("expected non-empty pricing models on codex")
	}
	cm, ok := codex.Pricing.Models["gpt-5.4"]
	if !ok {
		t.Fatal("expected 'gpt-5.4' in codex pricing models")
	}
	if cm.InputPerMTok != 1.75 {
		t.Errorf("expected gpt-5.4 input_per_mtok 1.75, got %v", cm.InputPerMTok)
	}
	if cm.OutputPerMTok != 14.00 {
		t.Errorf("expected gpt-5.4 output_per_mtok 14.00, got %v", cm.OutputPerMTok)
	}

	// Claude reports native cost via total_cost_usd; its pricing block
	// serves the expensive-model warning and must not assume a default model.
	claude := cfg.Agents["claude"]
	if claude.Pricing == nil || claude.Pricing.DefaultModel != "" {
		t.Errorf("expected claude pricing without default_model, got %+v", claude.Pricing)
	}
	if claude.Pricing.Models["opus"].OutputPerMTok != claude.Pricing.Models["claude-opus-5-5"].OutputPerMTok {
		t.Errorf("alias opus must price like the current opus generation")
	}
}

func TestPricingInheritance(t *testing.T) {
	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// claude-sonnet inherits the claude pricing table; claude has no
	// default_model on purpose (an unpinned model must not look pinned).
	cs := cfg.Agents["claude-sonnet"]
	if cs.Pricing == nil || cs.Pricing.DefaultModel != "" || cs.Pricing.Models["sonnet"].OutputPerMTok == 0 {
		t.Errorf("expected inherited claude pricing without default_model, got %+v", cs.Pricing)
	}

	// claude-no-sandbox has no base and no pricing block
	cd := cfg.Agents["claude-no-sandbox"]
	if cd.Pricing != nil {
		t.Errorf("expected nil pricing on claude-no-sandbox, got %+v", cd.Pricing)
	}
}

func TestPricingInheritanceOverride(t *testing.T) {
	dir := t.TempDir()

	hcl := `
agent "parent" {
  command = "test"

  pricing {
    default_model = "parent-model"

    model "parent-model" {
      input_per_mtok  = 10.00
      output_per_mtok = 20.00
    }
  }
}

agent "child" {
  base = "parent"
}

agent "child-override" {
  base = "parent"

  pricing {
    default_model = "child-model"

    model "child-model" {
      input_per_mtok  = 1.00
      output_per_mtok = 2.00
    }
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, ConfigFile), []byte(hcl), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load([]string{dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// child inherits parent pricing
	child := cfg.Agents["child"]
	if child.Pricing == nil {
		t.Fatal("expected child to inherit pricing from parent")
	}
	if child.Pricing.DefaultModel != "parent-model" {
		t.Errorf("expected inherited default_model 'parent-model', got %q", child.Pricing.DefaultModel)
	}

	// child-override replaces parent pricing
	co := cfg.Agents["child-override"]
	if co.Pricing == nil {
		t.Fatal("expected child-override to have pricing")
	}
	if co.Pricing.DefaultModel != "child-model" {
		t.Errorf("expected overridden default_model 'child-model', got %q", co.Pricing.DefaultModel)
	}
	if len(co.Pricing.Models) != 1 {
		t.Errorf("expected 1 model in child-override pricing, got %d", len(co.Pricing.Models))
	}
}

func TestMockPricingBlock(t *testing.T) {
	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mock := cfg.Agents["mock"]
	if mock.Pricing == nil {
		t.Fatal("expected non-nil pricing on mock agent")
	}
	if mock.Pricing.DefaultModel != "mock-default" {
		t.Errorf("expected default_model 'mock-default', got %q", mock.Pricing.DefaultModel)
	}
	mp, ok := mock.Pricing.Models["mock-default"]
	if !ok {
		t.Fatal("expected 'mock-default' in mock pricing models")
	}
	if mp.InputPerMTok != 1.00 {
		t.Errorf("expected input_per_mtok 1.00, got %v", mp.InputPerMTok)
	}
	if mp.OutputPerMTok != 2.00 {
		t.Errorf("expected output_per_mtok 2.00, got %v", mp.OutputPerMTok)
	}
}

func TestDockerProfile(t *testing.T) {
	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	prof, ok := cfg.Profiles["docker"]
	if !ok {
		t.Fatal("expected 'docker' profile in defaults")
	}
	if prof.Agent != "claude" {
		t.Errorf("expected agent 'claude', got %q", prof.Agent)
	}
	if prof.Container != "docker" {
		t.Errorf("expected container 'docker', got %q", prof.Container)
	}

	_, _, cc, err := cfg.ResolveProfile("docker")
	if err != nil {
		t.Fatalf("ResolveProfile(docker): %v", err)
	}
	if cc.Type != "docker" {
		t.Errorf("expected resolved container type 'docker', got %q", cc.Type)
	}
}

func TestContainerPrecheckField(t *testing.T) {
	dir := t.TempDir()

	_ = os.WriteFile(filepath.Join(dir, ConfigFile), []byte(`
agent "mock" {
  type    = "builtin"
  command = "mock"
}
container "docker" {
  type       = "docker"
  dockerfile = "Dockerfile"
  precheck   = ["sh", "my-precheck.sh"]
}
profile "docker" {
  agent     = "mock"
  container = "docker"
}
`), 0644)

	cfg, err := Load([]string{dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cc := cfg.Containers["docker"]
	want := []string{"sh", "my-precheck.sh"}
	if !slices.Equal(cc.Precheck, want) {
		t.Errorf("expected precheck %v, got %v", want, cc.Precheck)
	}
}

func TestContainerPrecheckOverride(t *testing.T) {
	dir := t.TempDir()
	orgDir := filepath.Join(dir, "org")
	projectDir := filepath.Join(dir, "project")
	_ = os.MkdirAll(orgDir, 0755)
	_ = os.MkdirAll(projectDir, 0755)

	// Org defines precheck
	_ = os.WriteFile(filepath.Join(orgDir, ConfigFile), []byte(`
container "docker" {
  type     = "docker"
  precheck = ["sh", "org-precheck.sh"]
}
`), 0644)

	// Project overrides it
	_ = os.WriteFile(filepath.Join(projectDir, ConfigFile), []byte(`
container "docker" {
  type     = "docker"
  precheck = ["sh", "project-precheck.sh"]
}
`), 0644)

	cfg, err := Load(layersOf(projectDir, orgDir))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cc := cfg.Containers["docker"]
	want := []string{"sh", "project-precheck.sh"}
	if !slices.Equal(cc.Precheck, want) {
		t.Errorf("expected precheck %v, got %v", want, cc.Precheck)
	}
}

func TestResolvePrecheckCmdExplicit(t *testing.T) {
	dir := t.TempDir()
	projectDir := filepath.Join(dir, "project")
	_ = os.MkdirAll(projectDir, 0755)

	// Bare command names like "sh" are not resolved relative to projectDir
	cc := &ContainerConfig{Precheck: []string{"sh", "custom-precheck.sh"}}
	got := ResolvePrecheckCmd(cc, layersOf(projectDir, ""), "")
	want := []string{"sh", "custom-precheck.sh"}
	if !slices.Equal(got, want) {
		t.Errorf("expected %v, got %v", want, got)
	}
}

func TestResolvePrecheckCmdExplicitRelativePath(t *testing.T) {
	dir := t.TempDir()
	projectDir := filepath.Join(dir, "project")
	_ = os.MkdirAll(projectDir, 0755)

	// Paths with separators are resolved relative to projectDir
	cc := &ContainerConfig{Precheck: []string{"scripts/precheck.sh", "--verbose"}}
	got := ResolvePrecheckCmd(cc, layersOf(projectDir, ""), "")
	want := []string{filepath.Join(projectDir, "scripts/precheck.sh"), "--verbose"}
	if !slices.Equal(got, want) {
		t.Errorf("expected %v, got %v", want, got)
	}
}

func TestResolvePrecheckCmdExplicitAbsolute(t *testing.T) {
	cc := &ContainerConfig{Precheck: []string{"/usr/local/bin/check", "--verify"}}
	got := ResolvePrecheckCmd(cc, layersOf("/some/project", ""), "")
	want := []string{"/usr/local/bin/check", "--verify"}
	if !slices.Equal(got, want) {
		t.Errorf("expected %v, got %v", want, got)
	}
}

func TestResolvePrecheckCmdMakeCommand(t *testing.T) {
	cc := &ContainerConfig{Precheck: []string{"make", "docker-restart"}}
	got := ResolvePrecheckCmd(cc, layersOf("/some/project", ""), "")
	want := []string{"make", "docker-restart"}
	if !slices.Equal(got, want) {
		t.Errorf("expected %v, got %v", want, got)
	}
}

func TestResolvePrecheckCmdWithContainerNameTemplate(t *testing.T) {
	cc := &ContainerConfig{Precheck: []string{"sh", "precheck.sh", "{{CONTAINER_NAME}}"}}
	got := ResolvePrecheckCmd(cc, layersOf("/some/project", ""), "")
	want := []string{"sh", "precheck.sh", "{{CONTAINER_NAME}}"}
	if !slices.Equal(got, want) {
		t.Errorf("expected %v, got %v", want, got)
	}
}

func TestResolvePrecheckCmdConvention(t *testing.T) {
	dir := t.TempDir()
	projectDir := filepath.Join(dir, "project")
	_ = os.MkdirAll(projectDir, 0755)

	scriptPath := filepath.Join(projectDir, "docker-agent-precheck.sh")
	_ = os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 0\n"), 0755)

	cc := &ContainerConfig{}
	got := ResolvePrecheckCmd(cc, layersOf(projectDir, ""), "")
	want := []string{"sh", scriptPath, "{{CONTAINER_NAME}}"}
	if !slices.Equal(got, want) {
		t.Errorf("expected %v, got %v", want, got)
	}
}

func TestResolvePrecheckCmdRoleOverride(t *testing.T) {
	dir := t.TempDir()
	projectDir := filepath.Join(dir, "project")
	roleDir := filepath.Join(projectDir, "roles", "security")
	_ = os.MkdirAll(roleDir, 0755)

	roleScript := filepath.Join(roleDir, "docker-agent-precheck.sh")
	projectScript := filepath.Join(projectDir, "docker-agent-precheck.sh")
	_ = os.WriteFile(roleScript, []byte("#!/bin/sh\nexit 0\n"), 0755)
	_ = os.WriteFile(projectScript, []byte("#!/bin/sh\nexit 0\n"), 0755)

	cc := &ContainerConfig{}
	got := ResolvePrecheckCmd(cc, layersOf(projectDir, ""), "security")
	want := []string{"sh", roleScript, "{{CONTAINER_NAME}}"}
	if !slices.Equal(got, want) {
		t.Errorf("expected role-level %v, got %v", want, got)
	}
}

func TestResolvePrecheckCmdOrgFallback(t *testing.T) {
	dir := t.TempDir()
	projectDir := filepath.Join(dir, "project")
	orgDir := filepath.Join(dir, "org")
	_ = os.MkdirAll(projectDir, 0755)
	_ = os.MkdirAll(orgDir, 0755)

	orgScript := filepath.Join(orgDir, "docker-agent-precheck.sh")
	_ = os.WriteFile(orgScript, []byte("#!/bin/sh\nexit 0\n"), 0755)

	cc := &ContainerConfig{}
	got := ResolvePrecheckCmd(cc, layersOf(projectDir, orgDir), "")
	want := []string{"sh", orgScript, "{{CONTAINER_NAME}}"}
	if !slices.Equal(got, want) {
		t.Errorf("expected org-level %v, got %v", want, got)
	}
}

func TestResolvePrecheckCmdNone(t *testing.T) {
	dir := t.TempDir()
	cc := &ContainerConfig{}
	got := ResolvePrecheckCmd(cc, layersOf(dir, ""), "")
	if got != nil {
		t.Errorf("expected nil when no script exists, got %v", got)
	}
}

func TestIsolatedProfile(t *testing.T) {
	cfg, err := Load(nil)
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

func TestSandboxDetectionTopLevel(t *testing.T) {
	// Defaults set sandbox_detection = false and docker_detection = true
	// explicitly. The values must be discoverable in `ateam env` and in
	// the rendered defaults file.
	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("Load defaults: %v", err)
	}
	if cfg.SandboxDetection == nil || *cfg.SandboxDetection != false {
		t.Errorf("default Load: expected SandboxDetection=false, got %v", cfg.SandboxDetection)
	}
	if cfg.DockerDetection == nil || *cfg.DockerDetection != true {
		t.Errorf("default Load: expected DockerDetection=true, got %v", cfg.DockerDetection)
	}

	// User org file sets it to false — must be picked up.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ConfigFile),
		[]byte("sandbox_detection = false\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load([]string{dir})
	if err != nil {
		t.Fatalf("Load with override: %v", err)
	}
	if cfg.SandboxDetection == nil {
		t.Fatal("expected SandboxDetection non-nil after override")
	}
	if *cfg.SandboxDetection != false {
		t.Errorf("expected false, got true")
	}

	// Project override wins over org override (last writer wins).
	projDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(projDir, ConfigFile),
		[]byte("sandbox_detection = true\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(layersOf(projDir, dir))
	if err != nil {
		t.Fatalf("Load project+org: %v", err)
	}
	if cfg.SandboxDetection == nil || *cfg.SandboxDetection != true {
		t.Errorf("expected project value true to win, got %v", cfg.SandboxDetection)
	}
}

// writeLayer writes a runtime.hcl into dir and returns dir.
func writeLayer(t *testing.T, dir, hcl string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ConfigFile), []byte(hcl), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPerFieldMergeKeepsUnsetAttributes(t *testing.T) {
	orgDir := writeLayer(t, t.TempDir(), `
agent "claude" {
  model = "opus"
}
`)
	cfg, err := Load([]string{orgDir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ac := cfg.Agents["claude"]
	if ac.Model != "opus" {
		t.Errorf("model: got %q, want opus", ac.Model)
	}
	if ac.Command != "claude" || len(ac.Args) == 0 || ac.Sandbox == "" {
		t.Errorf("built-in command/args/sandbox must survive a partial override: cmd=%q args=%v sandbox=%d bytes", ac.Command, ac.Args, len(ac.Sandbox))
	}
	if src, ok := cfg.Source("agent.claude.model"); !ok || src.Layer != filepath.Join(orgDir, ConfigFile) || src.Value != "opus" {
		t.Errorf("model source: got %+v (ok=%v)", src, ok)
	}
	if src, ok := cfg.Source("agent.claude.command"); !ok || src.Layer != BuiltinLayer {
		t.Errorf("command source: got %+v (ok=%v), want built-in", src, ok)
	}
}

func TestPerFieldMergeEnvPerKeyListsReplace(t *testing.T) {
	orgDir := writeLayer(t, t.TempDir(), `
agent "claude" {
  args = ["-p"]
  env = {
    CUSTOM_VAR = "hello"
  }
}
`)
	cfg, err := Load([]string{orgDir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ac := cfg.Agents["claude"]
	if ac.Env["CUSTOM_VAR"] != "hello" {
		t.Errorf("CUSTOM_VAR: got %q", ac.Env["CUSTOM_VAR"])
	}
	if v, ok := ac.Env["CLAUDECODE"]; !ok || v != "" {
		t.Errorf("built-in env key CLAUDECODE must survive per-key merge, got %q (present=%v)", v, ok)
	}
	if len(ac.Args) != 1 || ac.Args[0] != "-p" {
		t.Errorf("args must be replaced, not appended: %v", ac.Args)
	}
	chain := cfg.Sources["agent.claude.env.CLAUDECODE"]
	if len(chain) != 1 || chain[0].Layer != BuiltinLayer {
		t.Errorf("CLAUDECODE chain: %+v", chain)
	}
	chain = cfg.Sources["agent.claude.args"]
	if len(chain) != 2 || chain[0].Layer != BuiltinLayer || chain[1].Value != `["-p"]` {
		t.Errorf("args chain: %+v", chain)
	}
}

func TestProfilePartialOverride(t *testing.T) {
	projDir := writeLayer(t, t.TempDir(), `profile "default" { agent = "mock" }`)
	cfg, err := Load([]string{projDir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	p := cfg.Profiles["default"]
	if p.Agent != "mock" || p.Container != "none" {
		t.Errorf("got agent=%q container=%q, want mock/none", p.Agent, p.Container)
	}
	if src, _ := cfg.Source("profile.default.container"); src.Layer != BuiltinLayer {
		t.Errorf("container source: %+v", src)
	}
}

func TestProfileMissingContainerRejected(t *testing.T) {
	projDir := writeLayer(t, t.TempDir(), `profile "half" { agent = "mock" }`)
	_, err := Load([]string{projDir})
	if err == nil || !strings.Contains(err.Error(), `profile "half"`) {
		t.Fatalf("expected error naming the profile, got %v", err)
	}
}

func TestContainerPartialOverride(t *testing.T) {
	projDir := writeLayer(t, t.TempDir(), `container "docker" { forward_env = ["ONLY_THIS"] }`)
	cfg, err := Load([]string{projDir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cc := cfg.Containers["docker"]
	if cc.Type != "docker" || cc.Dockerfile != "Dockerfile" || !cc.MountClaudeConfig {
		t.Errorf("built-in container attributes lost: %+v", cc)
	}
	if len(cc.ForwardEnv) != 1 || cc.ForwardEnv[0] != "ONLY_THIS" {
		t.Errorf("forward_env: %v", cc.ForwardEnv)
	}
}

func TestBaseInheritanceProvenance(t *testing.T) {
	orgDir := writeLayer(t, t.TempDir(), `
agent "parent" {
  command = "parent-cmd"
  model   = "parent-model"
  env = {
    FROM_PARENT = "1"
  }
}

agent "child" {
  base  = "parent"
  model = "child-model"
  env = {
    FROM_CHILD = "1"
  }
}
`)
	cfg, err := Load([]string{orgDir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	child := cfg.Agents["child"]
	if child.Command != "parent-cmd" || child.Model != "child-model" {
		t.Errorf("got command=%q model=%q", child.Command, child.Model)
	}
	if child.Env["FROM_PARENT"] != "1" || child.Env["FROM_CHILD"] != "1" {
		t.Errorf("env must merge base keys under child keys: %v", child.Env)
	}
	if src, _ := cfg.Source("agent.child.command"); src.Layer != `agent "parent" (base)` || src.Value != "parent-cmd" {
		t.Errorf("command source: %+v", src)
	}
	if src, _ := cfg.Source("agent.child.model"); src.Layer == `agent "parent" (base)` {
		t.Errorf("child's own model must not be attributed to the base: %+v", src)
	}
	if src, _ := cfg.Source("agent.child.env.FROM_PARENT"); src.Layer != `agent "parent" (base)` {
		t.Errorf("inherited env key source: %+v", src)
	}
	// The parent's own env map must not have been mutated by the merge.
	if _, leaked := cfg.Agents["parent"].Env["FROM_CHILD"]; leaked {
		t.Error("child env key leaked into the parent's map")
	}
}

func TestExplicitZeroValueIsNotInherited(t *testing.T) {
	orgDir := writeLayer(t, t.TempDir(), `
agent "parent" {
  command                  = "parent-cmd"
  sandbox_inside_container = true
  tmux_width               = 300
}

agent "child" {
  base                     = "parent"
  sandbox_inside_container = false
  tmux_width               = 0
}
`)
	cfg, err := Load([]string{orgDir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	child := cfg.Agents["child"]
	if child.SandboxInsideContainer {
		t.Error("explicit sandbox_inside_container = false must stick")
	}
	if child.TmuxWidth != 0 {
		t.Errorf("explicit tmux_width = 0 must stick, got %d", child.TmuxWidth)
	}
}

func TestBaseInheritsPricingWhenChildHasNone(t *testing.T) {
	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Agents["codex-high"].Pricing == nil {
		t.Fatal("codex-high must inherit codex pricing")
	}
	if src, _ := cfg.Source("agent.codex-high.pricing"); src.Layer != `agent "codex" (base)` {
		t.Errorf("pricing source: %+v", src)
	}
}

func TestPerFieldMergeAcrossThreeLayers(t *testing.T) {
	homeDir := writeLayer(t, t.TempDir(), `agent "claude" { model = "sonnet" }`)
	parentDir := writeLayer(t, t.TempDir(), `agent "claude" { effort = "high" }`)
	projDir := writeLayer(t, t.TempDir(), `agent "claude" { model = "opus" }`)

	cfg, err := Load([]string{homeDir, parentDir, projDir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ac := cfg.Agents["claude"]
	if ac.Model != "opus" || ac.Effort != "high" {
		t.Errorf("got model=%q effort=%q, want opus/high", ac.Model, ac.Effort)
	}
	chain := cfg.Sources["agent.claude.model"]
	if len(chain) != 2 || chain[0].Value != "sonnet" || chain[1].Value != "opus" {
		t.Errorf("model chain: %+v", chain)
	}
	if len(cfg.Layers) != 4 || cfg.Layers[0].Label != BuiltinLayer || !cfg.Layers[3].Found {
		t.Errorf("layers: %+v", cfg.Layers)
	}
}

func TestLayersListMissingFiles(t *testing.T) {
	cfg, err := Load([]string{t.TempDir(), t.TempDir(), t.TempDir()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Layers) != 4 {
		t.Fatalf("expected 4 layers, got %+v", cfg.Layers)
	}
	for _, l := range cfg.Layers[1:] {
		if l.Found {
			t.Errorf("layer %q should be reported missing", l.Label)
		}
	}
}

// layersOf builds a layer list from the old (projectDir, orgDir) test shape:
// the org dir is the lower layer, the project dir the higher one.
func layersOf(projectDir, orgDir string) []string {
	var out []string
	for _, d := range []string{orgDir, projectDir} {
		if d != "" {
			out = append(out, d)
		}
	}
	return out
}

func TestProvenanceFileAndLine(t *testing.T) {
	homeDir := writeLayer(t, t.TempDir(), `
agent "claude" {
  model = "sonnet"
  env = {
    FOO   = "1"
    "BAR" = "2"
  }
}

agent "kid" {
  base   = "claude"
  effort = "low"
}

container "docker" {
  forward_env = ["X"]
}

profile "p" {
  agent     = "kid"
  container = "none"
}

role "security" {
  status = "on"
}

container_extra {
  env = {
    K = "v"
  }
}

expensive_output_per_mtok = 5
sandbox_detection         = false
`)
	cfg, err := Load([]string{homeDir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	file := filepath.Join(homeDir, ConfigFile)
	cases := map[string]Contribution{
		"agent.claude.model":           {Layer: file, Value: "sonnet", File: file, Line: 3},
		"agent.claude.env.FOO":         {Layer: file, Value: "1", File: file, Line: 5},
		"agent.claude.env.BAR":         {Layer: file, Value: "2", File: file, Line: 6},
		"agent.kid.name":               {Layer: file, Value: "kid", File: file, Line: 10},
		"agent.kid.base":               {Layer: file, Value: "claude", File: file, Line: 11},
		"agent.kid.model":              {Layer: `agent "claude" (base)`, Value: "sonnet", File: file, Line: 3},
		"agent.kid.env.BAR":            {Layer: `agent "claude" (base)`, Value: "2", File: file, Line: 6},
		"container.docker.forward_env": {Layer: file, Value: `["X"]`, File: file, Line: 16},
		"profile.p.name":               {Layer: file, Value: "p", File: file, Line: 19},
		"profile.p.container":          {Layer: file, Value: "none", File: file, Line: 21},
		"role.security.status":         {Layer: file, Value: "on", File: file, Line: 25},
		"container_extra.env.K":        {Layer: file, Value: "v", File: file, Line: 30},
		"expensive_output_per_mtok":    {Layer: file, Value: "5", File: file, Line: 34},
		"sandbox_detection":            {Layer: file, Value: "false", File: file, Line: 35},
	}
	for key, want := range cases {
		got, ok := cfg.Source(key)
		if !ok || got != want {
			t.Errorf("%s: got %+v (ok=%v), want %+v", key, got, ok, want)
		}
	}
	// Built-in values point into the embedded defaults; the base label
	// carries the base's own location.
	if got, _ := cfg.Source("agent.claude.command"); got.Layer != BuiltinLayer || got.File != BuiltinLayer || got.Line == 0 {
		t.Errorf("built-in command: %+v", got)
	}
	if got, _ := cfg.Source("agent.kid.command"); got.Layer != `agent "claude" (base)` || got.File != BuiltinLayer || got.Line == 0 {
		t.Errorf("inherited built-in command: %+v", got)
	}
}

func TestTraceFiltersByPrefix(t *testing.T) {
	homeDir := writeLayer(t, t.TempDir(), `agent "claude" { model = "sonnet" }`)
	projDir := writeLayer(t, t.TempDir(), `agent "claude" { model = "opus" }`)
	cfg, err := Load([]string{homeDir, projDir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	entries := cfg.Trace("agent.claude.model")
	if len(entries) != 1 || entries[0].Key != "agent.claude.model" || len(entries[0].Chain) != 2 {
		t.Fatalf("exact key: %+v", entries)
	}
	if w := cfg.Where(entries[0].Chain[1], nil); w != filepath.Join(projDir, ConfigFile)+":1" {
		t.Errorf("where: %q", w)
	}
	if w := cfg.Where(entries[0].Chain[0], nil); w != filepath.Join(homeDir, ConfigFile)+":1" {
		t.Errorf("where (home): %q", w)
	}
	for _, prefix := range []string{"agent.claude", "agent.claude.", "agent.claude.*"} {
		entries = cfg.Trace(prefix)
		if len(entries) < 5 {
			t.Errorf("%q: expected the whole block, got %d entries", prefix, len(entries))
		}
		for i, e := range entries {
			if !strings.HasPrefix(e.Key, "agent.claude.") {
				t.Errorf("%q: stray key %s", prefix, e.Key)
			}
			if i > 0 && entries[i-1].Key > e.Key {
				t.Errorf("%q: not sorted at %s", prefix, e.Key)
			}
		}
	}
	if entries = cfg.Trace("agent.claud"); len(entries) != 0 {
		t.Errorf("partial block name must not match: %+v", entries)
	}
	upper := func(p string) string { return strings.ToUpper(p) }
	model, _ := cfg.Source("agent.claude.model")
	if w := cfg.Where(model, upper); w != strings.ToUpper(filepath.Join(projDir, ConfigFile))+":1" {
		t.Errorf("where with display: %q", w)
	}
	if got, _ := cfg.Source("agent.claude.command"); cfg.Where(got, upper) != BuiltinLayer+":"+strconv.Itoa(got.Line) {
		t.Errorf("display must not touch the built-in label: %q", cfg.Where(got, upper))
	}
	if all := cfg.Trace(""); len(all) != len(cfg.Sources) {
		t.Errorf("empty prefix: %d entries, %d keys", len(all), len(cfg.Sources))
	}
	if got, _ := cfg.Source("agent.claude.command"); cfg.Where(got, nil) != BuiltinLayer+":"+strconv.Itoa(got.Line) {
		t.Errorf("built-in where: %q", cfg.Where(got, nil))
	}
}
