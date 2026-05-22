package agent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestCodexTmuxAgentDebugCommandArgs(t *testing.T) {
	a := &CodexTmuxAgent{
		Command: "codex",
		Args:    []string{"--no-alt-screen", "--sandbox", "workspace-write"},
		Model:   "gpt-5.5",
		Effort:  "xhigh",
	}

	_, args := a.DebugCommandArgs([]string{"--ask-for-approval", "never"})
	want := []string{
		"--no-alt-screen",
		"--sandbox", "workspace-write",
		"-c", "check_for_update_on_startup=false",
		"--disable", "apps",
		"--disable", "plugins",
		"--model", "gpt-5.5",
		"-c", "model_reasoning_effort=xhigh",
		"--ask-for-approval", "never",
	}
	if !slices.Equal(args, want) {
		t.Errorf("args = %v, want %v", args, want)
	}
}

func TestCodexTmuxAgentCloneCopiesMutableFields(t *testing.T) {
	a := &CodexTmuxAgent{
		Args:             []string{"--name", "{{ROLE}}"},
		Env:              map[string]string{"ROLE": "{{ROLE}}"},
		Pricing:          PricingTable{"m": {InputPerToken: 1}},
		StartTimeout:     time.Second,
		BusyTimeout:      2 * time.Second,
		QuiescenceWindow: 3 * time.Second,
	}
	r := strings.NewReplacer("{{ROLE}}", "security")

	clone := a.CloneWithResolvedTemplates(r).(*CodexTmuxAgent)
	clone.Args[1] = "mutated"
	clone.Env["ROLE"] = "mutated"
	clone.Pricing["m"] = ModelPrice{InputPerToken: 9}

	if a.Args[1] != "{{ROLE}}" {
		t.Errorf("original args mutated: %v", a.Args)
	}
	if a.Env["ROLE"] != "{{ROLE}}" {
		t.Errorf("original env mutated: %v", a.Env)
	}
	if a.Pricing["m"].InputPerToken != 1 {
		t.Errorf("original pricing mutated: %v", a.Pricing)
	}
}

func TestCodexTmuxEnvCreatesWritableCodexHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	srcDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(srcDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "auth.json"), []byte(`{"token":"x"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "config.toml"), []byte("model = \"gpt-5.5\"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	workdir := t.TempDir()
	env, err := codexTmuxEnv(workdir, nil, nil)
	if err != nil {
		t.Fatalf("codexTmuxEnv: %v", err)
	}
	wantHome := filepath.Join(workdir, ".cache", "codex-home")
	if env["CODEX_HOME"] != wantHome {
		t.Fatalf("CODEX_HOME = %q, want %q", env["CODEX_HOME"], wantHome)
	}
	if _, err := os.Lstat(filepath.Join(wantHome, "auth.json")); err != nil {
		t.Fatalf("auth.json not seeded: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(wantHome, "config.toml")); err != nil {
		t.Fatalf("config.toml not seeded: %v", err)
	}
	config, err := os.ReadFile(filepath.Join(wantHome, "config.toml"))
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}
	if strings.Contains(string(config), "model =") {
		t.Fatalf("generated config inherited user config: %q", config)
	}
	if !strings.Contains(string(config), "trust_level = \"trusted\"") {
		t.Fatalf("generated config does not trust workdir: %q", config)
	}
}

func TestCodexTmuxEnvHonorsExplicitCodexHome(t *testing.T) {
	env, err := codexTmuxEnv(t.TempDir(), map[string]string{"CODEX_HOME": "/custom"}, nil)
	if err != nil {
		t.Fatalf("codexTmuxEnv: %v", err)
	}
	if env["CODEX_HOME"] != "/custom" {
		t.Fatalf("CODEX_HOME = %q, want /custom", env["CODEX_HOME"])
	}
}
