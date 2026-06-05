package prompts

import (
	"os"
	"path/filepath"
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

// TestIsFilesystemPromptPath asserts the predicate's closed truth-table.
// Commit fedb49d exported this so cmd-layer dispatch (`ateam exec @PATH`)
// and PromptFile.assemble share one rule for "this is a filesystem
// reference, inject a temp anchor" — divergence between caller and
// callee would silently route some paths through the wrong branch.
func TestIsFilesystemPromptPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		// Filesystem-mode: ends in .prompt.md AND has separator or
		// leading dot.
		{"./foo.prompt.md", true},
		{"../foo.prompt.md", true},
		{".prompt.md", true}, // pathological — starts with "." even though basename is empty
		{"dir/foo.prompt.md", true},
		{"/abs/path/foo.prompt.md", true},
		{"sub/dir/foo.prompt.md", true},
		// Logical-mode (no separator, no leading dot): caller resolves
		// via the anchor walk.
		{"foo.prompt.md", false},
		{"review", false},
		// Wrong suffix: never filesystem-mode.
		{"./foo.md", false},
		{"./foo", false},
		{"./foo.prompt", false},
		// Empty is conservatively false.
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			if got := IsFilesystemPromptPath(tc.path); got != tc.want {
				t.Errorf("IsFilesystemPromptPath(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// TestPromptFileFilesystemModeComposesSiblings verifies the load-bearing
// commit-fedb49d behavior: a .prompt.md path with a directory component
// causes PromptFile.assemble to inject the file's parent directory as a
// temporary anchor at the front of the chain so sibling
// <basename>.pre.*.md fragments wrap the body — the same composition
// rule an in-anchor role would get.
func TestPromptFileFilesystemModeComposesSiblings(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("widget.prompt.md", "WIDGET BODY")
	write("widget.pre.intro.md", "WIDGET INTRO")
	write("widget.post.outro.md", "WIDGET OUTRO")

	// Pass an *inherited* anchor base via env.Assembler() to confirm the
	// injection adds to (rather than replaces) the standard chain.
	innerFS := fstest.MapFS{
		"prompts/widget.pre.fromchain.md": &fstest.MapFile{Data: []byte("FROM CHAIN")},
	}
	base := assembler.New(assembler.BuildAnchors("", "", innerFS))
	env := envWithAssembler(base)

	ctx := &stubCtx{
		env:  env,
		vars: assembler.MapVars{Prompt: map[string]string{"name": "widget"}},
		mode: ModeReal,
	}

	pathArg := filepath.Join(dir, "widget.prompt.md")
	out, err := (PromptFile{Path: pathArg}).Resolve(ctx)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	for _, want := range []string{"WIDGET INTRO", "WIDGET BODY", "WIDGET OUTRO", "FROM CHAIN"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// Ordering invariant from the assembler: pre fragments come before
	// role main which comes before post fragments.
	bodyIdx := strings.Index(out, "WIDGET BODY")
	preIdx := strings.Index(out, "WIDGET INTRO")
	postIdx := strings.Index(out, "WIDGET OUTRO")
	if preIdx >= bodyIdx || bodyIdx >= postIdx {
		t.Errorf("expected pre<body<post, got pre=%d body=%d post=%d", preIdx, bodyIdx, postIdx)
	}
}

// TestPromptFileFilesystemModeEmptyRoleErrors verifies the
// commit-fedb49d guard: a path whose basename is just ".prompt.md"
// (no role stem) errors clearly rather than silently looking up an
// empty role name in the anchor chain.
func TestPromptFileFilesystemModeEmptyRoleErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".prompt.md"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}
	base := assembler.New(nil)
	env := envWithAssembler(base)
	ctx := &stubCtx{env: env, mode: ModeReal}

	_, err := (PromptFile{Path: filepath.Join(dir, ".prompt.md")}).Resolve(ctx)
	if err == nil {
		t.Fatal("expected error for empty role basename, got nil")
	}
	if !strings.Contains(err.Error(), "empty role basename") {
		t.Errorf("error should mention 'empty role basename', got: %v", err)
	}
}

// TestPromptFileFilesystemModeWithPrePost verifies that PromptFile's
// PrePrompt / PostPrompt knobs flow into the assembler's AssembleOptions
// in filesystem-path mode — so cmd-layer wrappers (`--pre-prompt`,
// `--post-prompt`) end up as the outermost framing.
func TestPromptFileFilesystemModeWithPrePost(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "foo.prompt.md"), []byte("BODY"), 0o644); err != nil {
		t.Fatal(err)
	}
	base := assembler.New(nil)
	env := envWithAssembler(base)
	ctx := &stubCtx{env: env, mode: ModeReal}

	out, err := (PromptFile{
		Path:       filepath.Join(dir, "foo.prompt.md"),
		PrePrompt:  "CLI-PRE",
		PostPrompt: "CLI-POST",
	}).Resolve(ctx)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	preIdx := strings.Index(out, "CLI-PRE")
	bodyIdx := strings.Index(out, "BODY")
	postIdx := strings.Index(out, "CLI-POST")
	if preIdx < 0 || bodyIdx < 0 || postIdx < 0 {
		t.Fatalf("missing marker(s):\n%s", out)
	}
	if preIdx >= bodyIdx || bodyIdx >= postIdx {
		t.Errorf("expected pre<body<post, got pre=%d body=%d post=%d", preIdx, bodyIdx, postIdx)
	}
}
