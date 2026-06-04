package flow

import (
	"testing"

	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/prompts/assembler"
)

func TestRuntimeSatisfiesResolveContext(t *testing.T) {
	// Compile-time check above is the load-bearing one; this lives so a
	// runtime change that breaks the contract trips a focused unit test
	// before the rest of the suite times out resolving it.
	rt := NewRuntime(nil, nil, "/tmp")
	if rt.Mode() != ModePreview {
		t.Fatalf("default mode = %v, want ModePreview", rt.Mode())
	}
}

func TestRuntimeSettersAndAccessors(t *testing.T) {
	rt := NewRuntime(nil, nil, "/tmp")
	vars := assembler.MapVars{Prompt: map[string]string{"name": "rev"}}
	dyn := PromptDynamic{
		"id": func(prompts.ResolveContext, ...string) (string, error) { return "x", nil },
	}
	rt.SetVars(vars)
	rt.SetMode(ModeReal)
	rt.SetDynamics(dyn)

	if rt.Vars() == nil {
		t.Fatal("Vars() nil after SetVars")
	}
	if rt.Mode() != ModeReal {
		t.Fatalf("Mode() = %v, want ModeReal", rt.Mode())
	}
	if _, ok := rt.Dynamics()["id"]; !ok {
		t.Fatal("Dynamics() missing 'id' after SetDynamics")
	}
}

func TestRuntimeResolveContextEndToEnd(t *testing.T) {
	// Build a runtime, hand it to a PromptText, and check that vars +
	// dynamics propagate through the engine.
	dyn := PromptDynamic{
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
	rt.SetMode(ModeReal)
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
