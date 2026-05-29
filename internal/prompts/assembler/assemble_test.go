package assembler

import (
	"strings"
	"testing"
)

func TestAssembleSingleton(t *testing.T) {
	// Top-level prompt (no dir) with just a main file.
	anchors := mkAnchors(
		nil, nil,
		map[string]string{"review.prompt.md": "REVIEW BODY"},
	)
	a := New(anchors)
	res, err := a.Assemble("review", mkVars(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Prompt != "REVIEW BODY" {
		t.Fatalf("Prompt = %q", res.Prompt)
	}
	if len(res.Sections) != 1 || res.Sections[0].Slot != "role_main" {
		t.Fatalf("Sections = %+v", res.Sections)
	}
}

func TestAssembleNestedAllSlots(t *testing.T) {
	// report/security with: root pre, dir pre, role pre, main, role post, dir post.
	anchors := mkAnchors(
		map[string]string{
			"_pre.context.md":              "ROOT-PRE",
			"report/_pre.intro.md":         "DIR-PRE",
			"report/security.pre.scope.md": "ROLE-PRE",
			"report/security.post.note.md": "ROLE-POST",
			"report/_post.format.md":       "DIR-POST",
		},
		nil,
		map[string]string{
			"report/security.prompt.md": "MAIN-BODY",
		},
	)
	a := New(anchors)
	res, err := a.Assemble("report/security", mkVars(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantOrder := []string{
		"root_pre", "dir_pre:report", "role_pre", "role_main", "role_post", "dir_post:report",
	}
	if len(res.Sections) != len(wantOrder) {
		t.Fatalf("got %d sections, want %d:\n%+v", len(res.Sections), len(wantOrder), res.Sections)
	}
	for i, s := range res.Sections {
		if s.Slot != wantOrder[i] {
			t.Errorf("section[%d] slot = %q, want %q", i, s.Slot, wantOrder[i])
		}
	}
	want := "ROOT-PRE\n\n---\n\nDIR-PRE\n\n---\n\nROLE-PRE\n\n---\n\nMAIN-BODY\n\n---\n\nROLE-POST\n\n---\n\nDIR-POST"
	if res.Prompt != want {
		t.Fatalf("Prompt mismatch:\n got: %q\nwant: %q", res.Prompt, want)
	}
}

func TestAssembleSingletonFragments(t *testing.T) {
	// Singleton pre/post (no <NAME>) must compose alongside named fragments at
	// each slot. Lexically, `_pre.md` sorts after `_pre.context.md`.
	anchors := mkAnchors(
		nil, nil,
		map[string]string{
			"_pre.md":                   "ROOT-PRE-SINGLE",
			"_pre.context.md":           "ROOT-PRE-NAMED",
			"report/_pre.md":            "DIR-PRE-SINGLE",
			"report/security.pre.md":    "ROLE-PRE-SINGLE",
			"report/security.prompt.md": "MAIN",
			"report/security.post.md":   "ROLE-POST-SINGLE",
			"report/_post.md":           "DIR-POST-SINGLE",
		},
	)
	a := New(anchors)
	res, err := a.Assemble("report/security", mkVars(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "ROOT-PRE-NAMED\n\n---\n\nROOT-PRE-SINGLE\n\n---\n\nDIR-PRE-SINGLE\n\n---\n\nROLE-PRE-SINGLE\n\n---\n\nMAIN\n\n---\n\nROLE-POST-SINGLE\n\n---\n\nDIR-POST-SINGLE"
	if res.Prompt != want {
		t.Fatalf("Prompt =\n%q\nwant\n%q", res.Prompt, want)
	}
}

func TestAssembleStripsFrontmatter(t *testing.T) {
	// Frontmatter on the role main (and on fragments) must be parsed off, not
	// rendered into the output.
	anchors := mkAnchors(
		nil, nil,
		map[string]string{
			"_pre.context.md":           "---\ndescription: ctx\n---\nROOT-PRE",
			"report/security.prompt.md": "---\ndescription: sec\ndeprecated: true\n---\nMAIN-BODY",
		},
	)
	a := New(anchors)
	res, err := a.Assemble("report/security", mkVars(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Prompt, "description") {
		t.Fatalf("frontmatter leaked into prompt:\n%q", res.Prompt)
	}
	if res.Prompt != "ROOT-PRE\n\n---\n\nMAIN-BODY" {
		t.Fatalf("Prompt = %q", res.Prompt)
	}
}

func TestAssembleBadFrontmatterErrors(t *testing.T) {
	// Unknown frontmatter keys must surface as an error at assembly time,
	// tagged with the anchor:path location.
	anchors := mkAnchors(
		nil, nil,
		map[string]string{
			"report/security.prompt.md": "---\nbogus: nope\n---\nMAIN",
		},
	)
	a := New(anchors)
	_, err := a.Assemble("report/security", mkVars(), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("expected unknown-key frontmatter error, got %v", err)
	}
	if !strings.Contains(err.Error(), "report/security.prompt.md") {
		t.Fatalf("error should name the offending file, got %v", err)
	}
}

func TestAssembleMissingMainErrors(t *testing.T) {
	anchors := mkAnchors(
		map[string]string{"_pre.context.md": "ROOT-PRE"},
		nil, nil,
	)
	a := New(anchors)
	_, err := a.Assemble("report/security", mkVars(), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "no role main") {
		t.Fatalf("expected missing-main error, got %v", err)
	}
}

func TestAssembleVarSubstitution(t *testing.T) {
	anchors := mkAnchors(
		nil, nil,
		map[string]string{
			"_pre.context.md":           "Working on {{project.name}}.",
			"report/security.prompt.md": "Role: {{prompt.name}} (action {{prompt.action}}).",
		},
	)
	a := New(anchors)
	res, err := a.Assemble("report/security", mkVars(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "Working on ateam.\n\n---\n\nRole: security (action report)."
	if res.Prompt != want {
		t.Fatalf("Prompt = %q, want %q", res.Prompt, want)
	}
}

func TestAssembleAllCapsCompatViaEngine(t *testing.T) {
	// Defaults still use {{ROLE}} / {{PROJECT_NAME}}; engine compat shim
	// must resolve them during assembly.
	anchors := mkAnchors(
		nil, nil,
		map[string]string{
			"report/security.prompt.md": "ROLE={{ROLE}} PROJECT={{PROJECT_NAME}}",
		},
	)
	a := New(anchors)
	res, err := a.Assemble("report/security", mkVars(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "ROLE=security PROJECT=ateam"
	if res.Prompt != want {
		t.Fatalf("Prompt = %q, want %q", res.Prompt, want)
	}
}

func TestAssembleEmptyFragmentsSkipped(t *testing.T) {
	// _post.format.md is empty (or just whitespace) — should NOT add a
	// section to the result. The placeholder _post.format.md in defaults
	// is HTML-comment-only and would render to whitespace, so this
	// matters for clean composition.
	anchors := mkAnchors(
		nil, nil,
		map[string]string{
			"report/_post.empty.md":     "   \n\n  \t  \n",
			"report/security.prompt.md": "MAIN",
		},
	)
	a := New(anchors)
	res, err := a.Assemble("report/security", mkVars(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sections) != 1 {
		t.Fatalf("expected only role_main section, got %+v", res.Sections)
	}
	if res.Prompt != "MAIN" {
		t.Fatalf("Prompt = %q", res.Prompt)
	}
}

func TestAssembleOverloadAcrossAnchors(t *testing.T) {
	// Project provides its own version of an embedded fragment + a new one.
	anchors := mkAnchors(
		map[string]string{
			"report/_pre.intro.md": "PROJECT-OVERRIDE",
			"report/_pre.extra.md": "PROJECT-EXTRA",
		},
		nil,
		map[string]string{
			"report/_pre.intro.md":      "EMBEDDED-INTRO",
			"report/security.prompt.md": "MAIN",
		},
	)
	a := New(anchors)
	res, err := a.Assemble("report/security", mkVars(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// _pre.intro keeps embedded slot (most-general), project content;
	// _pre.extra is project-only (project slot).
	want := "PROJECT-OVERRIDE\n\n---\n\nPROJECT-EXTRA\n\n---\n\nMAIN"
	if res.Prompt != want {
		t.Fatalf("Prompt = %q\nwant %q", res.Prompt, want)
	}
}

func TestAssemblePrePromptWrap(t *testing.T) {
	anchors := mkAnchors(
		nil, nil,
		map[string]string{"review.prompt.md": "BODY"},
	)
	a := New(anchors)
	opts := &AssembleOptions{PrePrompt: "PRE"}
	res, err := a.Assemble("review", mkVars(), nil, opts)
	if err != nil {
		t.Fatal(err)
	}
	want := "PRE\n\n---\n\nBODY"
	if res.Prompt != want {
		t.Fatalf("Prompt = %q, want %q", res.Prompt, want)
	}
	// First section is the CLI pre.
	if len(res.Sections) != 2 || res.Sections[0].Slot != "cli_pre_prompt" || res.Sections[0].Anchor != "cli" {
		t.Fatalf("first section = %+v", res.Sections[0])
	}
	if res.Sections[0].Path != "(--pre-prompt)" {
		t.Errorf("first section path = %q, want (--pre-prompt)", res.Sections[0].Path)
	}
}

func TestAssemblePostPromptWrap(t *testing.T) {
	anchors := mkAnchors(
		nil, nil,
		map[string]string{"review.prompt.md": "BODY"},
	)
	a := New(anchors)
	opts := &AssembleOptions{PostPrompt: "POST"}
	res, err := a.Assemble("review", mkVars(), nil, opts)
	if err != nil {
		t.Fatal(err)
	}
	want := "BODY\n\n---\n\nPOST"
	if res.Prompt != want {
		t.Fatalf("Prompt = %q, want %q", res.Prompt, want)
	}
	last := res.Sections[len(res.Sections)-1]
	if last.Slot != "cli_post_prompt" || last.Anchor != "cli" || last.Path != "(--post-prompt)" {
		t.Fatalf("last section = %+v", last)
	}
}

func TestAssembleReplaceRoleMain(t *testing.T) {
	// Anchor has a main file; opts.ReplaceRoleMain overrides it. Framing
	// (dir_pre / dir_post) still composes.
	anchors := mkAnchors(
		map[string]string{
			"_pre.context.md":        "ROOT-PRE",
			"report/_post.format.md": "DIR-POST",
		},
		nil,
		map[string]string{"report/security.prompt.md": "ORIGINAL-MAIN"},
	)
	a := New(anchors)
	opts := &AssembleOptions{ReplaceRoleMain: "CUSTOM-BODY"}
	res, err := a.Assemble("report/security", mkVars(), nil, opts)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Prompt, "ORIGINAL-MAIN") {
		t.Errorf("expected ORIGINAL-MAIN replaced, got:\n%s", res.Prompt)
	}
	if !strings.Contains(res.Prompt, "CUSTOM-BODY") {
		t.Errorf("expected CUSTOM-BODY in result, got:\n%s", res.Prompt)
	}
	if !strings.Contains(res.Prompt, "ROOT-PRE") || !strings.Contains(res.Prompt, "DIR-POST") {
		t.Errorf("framing should still compose, got:\n%s", res.Prompt)
	}
	// role_main section comes from CLI now.
	var foundMain bool
	for _, s := range res.Sections {
		if s.Slot == "role_main" {
			foundMain = true
			if s.Anchor != "cli" || s.Path != "(--prompt)" {
				t.Errorf("role_main section = %+v, want anchor=cli path=(--prompt)", s)
			}
		}
	}
	if !foundMain {
		t.Error("missing role_main section")
	}
}

func TestAssembleReplaceRoleMainWithoutAnchorFile(t *testing.T) {
	// ReplaceRoleMain works even when no <role>.prompt.md exists in any
	// anchor — the caller's body stands in. Useful when a caller wants to
	// assemble a brand-new prompt without first writing a placeholder file.
	anchors := mkAnchors(
		map[string]string{
			"report/_pre.intro.md": "DIR-PRE",
		},
		nil, nil,
	)
	a := New(anchors)
	opts := &AssembleOptions{ReplaceRoleMain: "ONLY-BODY"}
	res, err := a.Assemble("report/newrole", mkVars(), nil, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Prompt, "ONLY-BODY") || !strings.Contains(res.Prompt, "DIR-PRE") {
		t.Errorf("result:\n%s", res.Prompt)
	}
}

func TestAssembleAllOverridesTogether(t *testing.T) {
	anchors := mkAnchors(
		nil, nil,
		map[string]string{"review.prompt.md": "ANCHOR-MAIN"},
	)
	a := New(anchors)
	opts := &AssembleOptions{
		PrePrompt:       "PRE",
		ReplaceRoleMain: "OVERRIDE-MAIN",
		PostPrompt:      "POST",
	}
	res, err := a.Assemble("review", mkVars(), nil, opts)
	if err != nil {
		t.Fatal(err)
	}
	want := "PRE\n\n---\n\nOVERRIDE-MAIN\n\n---\n\nPOST"
	if res.Prompt != want {
		t.Fatalf("Prompt = %q\nwant %q", res.Prompt, want)
	}
	if strings.Contains(res.Prompt, "ANCHOR-MAIN") {
		t.Error("ANCHOR-MAIN should be replaced")
	}
}

func TestAssembleEmptyOverridesNoOp(t *testing.T) {
	// Empty/whitespace overrides drop silently, same prompt as nil opts.
	anchors := mkAnchors(
		nil, nil,
		map[string]string{"review.prompt.md": "BODY"},
	)
	a := New(anchors)
	for _, opts := range []*AssembleOptions{
		nil,
		{},
		{PrePrompt: "", PostPrompt: "", ReplaceRoleMain: ""},
		{PrePrompt: "  \n\t", PostPrompt: "\n"},
	} {
		res, err := a.Assemble("review", mkVars(), nil, opts)
		if err != nil {
			t.Fatalf("opts=%+v: %v", opts, err)
		}
		if res.Prompt != "BODY" {
			t.Fatalf("opts=%+v: Prompt = %q, want BODY", opts, res.Prompt)
		}
	}
}

// TestAssembleErrorsOnEmptyRoleMainOverride covers a subtle bug: an
// empty/whitespace `<role>.prompt.md` at a more-specific anchor (e.g.
// project) would silently shadow the embedded fallback — Assemble's
// FirstMatch returns the empty project file, addRendered's whitespace
// filter drops the section, and the result has no role_main at all
// (empty Prompt, no error). The fix: error explicitly when the
// resolved role main renders to whitespace.
func TestAssembleErrorsOnEmptyRoleMainOverride(t *testing.T) {
	anchors := mkAnchors(
		map[string]string{"report/security.prompt.md": "   \n"},
		nil,
		map[string]string{"report/security.prompt.md": "REAL ROLE BODY"},
	)
	a := New(anchors)
	_, err := a.Assemble("report/security", mkVars(), nil, nil)
	if err == nil {
		t.Fatal("expected error when project override renders to whitespace, got nil")
	}
	if !strings.Contains(err.Error(), "empty after rendering") {
		t.Errorf("expected 'empty after rendering' in error, got: %v", err)
	}
}

// TestAssembleErrorsOnEmptyReplaceRoleMain mirrors the above for the CLI
// override path: passing only-whitespace via opts.ReplaceRoleMain is a
// hard error, not a silent empty result.
func TestAssembleErrorsOnEmptyReplaceRoleMain(t *testing.T) {
	anchors := mkAnchors(
		nil, nil,
		map[string]string{"review.prompt.md": "EMBEDDED BODY"},
	)
	a := New(anchors)
	opts := &AssembleOptions{ReplaceRoleMain: "   \n\t"}
	_, err := a.Assemble("review", mkVars(), nil, opts)
	if err == nil {
		t.Fatal("expected error when ReplaceRoleMain is whitespace, got nil")
	}
	if !strings.Contains(err.Error(), "empty after rendering") {
		t.Errorf("expected 'empty after rendering' in error, got: %v", err)
	}
}
