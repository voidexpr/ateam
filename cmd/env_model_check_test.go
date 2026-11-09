package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
)

func TestEnvReportsModelNameError(t *testing.T) {
	_, projPath, _ := setupFixture(t)
	projFile := filepath.Join(projPath, ".ateam", root.ConfigFile)
	if err := os.WriteFile(projFile, []byte("project {\n  name = \"myproj\"\n}\n\nagent \"claude\" {\n  model = \"claude-ops-5-5\"\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	env, err := root.LookupFrom(projPath)
	if err != nil {
		t.Fatal(err)
	}
	rtCfg, err := loadRuntime(env)
	if err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() { printProfilesInUse(env, rtCfg) })
	for _, want := range []string{
		`Error: agent "claude": model "claude-ops-5-5" does not look like a claude model id (set by ` + projFile + ":6)",
		"\n      known models: ",
		"\n      reference: https://platform.claude.com/docs/en/models/overview",
		"\n      --skip-model-name-check runs it anyway",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("env lacks %q:\n%s", want, out)
		}
	}
}

// TestCheckModelConfigRefusesAndSkipFlagDemotes covers the run gate in
// checkModelConfig: a model id failing model_pattern refuses the run,
// --skip-model-name-check demotes it to a stderr warning, a bad --model is
// refused the same way, and model_pattern = "" disables the check.
func TestCheckModelConfigRefusesAndSkipFlagDemotes(t *testing.T) {
	_, projPath, _ := setupFixture(t)
	projFile := filepath.Join(projPath, ".ateam", root.ConfigFile)
	writeConfig := func(agentBody string) *root.ResolvedEnv {
		t.Helper()
		hcl := "project {\n  name = \"myproj\"\n}\n\nagent \"claude\" {\n" + agentBody + "}\n"
		if err := os.WriteFile(projFile, []byte(hcl), 0644); err != nil {
			t.Fatal(err)
		}
		env, err := root.LookupFrom(projPath)
		if err != nil {
			t.Fatal(err)
		}
		return env
	}
	r := &runner.AgentExecutor{Profile: "a:claude"}

	env := writeConfig("  model = \"claude-ops-5-5\"\n")
	err := checkModelConfig(env, r, RunnerOverrides{})
	if err == nil {
		t.Fatal("bad pinned model: want refusal, got nil")
	}
	for _, want := range []string{`model "claude-ops-5-5"`, projFile + ":6"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal lacks %q: %v", want, err)
		}
	}

	old := skipModelNameCheck
	skipModelNameCheck = true
	t.Cleanup(func() { skipModelNameCheck = old })
	stderr := captureStderr(t, func() { err = checkModelConfig(env, r, RunnerOverrides{}) })
	if err != nil {
		t.Errorf("--skip-model-name-check: want nil, got %v", err)
	}
	if !strings.Contains(stderr, `Warning: agent "claude": model "claude-ops-5-5"`) {
		t.Errorf("--skip-model-name-check: stderr lacks the demoted refusal:\n%s", stderr)
	}
	skipModelNameCheck = false

	env = writeConfig("")
	err = checkModelConfig(env, r, RunnerOverrides{Model: "claude-bogus"})
	if err == nil || !strings.Contains(err.Error(), `model "claude-bogus"`) || !strings.Contains(err.Error(), "(set by --model)") {
		t.Errorf("unknown --model: want refusal naming it and --model, got %v", err)
	}

	env = writeConfig("  model_pattern = \"\"\n  model = \"claude-ops-5-5\"\n")
	if err := checkModelConfig(env, r, RunnerOverrides{}); err != nil {
		t.Errorf("model_pattern = \"\": want nil, got %v", err)
	}
}
