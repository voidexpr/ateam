package assembler

import "strings"

// VarRenameMap is the closed mapping from legacy ALL_CAPS variable names to
// their dotted equivalents. Used by the Phase C migrator to rewrite
// user-authored prompts on first contact. Unknown ALL_CAPS tokens (literal
// text the user wrote that isn't an ateam variable) are left untouched.
//
// Keys are the bare identifier (no surrounding {{ }}); values are the dotted
// form (also no braces).
var VarRenameMap = map[string]string{
	// Identity
	"ROLE":   "prompt.name",
	"ACTION": "prompt.action",

	// Project
	"PROJECT_NAME":      "project.name",
	"PROJECT_FULL_PATH": "project.full_path",
	"PROJECT_DIR":       "project.dir",

	// Per-execution
	"BATCH":                "exec.batch",
	"TIMESTAMP":            "exec.timestamp",
	"PROFILE":              "exec.profile",
	"EXEC_ID":              "exec.id",
	"AGENT":                "exec.agent",
	"MODEL":                "exec.model",
	"EFFORT":               "exec.effort",
	"MAX_BUDGET_USD":       "exec.max_budget_usd",
	"MAX_BUDGET_USD_BATCH": "exec.max_budget_usd_batch",
	"OUTPUT_DIR":           "exec.output_dir",
	"OUTPUT_FILE":          "exec.output_file",
	"SUBRUN_ARGS":          "exec.subrun_args",
	"EXECUTION_DIR":        "exec.output_dir", // legacy alias for code_management_prompt.md

	// Container
	"CONTAINER_TYPE": "container.type",
	"CONTAINER_NAME": "container.name",

	// Role-set computations
	"ROLE_REPORTS": "role.reports",

	// ateam self-docs
	"ATEAM_OWN_README":    "ateam.own_readme",
	"ATEAM_OWN_COMMANDS":  "ateam.own_commands",
	"ATEAM_OWN_CONFIG":    "ateam.own_config",
	"ATEAM_OWN_ISOLATION": "ateam.own_isolation",
	"ATEAM_OWN_ROLES":     "ateam.own_roles",

	// Action-specific context bundles
	"AUTO_ROLES_MARKER":                "ateam.auto_roles_marker",
	"ATEAM_AUTO_ROLES_COMMANDS_OUTPUT": "exec.auto_roles_commands_output",
	"EXEC_DEBUG_CONTEXT":               "exec.debug_context",
}

// VarLiteralRewrites are ALL_CAPS tokens that v1 no longer expresses as
// variables. They are rewritten to a fixed string instead of a `{{ns.key}}`
// reference. SOURCE_DIR was always substituted with "." by the old runner
// regardless of context, so v1 just inlines the literal.
var VarLiteralRewrites = map[string]string{
	"SOURCE_DIR": ".",
}

// RewriteContent walks content, looking for `{{NAME}}` tokens where NAME is a
// bare ALL_CAPS identifier (^[A-Z][A-Z0-9_]*$). Known names are rewritten via
// VarRenameMap (to `{{dotted.form}}`) or VarLiteralRewrites (to a literal
// value). Unknown ALL_CAPS tokens and any non-matching directive — directives
// with spaces, already-dotted variables, etc. — are emitted unchanged.
//
// The walker uses balanced `{{ ... }}` so nested directives don't get mangled
// (the outer brace is the one we substitute against).
func RewriteContent(content string) string {
	var out strings.Builder
	i := 0
	for i < len(content) {
		j := strings.Index(content[i:], "{{")
		if j < 0 {
			out.WriteString(content[i:])
			break
		}
		out.WriteString(content[i : i+j])
		start := i + j + 2
		closeIdx := findClosingBrace(content, start)
		if closeIdx < 0 {
			out.WriteString(content[i+j:])
			break
		}
		token := strings.TrimSpace(content[start:closeIdx])
		if replacement, ok := lookupRewrite(token); ok {
			out.WriteString(replacement)
		} else {
			// Preserve original — including any inner whitespace.
			out.WriteString("{{")
			out.WriteString(content[start:closeIdx])
			out.WriteString("}}")
		}
		i = closeIdx + 2
	}
	return out.String()
}

func lookupRewrite(token string) (string, bool) {
	if !isAllCapsIdent(token) {
		return "", false
	}
	if dotted, ok := VarRenameMap[token]; ok {
		return "{{" + dotted + "}}", true
	}
	if literal, ok := VarLiteralRewrites[token]; ok {
		return literal, true
	}
	return "", false
}

// isAllCapsIdent reports whether s matches ^[A-Z][A-Z0-9_]*$ — the shape of
// legacy template variables.
func isAllCapsIdent(s string) bool {
	if s == "" {
		return false
	}
	if s[0] < 'A' || s[0] > 'Z' {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case c == '_':
		default:
			return false
		}
	}
	return true
}
