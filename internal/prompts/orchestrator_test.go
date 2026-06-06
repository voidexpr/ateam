package prompts

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ateam/internal/prompts/assembler"
)

// fakeAssembler returns a fixed file list — used to pin the orchestrator
// behavior independently of the chain-walk logic in MultiAnchorAssembler.
type fakeAssembler struct {
	files   []assembler.ResolvedFile
	framing []assembler.ResolvedFile
}

func (f *fakeAssembler) Resolve(string) ([]assembler.ResolvedFile, error) {
	return f.files, nil
}
func (f *fakeAssembler) ResolveFramingOnly(string) ([]assembler.ResolvedFile, error) {
	return f.framing, nil
}
func (f *fakeAssembler) FindOrphans() ([]*assembler.OrphanError, error) { return nil, nil }
func (f *fakeAssembler) Anchors() []assembler.Anchor                    { return nil }

// fakeFactory returns a fixed Prompt for the role_main slot — pins that
// the orchestrator delegates to the factory and uses what it returns
// (rather than rendering itself).
type fakeFactory struct {
	got      []string
	gotBody  []string
	respond  func(body string) Prompt
	respErr  error
	respText string
}

func (f *fakeFactory) For(path, body string) Prompt {
	f.got = append(f.got, path)
	f.gotBody = append(f.gotBody, body)
	if f.respond != nil {
		return f.respond(body)
	}
	return literalPrompt{text: f.respText, err: f.respErr}
}

type literalPrompt struct {
	text string
	err  error
}

func (p literalPrompt) Resolve(ResolveContext) (string, error) { return p.text, p.err }
func (p literalPrompt) Inspect(ResolveContext) ([]Section, error) {
	return nil, nil
}

// TestPromptFile_OrchestratorRendersFramingAndDelegatesRoleMain pins the
// per-slot dispatch: framing → engine; role_main → factory. Uses a fake
// factory whose return value is a known-bad string so we can prove the
// orchestrator emitted the factory's output, not its own engine render.
func TestPromptFile_OrchestratorRendersFramingAndDelegatesRoleMain(t *testing.T) {
	files := []assembler.ResolvedFile{
		{Slot: assembler.SlotRootPre, Anchor: "embedded", Path: "_pre.intro.md", FS: fstest.MapFS{
			"_pre.intro.md": &fstest.MapFile{Data: []byte("INTRO {{prompt.name}}")},
		}},
		{Slot: assembler.SlotRoleMain, Anchor: "embedded", Path: "thing.prompt.md", FS: fstest.MapFS{
			"thing.prompt.md": &fstest.MapFile{Data: []byte("ROLE BODY")},
		}},
	}
	a := &fakeAssembler{files: files}
	fac := &fakeFactory{respText: "FACTORY-OUTPUT"}

	ctx := newPromptFileCtx(mkTestAssembler(nil), map[string]string{"name": "thing"}, nil)
	pf := PromptFile{Path: "thing", Assembler: a, Factory: fac}
	out, err := pf.Resolve(ctx)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.Contains(out, "INTRO thing") {
		t.Errorf("framing should be engine-rendered, got: %q", out)
	}
	if !strings.Contains(out, "FACTORY-OUTPUT") {
		t.Errorf("role_main should come from factory, got: %q", out)
	}
	if strings.Contains(out, "ROLE BODY") {
		t.Errorf("role_main file content should not appear when factory overrides, got: %q", out)
	}
	if len(fac.got) != 1 || fac.got[0] != "thing.prompt.md" {
		t.Errorf("factory.For called with %v, want [thing.prompt.md]", fac.got)
	}
}

// TestPromptFile_NilFactoryRendersThroughOrchestratorEngine pins
// implementer note 3: with a nil factory, role_main is rendered through
// the orchestrator's own engine — so `{{include}}` inside the body
// still resolves against the same anchor chain as framing fragments.
// The test invariant for existing behavior depends on this.
func TestPromptFile_NilFactoryRendersThroughOrchestratorEngine(t *testing.T) {
	a := mkTestAssembler(map[string]string{
		"thing.prompt.md": "ROLE for {{prompt.name}}",
	})
	ctx := newPromptFileCtx(a, map[string]string{"name": "widget"}, nil)
	pf := PromptFile{Path: "thing"}
	out, err := pf.Resolve(ctx)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.Contains(out, "ROLE for widget") {
		t.Errorf("expected engine-rendered role main, got: %q", out)
	}
}

// TestPromptFile_PrePostAreRaw locks in implementer note 2: operator
// wrappers reach the agent verbatim. A `{{ns.key}}` token inside
// --pre-prompt / --post-prompt survives as a literal — no engine
// expansion, no error.
func TestPromptFile_PrePostAreRaw(t *testing.T) {
	a := mkTestAssembler(map[string]string{
		"thing.prompt.md": "BODY",
	})
	ctx := newPromptFileCtx(a, map[string]string{"name": "thing"}, nil)
	pf := PromptFile{
		Path:       "thing",
		PrePrompt:  "PRE {{prompt.name}}",
		PostPrompt: "POST {{prompt.name}}",
	}
	out, err := pf.Resolve(ctx)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.Contains(out, "PRE {{prompt.name}}") {
		t.Errorf("pre should stay literal, got: %q", out)
	}
	if !strings.Contains(out, "POST {{prompt.name}}") {
		t.Errorf("post should stay literal, got: %q", out)
	}
}

// TestPromptFile_CustomBodySkipsFactoryAndFile pins implementer note 4:
// CustomBody bypasses the role_main file read AND the factory. The
// orchestrator engine-renders the body inline.
func TestPromptFile_CustomBodySkipsFactoryAndFile(t *testing.T) {
	a := mkTestAssembler(map[string]string{
		"thing.prompt.md": "REAL BODY WHICH SHOULD NOT APPEAR",
	})
	fac := &fakeFactory{respText: "FACTORY SHOULD NOT BE CALLED"}
	ctx := newPromptFileCtx(a, map[string]string{"name": "thing"}, nil)
	pf := PromptFile{
		Path:       "thing",
		CustomBody: "CUSTOM {{prompt.name}}",
		Factory:    fac,
	}
	out, err := pf.Resolve(ctx)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.Contains(out, "CUSTOM thing") {
		t.Errorf("expected engine-rendered CUSTOM body, got: %q", out)
	}
	if strings.Contains(out, "REAL BODY") {
		t.Errorf("role_main file should be bypassed, got: %q", out)
	}
	if len(fac.got) != 0 {
		t.Errorf("factory.For called with %v, expected none", fac.got)
	}
}

// TestPromptFile_FactoryErrorPropagates checks that a factory error
// surfaces through the orchestrator with wrap context (file path so the
// operator can locate the failure).
func TestPromptFile_FactoryErrorPropagates(t *testing.T) {
	files := []assembler.ResolvedFile{
		{Slot: assembler.SlotRoleMain, Anchor: "embedded", Path: "thing.prompt.md", FS: fstest.MapFS{
			"thing.prompt.md": &fstest.MapFile{Data: []byte("BODY")},
		}},
	}
	a := &fakeAssembler{files: files}
	fac := &fakeFactory{respErr: errors.New("boom from factory")}
	ctx := newPromptFileCtx(mkTestAssembler(nil), nil, nil)
	pf := PromptFile{Path: "thing", Assembler: a, Factory: fac}
	_, err := pf.Resolve(ctx)
	if err == nil || !strings.Contains(err.Error(), "boom from factory") {
		t.Errorf("expected factory error, got: %v", err)
	}
}
