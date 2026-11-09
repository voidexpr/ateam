package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/config"
	"github.com/ateam/internal/container"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runtime"
)

func loadRuntimeWithOrg(t *testing.T, hcl string) (*runtime.Config, string) {
	t.Helper()
	orgDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(orgDir, root.ConfigFile), []byte(hcl), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := runtime.Load([]string{orgDir})
	if err != nil {
		t.Fatal(err)
	}
	return cfg, orgDir
}

func TestResolveLoginTargetPicksAgentLikeARun(t *testing.T) {
	rtCfg, orgDir := loadRuntimeWithOrg(t, `
agent "shared" {
  base       = "claude"
  config_dir = "agents/claude"
}

agent "codex-home" {
  base       = "codex"
  config_dir = "~/codex-unattended"
}

profile "shared" {
  agent     = "shared"
  container = "none"
}
`)
	env := &root.ResolvedEnv{HomeDir: orgDir, Layers: []string{orgDir}, Config: &config.Config{
		Project: config.ProjectConfig{DefaultProfile: "shared"},
	}}

	// Default: the project's default profile, config_dir anchored at the state dir.
	got, err := resolveLoginTarget(env, rtCfg, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Agent.Name != "shared" || got.Type != "claude" || got.ConfigDir != filepath.Join(orgDir, "state", "agents", "claude") {
		t.Errorf("default: got %+v", got)
	}
	if !strings.Contains(got.Source, "project.default_profile") {
		t.Errorf("default source: %q", got.Source)
	}

	// --agent wins and ~ expands.
	got, err = resolveLoginTarget(env, rtCfg, "", "codex-home")
	if err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	if got.Type != "codex" || got.ConfigDir != filepath.Join(home, "codex-unattended") || got.Source != config.ProfileSourceAgent {
		t.Errorf("--agent: got %+v", got)
	}

	// --profile naming an agent shorthand still resolves.
	got, err = resolveLoginTarget(env, rtCfg, "codex-home", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Agent.Name != "codex-home" {
		t.Errorf("--profile shorthand: got %+v", got)
	}

	// No config_dir: the CLI's own default.
	got, err = resolveLoginTarget(nil, rtCfg, "default", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.ConfigDir != "" || got.Agent.Name != "claude" {
		t.Errorf("built-in default: got %+v", got)
	}

	if _, err := resolveLoginTarget(env, rtCfg, "", "nope"); err == nil {
		t.Error("unknown agent must error")
	}
	if _, err := resolveLoginTarget(env, rtCfg, "shared", "shared"); err == nil {
		t.Error("--profile with --agent must error")
	}
}

func TestResolveLoginTargetAgentAppliesContextBlock(t *testing.T) {
	if container.IsInContainer() {
		t.Skip("asserts the outside_container block; running inside a container")
	}
	rtCfg, orgDir := loadRuntimeWithOrg(t, `
agent "ctx" {
  base = "claude"
  outside_container {
    config_dir = "agents/ctx-host"
  }
}
`)
	env := &root.ResolvedEnv{HomeDir: orgDir, Layers: []string{orgDir}}

	got, err := resolveLoginTarget(env, rtCfg, "", "ctx")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(orgDir, "state", "agents", "ctx-host")
	if got.ConfigDir != want || got.Agent.Context != "outside_container" {
		t.Errorf("--agent: got ConfigDir %q context %q, want %q outside_container", got.ConfigDir, got.Agent.Context, want)
	}
}

func TestAgentConfigDirPathErrors(t *testing.T) {
	ac := &runtime.AgentConfig{Name: "a", ConfigDir: "rel"}
	if _, err := agentConfigDirPath(ac, ""); err == nil || !strings.Contains(err.Error(), "relative config_dir") {
		t.Errorf("relative dir without state dir: %v", err)
	}
	ac.ConfigDir = ".claude-{{role}}"
	if _, err := agentConfigDirPath(ac, "/state"); err == nil || !strings.Contains(err.Error(), "template") {
		t.Errorf("templated dir: %v", err)
	}
}

func TestAgentCLIEnvStripsCredentialsAndSetsConfigDir(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "tok")
	t.Setenv("OPENAI_API_KEY", "oa")
	t.Setenv("CLAUDECODE", "1")

	target := loginTarget{
		Agent:     &runtime.AgentConfig{Name: "claude", Env: map[string]string{"CLAUDECODE": "", "KEEP": "yes"}},
		Type:      "claude",
		ConfigDir: "/tmp/cfg",
	}
	env, unset := agentCLIEnv(target)
	got := map[string]string{}
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		got[k] = v
	}
	if _, ok := got["ANTHROPIC_API_KEY"]; ok {
		t.Error("ANTHROPIC_API_KEY must be removed for a claude login")
	}
	if _, ok := got["CLAUDE_CODE_OAUTH_TOKEN"]; ok {
		t.Error("CLAUDE_CODE_OAUTH_TOKEN must be removed for a claude login")
	}
	if got["OPENAI_API_KEY"] != "oa" {
		t.Error("OPENAI_API_KEY is not a claude credential and must stay")
	}
	if _, ok := got["CLAUDECODE"]; ok {
		t.Error("agent env with an empty value must unset the var")
	}
	if got["KEEP"] != "yes" || got["CLAUDE_CONFIG_DIR"] != "/tmp/cfg" {
		t.Errorf("agent env and config dir not applied: %v", got)
	}
	if len(unset) != 2 {
		t.Errorf("unset = %v, want the two claude credential vars", unset)
	}
}
