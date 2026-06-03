package assembler

import "testing"

func TestRewriteContent(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			"plain rename",
			"role={{ROLE}}, action={{ACTION}}",
			"role={{prompt.name}}, action={{prompt.action}}",
		},
		{
			"output dir + file",
			"write to {{OUTPUT_DIR}}/{{OUTPUT_FILE}}",
			"write to {{exec.output_dir}}/{{exec.output_file}}",
		},
		{
			"legacy alias",
			"cd {{EXECUTION_DIR}}",
			"cd {{exec.output_dir}}",
		},
		{
			"ateam self-docs",
			"see {{ATEAM_OWN_README}} and {{ATEAM_OWN_ISOLATION}}",
			"see {{ateam.own_readme}} and {{ateam.own_isolation}}",
		},
		{
			"source dir literal",
			"work in {{SOURCE_DIR}}",
			"work in .",
		},
		{
			"unknown ALL_CAPS left alone",
			"see {{MY_USER_TOKEN}} and {{HOME}}",
			"see {{MY_USER_TOKEN}} and {{HOME}}",
		},
		{
			"already dotted is not touched",
			"{{prompt.name}} stays",
			"{{prompt.name}} stays",
		},
		{
			"directive with space not touched",
			"{{include foo.md}} stays",
			"{{include foo.md}} stays",
		},
		{
			"plain text unchanged",
			"no braces here",
			"no braces here",
		},
		{
			"mixed",
			"# {{ACTION}} for {{ROLE}}\n\n{{include _pre.md}}\n\n{{MY_CUSTOM}} keeps.",
			"# {{prompt.action}} for {{prompt.name}}\n\n{{include _pre.md}}\n\n{{MY_CUSTOM}} keeps.",
		},
		{
			"nested directive preserved",
			"{{include {{ROLE}}.prompt.md}}",
			// Outer is a directive (has space) → left alone. Inner ROLE is
			// inside the directive's body, not seen as a separate token by
			// the walker — that's correct: nested rewriting happens at
			// render time via the engine, not at migration time.
			"{{include {{ROLE}}.prompt.md}}",
		},
		{
			"unterminated brace",
			"open {{ never closed",
			"open {{ never closed",
		},
		{
			"empty token preserved",
			"{{}}",
			"{{}}",
		},
		{
			"adjacent rewrites",
			"{{ROLE}}{{ACTION}}",
			"{{prompt.name}}{{prompt.action}}",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RewriteContent(tc.in)
			if got != tc.want {
				t.Fatalf("RewriteContent(%q)\n  got:  %q\n  want: %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestVarRenameMapCoversCurrentRunnerVars(t *testing.T) {
	// Every ALL_CAPS variable known to internal/runner/template.go's Replacer
	// must have a v1 mapping — otherwise the migrator silently leaves it as a
	// dead variable in user prompts. Update this list when adding/removing a
	// runner variable.
	current := []string{
		"PROJECT_NAME", "PROJECT_FULL_PATH", "PROJECT_DIR",
		"ROLE", "ACTION", "BATCH", "TIMESTAMP", "PROFILE", "EXEC_ID",
		"AGENT", "MODEL",
		"EFFORT", "MAX_BUDGET_USD", "MAX_BUDGET_USD_BATCH",
		"PROFILE_ARGS",
		"CONTAINER_TYPE", "CONTAINER_NAME",
		"OUTPUT_DIR", "OUTPUT_FILE", "EXECUTION_DIR",
		"ATEAM_OWN_README", "ATEAM_OWN_COMMANDS", "ATEAM_OWN_CONFIG",
		"ATEAM_OWN_ISOLATION", "ATEAM_OWN_ROLES",
		"AUTO_ROLES_MARKER", "ATEAM_AUTO_ROLES_COMMANDS_OUTPUT",
	}
	for _, name := range current {
		if _, ok := VarRenameMap[name]; !ok {
			t.Errorf("legacy variable %q has no mapping in VarRenameMap", name)
		}
	}
	// Special-cases that don't go in VarRenameMap.
	for _, name := range []string{"SOURCE_DIR"} {
		if _, ok := VarLiteralRewrites[name]; !ok {
			t.Errorf("legacy literal %q has no mapping in VarLiteralRewrites", name)
		}
	}
}

func TestIsAllCapsIdent(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"ROLE", true},
		{"PROJECT_NAME", true},
		{"A1", true},
		{"A_1_B", true},
		{"", false},
		{"role", false},
		{"Role", false},
		{"1ROLE", false},
		{"_ROLE", false},
		{"ROLE.X", false},
		{"ROLE-X", false},
		{"include foo", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := isAllCapsIdent(tc.in); got != tc.want {
				t.Fatalf("isAllCapsIdent(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
