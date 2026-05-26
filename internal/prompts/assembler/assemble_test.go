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
	res, err := a.Assemble("review", mkVars(), nil)
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
	res, err := a.Assemble("report/security", mkVars(), nil)
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
	want := "ROOT-PRE\n\nDIR-PRE\n\nROLE-PRE\n\nMAIN-BODY\n\nROLE-POST\n\nDIR-POST"
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
	res, err := a.Assemble("report/security", mkVars(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "ROOT-PRE-NAMED\n\nROOT-PRE-SINGLE\n\nDIR-PRE-SINGLE\n\nROLE-PRE-SINGLE\n\nMAIN\n\nROLE-POST-SINGLE\n\nDIR-POST-SINGLE"
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
	res, err := a.Assemble("report/security", mkVars(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Prompt, "description") || strings.Contains(res.Prompt, "---") {
		t.Fatalf("frontmatter leaked into prompt:\n%q", res.Prompt)
	}
	if res.Prompt != "ROOT-PRE\n\nMAIN-BODY" {
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
	_, err := a.Assemble("report/security", mkVars(), nil)
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
	_, err := a.Assemble("report/security", mkVars(), nil)
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
	res, err := a.Assemble("report/security", mkVars(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "Working on ateam.\n\nRole: security (action report)."
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
	res, err := a.Assemble("report/security", mkVars(), nil)
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
	res, err := a.Assemble("report/security", mkVars(), nil)
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
	res, err := a.Assemble("report/security", mkVars(), nil)
	if err != nil {
		t.Fatal(err)
	}
	// _pre.intro keeps embedded slot (most-general), project content;
	// _pre.extra is project-only (project slot).
	want := "PROJECT-OVERRIDE\n\nPROJECT-EXTRA\n\nMAIN"
	if res.Prompt != want {
		t.Fatalf("Prompt = %q\nwant %q", res.Prompt, want)
	}
}
