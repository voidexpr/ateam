package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/root"
)

func setupFixture(t *testing.T) (home, projPath string, env *root.ResolvedEnv) {
	t.Helper()
	base := resolvedTestDir(t)
	home = testHome(t, base)
	projPath = filepath.Join(base, "myproj")
	if err := os.MkdirAll(projPath, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := root.InitProject(projPath, home, root.InitProjectOpts{Name: "myproj"}); err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	env, err := root.LookupFrom(projPath)
	if err != nil {
		t.Fatalf("LookupFrom: %v", err)
	}
	return home, projPath, env
}

func TestAgentConfigPlan(t *testing.T) {
	home, projPath, env := setupFixture(t)

	file, value, dir, err := agentConfigPlan(env, "claude", agentConfigHome)
	if err != nil {
		t.Fatal(err)
	}
	wantDir := filepath.Join(home, "state", "agents", "claude")
	if file != filepath.Join(home, root.ConfigFile) || dir != wantDir || value == "" || strings.HasPrefix(value, "state/") {
		t.Errorf("home plan: file=%q value=%q dir=%q", file, value, dir)
	}

	file, value, dir, err = agentConfigPlan(env, "codex", agentConfigProject)
	if err != nil {
		t.Fatal(err)
	}
	if file != filepath.Join(projPath, ".ateam", root.ConfigFile) || value != "agents/codex" || dir != filepath.Join(env.ProjectDir, "agents", "codex") {
		t.Errorf("project plan: file=%q value=%q dir=%q", file, value, dir)
	}

	scratch := &root.ResolvedEnv{HomeDir: home}
	if _, _, _, err := agentConfigPlan(scratch, "claude", agentConfigProject); err == nil {
		t.Error("project choice without a project must error")
	}
}

func TestSetupWritesConfigDirAndLoginResolvesIt(t *testing.T) {
	home, projPath, env := setupFixture(t)

	out := captureStdout(t, func() {
		if err := setupAgentConfigDirs(env, agentConfigHome, []string{"claude"}, false); err != nil {
			t.Fatalf("setup home: %v", err)
		}
	})
	if !strings.Contains(out, "ateam login --agent claude") {
		t.Errorf("expected the login hint:\n%s", out)
	}

	env2, err := root.LookupFrom(projPath)
	if err != nil {
		t.Fatal(err)
	}
	target, err := resolveLoginTarget(env2, env2.Runtime, "", "claude")
	if err != nil {
		t.Fatal(err)
	}
	if target.ConfigDir != filepath.Join(home, "state", "agents", "claude") {
		t.Errorf("login resolves config dir %q", target.ConfigDir)
	}
	// Profiles that run claude see it too.
	if prof, ac, _, err := env2.Runtime.ResolveProfile("high"); err != nil || env2.Runtime.AgentForRun(prof, ac, false).ConfigDir == "" {
		t.Errorf("the high profile should run claude with the config_dir (%v)", err)
	}

	// A second run reports the existing setting instead of appending again.
	out = captureStdout(t, func() {
		if err := setupAgentConfigDirs(env2, agentConfigProject, []string{"claude"}, false); err != nil {
			t.Fatalf("setup again: %v", err)
		}
	})
	if !strings.Contains(out, "already set") {
		t.Errorf("expected the already-set notice:\n%s", out)
	}
	data, _ := os.ReadFile(filepath.Join(home, root.ConfigFile))
	if strings.Count(string(data), "\nagent \"claude\" {") != 1 {
		t.Errorf("config_dir block must be written once:\n%s", data)
	}

	// Project choice for codex lands in the project file with a relative path.
	captureStdout(t, func() {
		if err := setupAgentConfigDirs(env2, agentConfigProject, []string{"codex"}, false); err != nil {
			t.Fatalf("setup codex project: %v", err)
		}
	})
	env3, err := root.LookupFrom(projPath)
	if err != nil {
		t.Fatal(err)
	}
	if env3.Runtime.Agents["codex"].ConfigDir != "agents/codex" || env3.Runtime.Agents["codex-tmux"].ConfigDir != "agents/codex" {
		t.Errorf("codex config_dir: %q / tmux %q", env3.Runtime.Agents["codex"].ConfigDir, env3.Runtime.Agents["codex-tmux"].ConfigDir)
	}
}

func TestSetupNonInteractiveWithoutFlagOnlyHints(t *testing.T) {
	home, _, env := setupFixture(t)
	out := captureStdout(t, func() {
		if err := setupAgentConfigDirs(env, "", []string{"claude"}, false); err != nil {
			t.Fatalf("setup: %v", err)
		}
	})
	if !strings.Contains(out, "ateam setup") {
		t.Errorf("expected a hint to run ateam setup:\n%s", out)
	}
	data, _ := os.ReadFile(filepath.Join(home, root.ConfigFile))
	if strings.Contains(string(data), "\nagent \"claude\" {") {
		t.Error("nothing must be written without a choice")
	}
	if err := setupAgentConfigDirs(env, "elsewhere", []string{"claude"}, false); err == nil {
		t.Error("invalid choice must error")
	}
}
