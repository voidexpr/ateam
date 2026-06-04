package flow

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/prompts/assembler"
)

// minimal Pipe used to test Walk's recursion through nested compositions.
func makeRC() RunCtx { return RunCtx{} }

func TestVerify_Clean(t *testing.T) {
	b := PromptBundle{Name: "x", Prompt: prompts.RawTextPrompt{Text: "ok"}}
	if vr := Verify(b, makeRC()); vr != nil {
		t.Fatalf("expected clean, got %+v", vr)
	}
}

func TestVerify_PropagatesResolveError(t *testing.T) {
	want := errors.New("bad")
	b := PromptBundle{Name: "x", Prompt: errPrompt{err: want}}
	vr := Verify(b, makeRC())
	if vr == nil || len(vr.Errors) != 1 {
		t.Fatalf("expected one verify error, got %+v", vr)
	}
	if !errors.Is(vr.Errors[0].Err, want) {
		t.Errorf("Err = %v, want wrap of %v", vr.Errors[0].Err, want)
	}
}

func TestVerify_RecoversPanic(t *testing.T) {
	b := PromptBundle{Name: "boom", Prompt: &panicPrompt{msg: "kaboom"}}
	// First Verify call: panicPrompt's first Resolve returns OK by
	// design — used by the existing tests. Run it twice to exercise the
	// recover path.
	if vr := Verify(b, makeRC()); vr != nil {
		t.Fatalf("first verify should be clean, got %+v", vr)
	}
	vr := Verify(b, makeRC())
	if vr == nil || len(vr.Errors) != 1 || !strings.Contains(vr.Errors[0].Err.Error(), "panic") {
		t.Fatalf("expected panic-recovered verify error, got %+v", vr)
	}
}

func TestVerify_TyposInKnownNamespaceError(t *testing.T) {
	// Spec-required failure mode: a {{prompt.nope}} that targets a known
	// namespace but an unknown key surfaces at verify time.
	a := mkVerifyAssembler(map[string]string{
		"x.prompt.md": "hi {{prompt.nope}}",
	})
	b := PromptBundle{
		Name: "x",
		Prompt: prompts.PromptFile{
			Path:      "x",
			Assembler: a,
			Vars: assembler.MapVars{
				Prompt: map[string]string{"name": "x", "path": "x", "action": "x"},
			},
		},
	}
	vr := Verify(b, makeRC())
	if vr == nil || len(vr.Errors) != 1 {
		t.Fatalf("expected one verify error, got %+v", vr)
	}
	if !strings.Contains(vr.Errors[0].Err.Error(), "prompt.nope") {
		t.Errorf("error %q should mention prompt.nope", vr.Errors[0].Err)
	}
}

func TestVerify_StrictIncludeMissingErrors(t *testing.T) {
	a := mkVerifyAssembler(map[string]string{
		"x.prompt.md": "{{include nope.md}}",
	})
	b := PromptBundle{
		Name: "x",
		Prompt: prompts.PromptFile{
			Path:      "x",
			Assembler: a,
			Vars: assembler.MapVars{
				Prompt: map[string]string{"name": "x", "path": "x", "action": "x"},
			},
		},
	}
	vr := Verify(b, makeRC())
	if vr == nil || !strings.Contains(vr.Errors[0].Err.Error(), "nope.md") {
		t.Fatalf("expected include-missing error, got %+v", vr)
	}
}

func TestVerify_OptionalIncludeMissingIsClean(t *testing.T) {
	a := mkVerifyAssembler(map[string]string{
		"x.prompt.md": "before{{include? nope.md}}after",
	})
	b := PromptBundle{
		Name: "x",
		Prompt: prompts.PromptFile{
			Path:      "x",
			Assembler: a,
			Vars: assembler.MapVars{
				Prompt: map[string]string{"name": "x", "path": "x", "action": "x"},
			},
		},
	}
	if vr := Verify(b, makeRC()); vr != nil {
		t.Fatalf("optional include should not error during verify, got %+v", vr)
	}
}

func TestVerify_DynamicsRunInPreviewMode(t *testing.T) {
	// A dynamic that returns a sentinel in ModePreview and the real value
	// in ModeReal should hit its preview branch during verification.
	var modes []prompts.ResolveMode
	dyn := prompts.PromptDynamic{
		"check": func(ctx prompts.ResolveContext, _ ...string) (string, error) {
			modes = append(modes, ctx.Mode())
			if ctx.Mode() == prompts.ModePreview {
				return "<sentinel>", nil
			}
			return "<real>", nil
		},
	}
	a := mkVerifyAssembler(map[string]string{
		"x.prompt.md": "{{dynamic.check}}",
	})
	b := PromptBundle{
		Name: "x",
		Prompt: prompts.PromptFile{
			Path:      "x",
			Assembler: a,
			Vars: assembler.MapVars{
				Prompt: map[string]string{"name": "x", "path": "x", "action": "x"},
			},
		},
		Dynamics: dyn,
	}
	if vr := Verify(b, makeRC()); vr != nil {
		t.Fatalf("verify error: %+v", vr)
	}
	if len(modes) != 1 || modes[0] != prompts.ModePreview {
		t.Fatalf("dynamic invoked with modes %v, want [ModePreview]", modes)
	}
}

func TestVerify_WalksPipelineAndParallel(t *testing.T) {
	bad := errors.New("bad")
	mk := func(name string, p prompts.Prompt) PromptBundle {
		return PromptBundle{Name: name, Prompt: p}
	}
	ok := prompts.RawTextPrompt{Text: "ok"}
	pipe := Pipeline{
		Name: "pipe",
		Steps: []Step{
			mk("a", ok),
			Parallel{Name: "fan", Steps: []Step{
				mk("b", ok),
				mk("c", errPrompt{err: bad}),
			}},
			mk("d", errPrompt{err: bad}),
		},
	}
	vr := Verify(pipe, makeRC())
	if vr == nil || len(vr.Errors) != 2 {
		t.Fatalf("expected 2 verify errors (c, d), got %+v", vr)
	}
	names := map[string]bool{}
	for _, e := range vr.Errors {
		names[e.BundleName] = true
	}
	for _, n := range []string{"c", "d"} {
		if !names[n] {
			t.Errorf("missing verify error for bundle %q", n)
		}
	}
}

func TestRun_ShortCircuitsOnVerifyError(t *testing.T) {
	exec := &fakeExecutor{}
	rc := newCtx()
	env := newEnv(exec)
	b := PromptBundle{Name: "x", Prompt: errPrompt{err: errors.New("bad")}}
	out := Run(b, env, rc)
	if out.FirstErrorIndex != 0 {
		t.Fatalf("FirstErrorIndex = %d, want 0", out.FirstErrorIndex)
	}
	if len(out.Steps) != 1 || out.Steps[0].Name != "verify" {
		t.Fatalf("expected single verify step, got %+v", out.Steps)
	}
	if exec.prepped != 0 {
		t.Errorf("Prepare should not run when Verify errored; prepped=%d", exec.prepped)
	}
}

// mkVerifyAssembler builds a tiny in-memory assembler for verify tests.
// Defined here (not reused from prompts package) so flow tests stay
// self-contained against the assembler subpackage.
func mkVerifyAssembler(files map[string]string) *assembler.Assembler {
	mf := fstest.MapFS{}
	for path, body := range files {
		mf["prompts/"+path] = &fstest.MapFile{Data: []byte(body)}
	}
	return assembler.New(assembler.BuildAnchors("", "", mf))
}
