package cmd

import (
	"testing"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
)

// TestApplyRunnerOverridesEmpty verifies that an empty RunnerOverrides leaves
// the agent's tunables untouched and only marks ContainerNameSource so the
// dry-run display has a value to render.
func TestApplyRunnerOverridesEmpty(t *testing.T) {
	a := &agent.ClaudeAgent{}
	r := &runner.Runner{Agent: a}

	if err := applyRunnerOverrides(r, &root.ResolvedEnv{}, RunnerOverrides{}, runner.ActionExec); err != nil {
		t.Fatalf("applyRunnerOverrides: %v", err)
	}

	if a.Model != "" {
		t.Errorf("Model = %q, want empty", a.Model)
	}
	if a.Effort != "" {
		t.Errorf("Effort = %q, want empty", a.Effort)
	}
	if a.MaxBudgetUSD != "" {
		t.Errorf("MaxBudgetUSD = %q, want empty", a.MaxBudgetUSD)
	}
	if r.ContainerNameSource != runner.ContainerNameSourceConfig {
		t.Errorf("ContainerNameSource = %q, want %q", r.ContainerNameSource, runner.ContainerNameSourceConfig)
	}
}

// TestApplyRunnerOverridesFull verifies that a fully-populated RunnerOverrides
// flows every field through to the agent and runner. CheaperModel is
// intentionally false so Model wins straightforwardly.
func TestApplyRunnerOverridesFull(t *testing.T) {
	a := &agent.ClaudeAgent{}
	r := &runner.Runner{
		Agent:         a,
		ContainerName: "config-container",
	}

	o := RunnerOverrides{
		ContainerName:     "cli-container",
		Model:             "opus-4",
		Effort:            "high",
		MaxBudgetUSD:      "10",
		MaxBudgetUSDBatch: "50",
	}
	if err := applyRunnerOverrides(r, &root.ResolvedEnv{}, o, runner.ActionExec); err != nil {
		t.Fatalf("applyRunnerOverrides: %v", err)
	}

	if a.Model != "opus-4" {
		t.Errorf("Model = %q, want opus-4", a.Model)
	}
	if a.Effort != "high" {
		t.Errorf("Effort = %q, want high", a.Effort)
	}
	if a.MaxBudgetUSD != "10" {
		t.Errorf("MaxBudgetUSD = %q, want 10", a.MaxBudgetUSD)
	}
	if r.ContainerNameSource != runner.ContainerNameSourceCLI {
		t.Errorf("ContainerNameSource = %q, want %q", r.ContainerNameSource, runner.ContainerNameSourceCLI)
	}
}

// TestApplyRunnerOverridesCheaperModel exercises the --cheaper-model branch via
// the helper to make sure it routes through applyModelOverrides (and not the
// older applyModel that ignored --cheaper-model).
func TestApplyRunnerOverridesCheaperModel(t *testing.T) {
	a := &agent.ClaudeAgent{}
	r := &runner.Runner{Agent: a}

	o := RunnerOverrides{CheaperModel: true}
	if err := applyRunnerOverrides(r, &root.ResolvedEnv{}, o, runner.ActionExec); err != nil {
		t.Fatalf("applyRunnerOverrides: %v", err)
	}
	if a.Model != cheaperModelName {
		t.Errorf("Model = %q, want %q", a.Model, cheaperModelName)
	}
}

// TestApplyRunnerOverridesMaxBudgetError verifies the helper propagates the
// codex-on-single-exec error from applyMaxBudgetUSD instead of swallowing it.
func TestApplyRunnerOverridesMaxBudgetError(t *testing.T) {
	r := &runner.Runner{Agent: &agent.CodexAgent{}}
	o := RunnerOverrides{MaxBudgetUSD: "5"}
	if err := applyRunnerOverrides(r, &root.ResolvedEnv{}, o, runner.ActionExec); err == nil {
		t.Fatal("expected error when codex action=exec receives --max-budget-usd, got nil")
	}
}
