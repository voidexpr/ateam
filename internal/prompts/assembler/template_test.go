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
		Args:      map[string]string{"ignore_previous_report": "true"},
		Roles:     map[string]string{"enabled": "code.bugs,security,test.gaps"},
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

// TestMapVarsEnumerate covers the snapshot pattern: every populated
// namespace map is cloned into the result; empty namespaces are
// omitted; env.* is intentionally excluded (lookup-only).
//
// Each enumerated map is independent of the source — mutating the
// returned map must not propagate back. Pins the "snapshot" semantics
// the future script-Prompt impls (JSON-stdin) depend on.
func TestMapVarsEnumerate(t *testing.T) {
	v := mkVars()
	snap := v.Enumerate()

	// Populated namespaces present.
	for _, ns := range []string{"prompt", "exec", "project", "container", "ateam", "args", "roles"} {
		if _, ok := snap[ns]; !ok {
			t.Errorf("Enumerate(): missing namespace %q", ns)
		}
	}
	// Unpopulated namespaces (git, role) omitted — they were nil in mkVars.
	for _, ns := range []string{"git", "role"} {
		if _, ok := snap[ns]; ok {
			t.Errorf("Enumerate(): expected nil-map namespace %q to be omitted, got %v", ns, snap[ns])
		}
	}
	// env.* is callback-based and intentionally not in the snapshot.
	if _, ok := snap["env"]; ok {
		t.Errorf("Enumerate(): env namespace must be excluded (callback-based)")
	}

	// Spot-check a value.
	if got := snap["args"]["ignore_previous_report"]; got != "true" {
		t.Errorf("args.ignore_previous_report = %q, want true", got)
	}

	// Mutation isolation: caller-side change must not leak back.
	snap["args"]["ignore_previous_report"] = "tampered"
	again := v.Enumerate()
	if again["args"]["ignore_previous_report"] != "true" {
		t.Errorf("MapVars.Enumerate must return a clone; saw tampered value: %v", again["args"])
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
		// args.* / roles.* — factory-curated namespaces. Wired here
		// to prove the resolver covers them; consumers live in
		// cmd/report_factory.go (args.ignore_previous_report) and
		// internal/root/resolve.go (roles.enabled).
		{"args namespace resolves", "skip={{args.ignore_previous_report}}", "skip=true"},
		{"roles namespace resolves", "enabled={{roles.enabled}}", "enabled=code.bugs,security,test.gaps"},
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

// TestRenderUnknownKeyInClosedNamespaceErrors pins the spec invariant
// (line 638): a known namespace + unknown key is always an error, not
// a silent empty substitution or passthrough. Covers args.* and
// roles.* — namespaces that ship with a small populated key set today
// and explicitly promise unknown-key-errors in the Future section's
// "wire on demand" pattern.
func TestRenderUnknownKeyInClosedNamespaceErrors(t *testing.T) {
	e := NewEngine(nil, 0)
	v := mkVars()
	cases := []struct {
		name string
		body string
	}{
		// roles.* — only roles.enabled is wired today; future keys
		// (roles.all, roles.disabled, roles.selected, roles.failed,
		// roles.aged_out) must error, not render empty.
		{"roles.all unknown", "{{roles.all}}"},
		{"roles.disabled unknown", "{{roles.disabled}}"},
		{"roles.selected unknown", "{{roles.selected}}"},
		{"roles.failed unknown", "{{roles.failed}}"},
		{"roles.aged_out unknown", "{{roles.aged_out}}"},
		// args.* — only args.ignore_previous_report is wired today;
		// other keys (args.no_project_info, args.roles, args.batch,
		// args.verbose, args.force, args.print) must error.
		{"args.no_project_info unknown", "{{args.no_project_info}}"},
		{"args.roles unknown", "{{args.roles}}"},
		{"args.batch unknown", "{{args.batch}}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := e.Render(tc.body, v)
			if err == nil {
				t.Fatalf("expected unknown-key error for %s", tc.body)
			}
			if !strings.Contains(err.Error(), "unknown key") {
				t.Errorf("error should say 'unknown key', got: %v", err)
			}
		})
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
