package assembler

import (
	"strings"
	"testing"
)

func mkVars() MapVars {
	return MapVars{
		Prompt:    map[string]string{"name": "security", "path": "report/security", "action": "report"},
		Exec:      map[string]string{"id": "42", "output_dir": "/tmp/runtime/42"},
		Project:   map[string]string{"name": "ateam", "info": "ATeam project context block"},
		Container: map[string]string{"type": "docker"},
		Ateam:     map[string]string{"own_bin": "/usr/local/bin/ateam"},
		EnvLookup: func(name string) (string, bool) {
			switch name {
			case "HOME":
				return "/home/me", true
			case "EMPTY":
				return "", true
			}
			return "", false
		},
	}
}

func TestRenderVariables(t *testing.T) {
	e := NewEngine(nil, 0)
	v := mkVars()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"single", "Hello {{prompt.name}}", "Hello security"},
		{"multiple", "{{project.name}}/{{prompt.action}}/{{prompt.name}}", "ateam/report/security"},
		{"empty value", "X={{env.EMPTY}}Y", "X=Y"},
		{"unknown namespace passes through", "{{foo.bar}} stays", "{{foo.bar}} stays"},
		{"unknown non-ALL_CAPS token passes through", "{{legacy}} too", "{{legacy}} too"},
		{"no directives", "no braces here", "no braces here"},
		{"unterminated {{ kept literal", "open {{ never closed", "open {{ never closed"},
		{"adjacent", "{{prompt.name}}{{prompt.action}}", "securityreport"},
		{"deep nesting in literal", "{{prompt.name}} and {{ateam.own_bin}}", "security and /usr/local/bin/ateam"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := e.Render(tc.in, v)
			if err != nil {
				t.Fatalf("Render err: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRenderUnknownKeyErrors(t *testing.T) {
	e := NewEngine(nil, 0)
	v := mkVars()
	_, err := e.Render("{{prompt.nope}}", v)
	if err == nil || !strings.Contains(err.Error(), "prompt.nope") {
		t.Fatalf("expected unknown-key error, got %v", err)
	}
}

func TestRenderEnvMissing(t *testing.T) {
	e := NewEngine(nil, 0)
	v := mkVars()
	_, err := e.Render("{{env.NOPE}}", v)
	if err == nil || !strings.Contains(err.Error(), "NOPE") {
		t.Fatalf("expected env missing error, got %v", err)
	}
}

func TestRenderEnvNoLookup(t *testing.T) {
	e := NewEngine(nil, 0)
	v := MapVars{} // no EnvLookup
	_, err := e.Render("{{env.HOME}}", v)
	if err == nil || !strings.Contains(err.Error(), "HOME") {
		t.Fatalf("expected error when EnvLookup nil, got %v", err)
	}
}

func TestRenderInclude(t *testing.T) {
	anchors := mkAnchors(
		map[string]string{"fragment.md": "hello {{prompt.name}}"},
		nil,
		map[string]string{"fragment.md": "embedded version", "extra.md": "extra body"},
	)
	a := New(anchors)
	e := NewEngine(a, 0)
	v := mkVars()

	t.Run("first-match wins", func(t *testing.T) {
		got, err := e.Render("Begin\n{{include fragment.md}}\nEnd", v)
		if err != nil {
			t.Fatal(err)
		}
		want := "Begin\nhello security\nEnd"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("path uses var substitution", func(t *testing.T) {
		got, err := e.Render("{{include {{prompt.name}}/../extra.md}}", v)
		// Path becomes "security/../extra.md". fs.FS doesn't normalize .. — should not match.
		if err == nil {
			t.Fatalf("expected error for unresolvable include, got %q", got)
		}
	})
}

func TestRenderIncludeMissingRequired(t *testing.T) {
	a := New(mkAnchors(nil, nil, nil))
	e := NewEngine(a, 0)
	_, err := e.Render("{{include missing.md}}", mkVars())
	if err == nil || !strings.Contains(err.Error(), "missing.md") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestRenderIncludeOptional(t *testing.T) {
	a := New(mkAnchors(nil, nil, nil))
	e := NewEngine(a, 0)
	got, err := e.Render("before {{include? missing.md}} after", mkVars())
	if err != nil {
		t.Fatal(err)
	}
	if got != "before  after" {
		t.Fatalf("got %q", got)
	}
}

func TestRenderIncludeGlob(t *testing.T) {
	anchors := mkAnchors(
		map[string]string{"frags/_pre.local.md": "P-local: {{prompt.name}}"},
		nil,
		map[string]string{
			"frags/_pre.intro.md": "E-intro",
			"frags/_pre.other.md": "E-other",
		},
	)
	a := New(anchors)
	e := NewEngine(a, 0)
	got, err := e.Render("{{include_glob frags/_pre.*.md}}", mkVars())
	if err != nil {
		t.Fatal(err)
	}
	want := "E-intro\n\nE-other\n\nP-local: security"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestRenderIncludeGlobEmpty(t *testing.T) {
	a := New(mkAnchors(nil, nil, nil))
	e := NewEngine(a, 0)
	got, err := e.Render("[{{include_glob frags/_pre.*.md}}]", mkVars())
	if err != nil {
		t.Fatal(err)
	}
	if got != "[]" {
		t.Fatalf("got %q", got)
	}
}

func TestRenderCycleDepthLimited(t *testing.T) {
	// a.md includes b.md, b.md includes a.md.
	anchors := mkAnchors(
		nil, nil,
		map[string]string{
			"a.md": "A {{include b.md}}",
			"b.md": "B {{include a.md}}",
		},
	)
	a := New(anchors)
	e := NewEngine(a, 0)
	_, err := e.Render("{{include a.md}}", mkVars())
	if err == nil || !strings.Contains(err.Error(), "depth exceeded") {
		t.Fatalf("expected depth-exceeded error, got %v", err)
	}
}

func TestRenderUnknownDirectivePassesThrough(t *testing.T) {
	e := NewEngine(nil, 0)
	got, err := e.Render("call {{shell echo hi}}", mkVars())
	if err != nil {
		t.Fatal(err)
	}
	want := "call {{shell echo hi}}"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRenderNestedDirective(t *testing.T) {
	// Path uses {{prompt.name}} inside an {{include ...}} — the outer `}}`
	// must balance with the outer `{{`, not the inner one.
	anchors := mkAnchors(
		nil, nil,
		map[string]string{"security.prompt.md": "BODY for {{prompt.name}}"},
	)
	a := New(anchors)
	e := NewEngine(a, 0)
	got, err := e.Render("[{{include {{prompt.name}}.prompt.md}}]", mkVars())
	if err != nil {
		t.Fatal(err)
	}
	want := "[BODY for security]"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRenderAllCapsPassesThrough(t *testing.T) {
	// Engine-side ALL_CAPS compat was removed; the engine no longer maps
	// {{ROLE}} → {{prompt.name}} etc. Tokens pass through verbatim so the
	// runner-side substitution layer can fill {{BATCH}} / {{EXEC_ID}} /
	// etc. at execution time.
	e := NewEngine(nil, 0)
	v := mkVars()
	for _, tok := range []string{"ROLE", "BATCH", "EXEC_ID", "OUTPUT_DIR"} {
		t.Run(tok, func(t *testing.T) {
			got, err := e.Render("x={{"+tok+"}}y", v)
			if err != nil {
				t.Fatalf("{{%s}}: unexpected error %v", tok, err)
			}
			want := "x={{" + tok + "}}y"
			if got != want {
				t.Fatalf("{{%s}}: got %q want %q", tok, got, want)
			}
		})
	}
}

func TestRenderIncludeRecursiveExpansion(t *testing.T) {
	// Included content itself contains a var reference; it expands.
	anchors := mkAnchors(
		nil, nil,
		map[string]string{"inner.md": "name={{prompt.name}}"},
	)
	a := New(anchors)
	e := NewEngine(a, 0)
	got, err := e.Render("[{{include inner.md}}]", mkVars())
	if err != nil {
		t.Fatal(err)
	}
	if got != "[name=security]" {
		t.Fatalf("got %q", got)
	}
}
