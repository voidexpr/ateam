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
