package cmd

import (
	"strings"
	"testing"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/runner"
)

func TestApplyMaxBudgetUSD(t *testing.T) {
	tests := []struct {
		name      string
		agent     agent.Agent
		value     string
		action    string
		wantErr   bool
		wantStore string
	}{
		{"empty value is no-op", &agent.ClaudeAgent{}, "", runner.ActionExec, false, ""},
		{"claude exec stores value", &agent.ClaudeAgent{}, "5", runner.ActionExec, false, "5"},
		{"claude code stores value", &agent.ClaudeAgent{}, "10.5", runner.ActionCode, false, "10.5"},
		{"codex parallel warns but ok", &agent.CodexAgent{}, "5", runner.ActionParallel, false, "5"},
		{"codex report warns but ok", &agent.CodexAgent{}, "5", runner.ActionReport, false, "5"},
		{"codex exec errors", &agent.CodexAgent{}, "5", runner.ActionExec, true, "5"},
		{"codex review errors", &agent.CodexAgent{}, "5", runner.ActionReview, true, "5"},
		{"codex code errors", &agent.CodexAgent{}, "5", runner.ActionCode, true, "5"},
		{"codex verify errors", &agent.CodexAgent{}, "5", runner.ActionVerify, true, "5"},
		{"codex-tmux review errors", &agent.CodexTmuxAgent{}, "5", runner.ActionReview, true, "5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &runner.AgentExecutor{Agent: tt.agent}
			err := applyMaxBudgetUSD(r, tt.value, tt.action)
			if (err != nil) != tt.wantErr {
				t.Errorf("err=%v wantErr=%v", err, tt.wantErr)
			}
			switch a := tt.agent.(type) {
			case *agent.ClaudeAgent:
				if a.MaxBudgetUSD != tt.wantStore {
					t.Errorf("claude.MaxBudgetUSD = %q, want %q", a.MaxBudgetUSD, tt.wantStore)
				}
			case *agent.CodexAgent:
				if a.MaxBudgetUSD != tt.wantStore {
					t.Errorf("codex.MaxBudgetUSD = %q, want %q", a.MaxBudgetUSD, tt.wantStore)
				}
			case *agent.CodexTmuxAgent:
				if a.MaxBudgetUSD != tt.wantStore {
					t.Errorf("codex-tmux.MaxBudgetUSD = %q, want %q", a.MaxBudgetUSD, tt.wantStore)
				}
			}
		})
	}
}

func TestApplyModelAndEffort(t *testing.T) {
	t.Run("empty model is no-op on claude", func(t *testing.T) {
		a := &agent.ClaudeAgent{Model: "preset"}
		r := &runner.AgentExecutor{Agent: a}
		applyModel(r, "")
		if a.Model != "preset" {
			t.Errorf("Model = %q, want unchanged %q", a.Model, "preset")
		}
	})

	t.Run("empty effort is no-op on codex", func(t *testing.T) {
		a := &agent.CodexAgent{Effort: "preset"}
		r := &runner.AgentExecutor{Agent: a}
		applyEffort(r, "")
		if a.Effort != "preset" {
			t.Errorf("Effort = %q, want unchanged %q", a.Effort, "preset")
		}
	})

	t.Run("non-empty model populates ClaudeAgent.Model", func(t *testing.T) {
		a := &agent.ClaudeAgent{}
		r := &runner.AgentExecutor{Agent: a}
		applyModel(r, "opus-4")
		if a.Model != "opus-4" {
			t.Errorf("ClaudeAgent.Model = %q, want %q", a.Model, "opus-4")
		}
	})

	t.Run("non-empty model populates CodexAgent.Model", func(t *testing.T) {
		a := &agent.CodexAgent{}
		r := &runner.AgentExecutor{Agent: a}
		applyModel(r, "gpt-5")
		if a.Model != "gpt-5" {
			t.Errorf("CodexAgent.Model = %q, want %q", a.Model, "gpt-5")
		}
	})

	t.Run("non-empty effort populates ClaudeAgent.Effort", func(t *testing.T) {
		a := &agent.ClaudeAgent{}
		r := &runner.AgentExecutor{Agent: a}
		applyEffort(r, "high")
		if a.Effort != "high" {
			t.Errorf("ClaudeAgent.Effort = %q, want %q", a.Effort, "high")
		}
	})

	t.Run("non-empty effort populates CodexAgent.Effort", func(t *testing.T) {
		a := &agent.CodexAgent{}
		r := &runner.AgentExecutor{Agent: a}
		applyEffort(r, "medium")
		if a.Effort != "medium" {
			t.Errorf("CodexAgent.Effort = %q, want %q", a.Effort, "medium")
		}
	})
}

func TestParseBudgetUSD(t *testing.T) {
	tests := []struct {
		in      string
		wantSet bool
		wantVal float64
		wantErr bool
	}{
		{"", false, 0, false},
		{"0", true, 0, false},
		{"5", true, 5, false},
		{"10.5", true, 10.5, false},
		{"-1", false, 0, true},
		{"abc", false, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			v, set, err := parseBudgetUSD(tt.in)
			if (err != nil) != tt.wantErr {
				t.Errorf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if set != tt.wantSet {
				t.Errorf("set=%v want=%v", set, tt.wantSet)
			}
			if v != tt.wantVal && !tt.wantErr {
				t.Errorf("v=%v want=%v", v, tt.wantVal)
			}
		})
	}
}

// TestApplyModelOverrides locks in the precedence rule between --cheaper-model
// and --model: if --model is set it always wins, --cheaper-model alone falls
// back to the cheaper model name, and the helper never touches AgentExecutor.ExtraArgs
// (the previous encoding through ExtraArgs caused dry-run output to disagree
// with what actually ran).
func TestApplyModelOverrides(t *testing.T) {
	tests := []struct {
		name        string
		cheaper     bool
		model       string
		wantModel   string
		wantWarning bool
	}{
		{"both unset is no-op", false, "", "", false},
		{"cheaper only sets cheaper model", true, "", cheaperModelName, false},
		{"model only sets explicit model", false, "opus-4", "opus-4", false},
		{"both set: model wins with warning", true, "opus-4", "opus-4", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &agent.ClaudeAgent{}
			r := &runner.AgentExecutor{
				Agent:     a,
				ExtraArgs: []string{"--existing", "arg"},
			}
			origExtraArgs := append([]string(nil), r.ExtraArgs...)

			stderr := captureStderr(t, func() {
				applyModelOverrides(r, tt.cheaper, tt.model)
			})

			if a.Model != tt.wantModel {
				t.Errorf("agent.Model = %q, want %q", a.Model, tt.wantModel)
			}
			// ExtraArgs must be untouched: the helper sets the agent's model
			// directly, no longer encoding it as a CLI arg.
			if !equalStrings(r.ExtraArgs, origExtraArgs) {
				t.Errorf("ExtraArgs mutated: got %v, want %v", r.ExtraArgs, origExtraArgs)
			}
			hasWarning := strings.Contains(stderr, "Warning:") &&
				strings.Contains(stderr, "--model") &&
				strings.Contains(stderr, "--cheaper-model")
			if hasWarning != tt.wantWarning {
				t.Errorf("warning emitted=%v want=%v (stderr=%q)", hasWarning, tt.wantWarning, stderr)
			}
		})
	}
}
