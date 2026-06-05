package prompts

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ateam/internal/prompts/assembler"
	"github.com/ateam/internal/root"
)

func mkTestAssembler(files map[string]string) *assembler.Assembler {
	mf := fstest.MapFS{}
	for path, body := range files {
		mf["prompts/"+path] = &fstest.MapFile{Data: []byte(body)}
	}
	anchors := assembler.BuildAnchors("", "", mf)
	return assembler.New(anchors)
}

// envWithAssembler returns a minimal ResolvedEnv that surfaces a
// test-supplied assembler via env.Assembler(). Production code never
// calls SetAssemblerOverride; tests use it to avoid needing on-disk
// anchor fixtures.
func envWithAssembler(a *assembler.Assembler) *root.ResolvedEnv {
	env := &root.ResolvedEnv{}
	env.SetAssemblerOverride(a)
	return env
}

func newCtx(prompt map[string]string, dyn PromptDynamic) *stubCtx {
	return &stubCtx{
		vars: assembler.MapVars{Prompt: prompt},
		mode: ModeReal,
		dyn:  dyn,
	}
}

// newPromptFileCtx returns a stubCtx wired with an env carrying the
// test assembler, plus the test's prompt vars + dynamics.
func newPromptFileCtx(a *assembler.Assembler, prompt map[string]string, dyn PromptDynamic) *stubCtx {
	return &stubCtx{
		env:  envWithAssembler(a),
		vars: assembler.MapVars{Prompt: prompt},
		mode: ModeReal,
		dyn:  dyn,
	}
}

func TestRawTextPromptPassesThrough(t *testing.T) {
	p := RawTextPrompt{Text: "Hello {{prompt.name}}"}
	ctx := newCtx(map[string]string{"name": "world"}, nil)
	got, err := p.Resolve(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Hello {{prompt.name}}" {
		t.Fatalf("got %q — expected no expansion", got)
	}
	sec, err := p.Inspect(ctx)
	if err != nil || sec != nil {
		t.Fatalf("Inspect = (%v, %v), want (nil, nil)", sec, err)
	}
}

func TestPromptTextInspectReturnsNoSections(t *testing.T) {
	// Inline text has no section structure — Inspect returns (nil, nil) just
	// like RawTextPrompt. Keeps the contract symmetric for --paths callers.
	p := PromptText{Text: "anything"}
	sec, err := p.Inspect(newCtx(nil, nil))
	if err != nil || sec != nil {
		t.Fatalf("Inspect = (%v, %v), want (nil, nil)", sec, err)
	}
}

func TestPromptTextExpands(t *testing.T) {
	p := PromptText{Text: "name={{prompt.name}} role={{prompt.role}}"}
	ctx := newCtx(map[string]string{"name": "security", "role": "auditor"}, nil)
	got, err := p.Resolve(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != "name=security role=auditor" {
		t.Fatalf("got %q", got)
	}
}

func TestPromptTextDispatchesDynamic(t *testing.T) {
	dyn := PromptDynamic{
		"upper": func(ctx ResolveContext, args ...string) (string, error) {
			return strings.ToUpper(strings.Join(args, " ")), nil
		},
	}
	p := PromptText{Text: "[{{dynamic.upper hello {{prompt.name}}}}]"}
	ctx := newCtx(map[string]string{"name": "world"}, dyn)
	got, err := p.Resolve(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != "[HELLO WORLD]" {
		t.Fatalf("got %q", got)
	}
}

func TestPromptTextIncludeErrors(t *testing.T) {
	// PromptText has no assembler — include directives must fail loudly.
	p := PromptText{Text: "{{include foo.md}}"}
	ctx := newCtx(nil, nil)
	_, err := p.Resolve(ctx)
	if err == nil || !strings.Contains(err.Error(), "no assembler") {
		t.Fatalf("expected no-assembler error, got %v", err)
	}
}

func TestPromptFileErrorsWithoutCtx(t *testing.T) {
	// PromptFile sources Assembler/Vars from ctx. nil ctx — or a ctx
	// whose Env() returns nil — is a programmer error: surface it.
	p := PromptFile{Path: "review"}
	if _, err := p.Resolve(nil); err == nil {
		t.Fatal("expected nil-ctx error")
	}
	if _, err := p.Inspect(&stubCtx{}); err == nil {
		t.Fatal("expected nil-env error (stubCtx has no env)")
	}
}

func TestPromptFileResolveComposesFraming(t *testing.T) {
	// Build an in-memory anchor list with a role prompt + framing fragments.
	// PromptFile.Resolve should return the assembler's composed output.
	a := mkTestAssembler(map[string]string{
		"review.prompt.md":     "BODY for {{prompt.name}}",
		"review.pre.intro.md":  "INTRO",
		"review.post.outro.md": "OUTRO",
	})

	p := PromptFile{Path: "review"}
	ctx := newPromptFileCtx(a, map[string]string{
		"name":   "supervisor",
		"action": "review",
		"path":   "review",
	}, nil)
	got, err := p.Resolve(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"INTRO", "BODY for supervisor", "OUTRO"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

func TestPromptFileInspectReturnsSections(t *testing.T) {
	a := mkTestAssembler(map[string]string{
		"review.prompt.md": "BODY",
	})
	p := PromptFile{Path: "review"}
	ctx := newPromptFileCtx(a, map[string]string{"name": "supervisor", "action": "review", "path": "review"}, nil)
	secs, err := p.Inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(secs) == 0 {
		t.Fatal("expected at least one section")
	}
}
