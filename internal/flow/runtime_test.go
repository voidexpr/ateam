package flow

import (
	"testing"
	"time"

	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/prompts/assembler"
	"github.com/ateam/internal/runner"
)

func TestRuntimeSatisfiesResolveContext(t *testing.T) {
	// Compile-time check above is the load-bearing one; this lives so a
	// runtime change that breaks the contract trips a focused unit test
	// before the rest of the suite times out resolving it.
	rt := NewRuntime(nil, nil, "/tmp")
	if rt.Mode() != prompts.ModePreview {
		t.Fatalf("default mode = %v, want prompts.ModePreview", rt.Mode())
	}
}

func TestRuntimeSettersAndAccessors(t *testing.T) {
	rt := NewRuntime(nil, nil, "/tmp")
	vars := assembler.MapVars{Prompt: map[string]string{"name": "rev"}}
	dyn := prompts.PromptDynamic{
		"id": func(prompts.ResolveContext, ...string) (string, error) { return "x", nil },
	}
	rt.SetVars(vars)
	rt.SetMode(prompts.ModeReal)
	rt.SetDynamics(dyn)

	if rt.Vars() == nil {
		t.Fatal("Vars() nil after SetVars")
	}
	if rt.Mode() != prompts.ModeReal {
		t.Fatalf("Mode() = %v, want prompts.ModeReal", rt.Mode())
	}
	if _, ok := rt.Dynamics()["id"]; !ok {
		t.Fatal("Dynamics() missing 'id' after SetDynamics")
	}
}

func TestRuntimeResolveContextEndToEnd(t *testing.T) {
	// Build a runtime, hand it to a PromptText, and check that vars +
	// dynamics propagate through the engine.
	dyn := prompts.PromptDynamic{
		"upper": func(_ prompts.ResolveContext, args ...string) (string, error) {
			out := ""
			for _, a := range args {
				out += a
			}
			return "[" + out + "]", nil
		},
	}
	rt := NewRuntime(nil, nil, "/tmp")
	rt.SetVars(assembler.MapVars{Prompt: map[string]string{"name": "alpha"}})
	rt.SetMode(prompts.ModeReal)
	rt.SetDynamics(dyn)

	p := prompts.PromptText{Text: "{{prompt.name}}-{{dynamic.upper end}}"}
	got, err := p.Resolve(rt)
	if err != nil {
		t.Fatal(err)
	}
	if got != "alpha-[end]" {
		t.Fatalf("got %q", got)
	}
}

// TestNewBundleRuntimeAcceptsScalarsWithoutPreparedRun pins the
// flow ↔ runner decoupling: newBundleRuntime takes preparedScalars,
// not *runner.PreparedRun. Tests (or alternate executors) can
// construct a preparedScalars value directly without producing a
// runner-internal struct. The scalarsFromPrepared adapter handles
// the runner-backed path at the flow.execute callsite.
func TestNewBundleRuntimeAcceptsScalarsWithoutPreparedRun(t *testing.T) {
	scalars := &preparedScalars{
		ExecID:     42,
		RuntimeDir: "/tmp/runtime/42",
		PromptFile: "/tmp/logs/42/prompt.md",
		StartedAt:  time.Unix(1730000000, 0),
		AgentName:  "claude",
		Model:      "opus",
	}
	rc := RunCtx{}
	env := RuntimeEnv{WorkDir: "/wd"}
	opts := runner.RunOpts{Batch: "batch-X", OutputKind: runner.OutputKindReport, PromptName: "security"}

	rt := newBundleRuntime(rc, env, opts, scalars)
	if rt.Mode() != prompts.ModeReal {
		t.Errorf("non-nil scalars must produce ModeReal, got %v", rt.Mode())
	}
	if rt.ExecID != 42 {
		t.Errorf("ExecID = %d, want 42", rt.ExecID)
	}
	if rt.Batch != "batch-X" {
		t.Errorf("Batch = %q, want batch-X (sourced from opts, not scalars)", rt.Batch)
	}
	if rt.OutputDir != "/tmp/runtime/42" {
		t.Errorf("OutputDir = %q, want /tmp/runtime/42", rt.OutputDir)
	}
	if rt.PromptFile != "/tmp/logs/42/prompt.md" {
		t.Errorf("PromptFile = %q, want /tmp/logs/42/prompt.md", rt.PromptFile)
	}
	if rt.Agent != "claude" {
		t.Errorf("Agent = %q, want claude", rt.Agent)
	}
	if rt.Model != "opus" {
		t.Errorf("Model = %q, want opus", rt.Model)
	}
	if rt.OutputFile != "/tmp/runtime/42/security.md" {
		t.Errorf("OutputFile = %q, want /tmp/runtime/42/security.md (derived from OutputKind + PromptName)", rt.OutputFile)
	}
}

// TestScalarsFromPreparedHandlesNil pins the preview / dry-run path
// contract: a nil *runner.PreparedRun lifts to a nil *preparedScalars,
// which newBundleRuntime treats as "go to ModePreview".
func TestScalarsFromPreparedHandlesNil(t *testing.T) {
	if got := scalarsFromPrepared(nil); got != nil {
		t.Errorf("scalarsFromPrepared(nil) = %+v, want nil", got)
	}
	rc := RunCtx{}
	env := RuntimeEnv{WorkDir: "/wd"}
	rt := newBundleRuntime(rc, env, runner.RunOpts{}, nil)
	if rt.Mode() != prompts.ModePreview {
		t.Errorf("nil scalars must produce ModePreview, got %v", rt.Mode())
	}
}
