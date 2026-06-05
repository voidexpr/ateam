package migrate

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// tokenMapping describes the replacement for a legacy ALL_CAPS prompt
// token. Replacement is normally a dotted template name (rendered as
// {{X}}); when Literal is true it's a verbatim string to substitute in
// place of the token (rendered as "X").
type tokenMapping struct {
	Replacement string
	Literal     bool
}

// legacyPromptTokens is the closed mapping from pre-refactor ALL_CAPS
// prompt-body tokens to their dotted equivalents. It mirrors the deleted
// internal/prompts/assembler/varmap.go (commit a834701) so the migrator
// can surface a usable replacement hint for each token it finds in a
// migrated prompt body. Anything not in this set is either still
// resolved by runner/template.go (runtime.hcl side) or was never a
// substituted variable, so the migrator stays quiet about it.
var legacyPromptTokens = map[string]tokenMapping{
	// Identity
	"ROLE":   {Replacement: "prompt.name"},
	"ACTION": {Replacement: "prompt.action"},

	// Project
	"PROJECT_NAME":      {Replacement: "project.name"},
	"PROJECT_FULL_PATH": {Replacement: "project.full_path"},
	"PROJECT_DIR":       {Replacement: "project.dir"},

	// Per-execution
	"BATCH":                {Replacement: "exec.batch"},
	"TIMESTAMP":            {Replacement: "exec.timestamp"},
	"PROFILE":              {Replacement: "exec.profile"},
	"EXEC_ID":              {Replacement: "exec.id"},
	"AGENT":                {Replacement: "exec.agent"},
	"MODEL":                {Replacement: "exec.model"},
	"EFFORT":               {Replacement: "exec.effort"},
	"MAX_BUDGET_USD":       {Replacement: "exec.max_budget_usd"},
	"MAX_BUDGET_USD_BATCH": {Replacement: "exec.max_budget_usd_batch"},
	"OUTPUT_DIR":           {Replacement: "exec.output_dir"},
	"OUTPUT_FILE":          {Replacement: "exec.output_file"},
	"SUBRUN_ARGS":          {Replacement: "exec.subrun_args"},
	"EXECUTION_DIR":        {Replacement: "exec.output_dir"},

	// Container
	"CONTAINER_TYPE": {Replacement: "container.type"},
	"CONTAINER_NAME": {Replacement: "container.name"},

	// Role-set computations
	"ROLE_REPORTS": {Replacement: "role.reports"},

	// ateam self-docs
	"ATEAM_OWN_README":    {Replacement: "ateam.own_readme"},
	"ATEAM_OWN_COMMANDS":  {Replacement: "ateam.own_commands"},
	"ATEAM_OWN_CONFIG":    {Replacement: "ateam.own_config"},
	"ATEAM_OWN_ISOLATION": {Replacement: "ateam.own_isolation"},
	"ATEAM_OWN_ROLES":     {Replacement: "ateam.own_roles"},

	// Action-specific context bundles
	"AUTO_ROLES_MARKER":                {Replacement: "ateam.auto_roles_marker"},
	"ATEAM_AUTO_ROLES_COMMANDS_OUTPUT": {Replacement: "exec.auto_roles_commands_output"},
	"EXEC_DEBUG_CONTEXT":               {Replacement: "exec.debug_context"},

	// Pre-v1 literal alias — the old runner always rewrote SOURCE_DIR to ".".
	"SOURCE_DIR": {Replacement: ".", Literal: true},
}

var legacyTokenRE = regexp.MustCompile(`{{([A-Z][A-Z0-9_]*)}}`)

// scanLegacyPromptTokens returns a sorted, deduplicated list of legacy
// ALL_CAPS prompt-body tokens present in data. Empty result means the
// content is clean.
func scanLegacyPromptTokens(data []byte) []string {
	matches := legacyTokenRE.FindAllSubmatch(data, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	for _, m := range matches {
		name := string(m[1])
		if _, ok := legacyPromptTokens[name]; !ok {
			continue
		}
		seen[name] = struct{}{}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// warnLegacyPromptTokens reads the migrated file at fullPath, scans the
// content for legacy ALL_CAPS prompt-body tokens, and appends a Warning
// per match. Quiet for files outside the prompts/ subtree and for clean
// content. Read failures are appended as a single warning — they don't
// abort the migration since the move already succeeded.
//
// Called after a successful structural move; the rewrite is intentionally
// NOT applied automatically because content rewriting and structural
// moves should stay separate concerns. The warning tells the operator
// which tokens to convert by hand.
func warnLegacyPromptTokens(fullPath, relTo string, r *Result) {
	if !isPromptBodyDestination(relTo) {
		return
	}
	data, err := os.ReadFile(fullPath)
	if err != nil {
		r.Warnings = append(r.Warnings,
			fmt.Sprintf("could not scan %s for legacy ALL_CAPS prompt tokens: %v", relTo, err))
		return
	}
	tokens := scanLegacyPromptTokens(data)
	if len(tokens) == 0 {
		return
	}
	hints := make([]string, 0, len(tokens))
	for _, t := range tokens {
		m := legacyPromptTokens[t]
		if m.Literal {
			hints = append(hints, fmt.Sprintf("{{%s}} → %q", t, m.Replacement))
		} else {
			hints = append(hints, fmt.Sprintf("{{%s}} → {{%s}}", t, m.Replacement))
		}
	}
	r.Warnings = append(r.Warnings,
		fmt.Sprintf("%s still references legacy ALL_CAPS prompt tokens that the engine no longer rewrites; replace with dotted form before next run: %s",
			relTo, strings.Join(hints, ", ")))
}

// isPromptBodyDestination reports whether relTo (a migration destination
// path relative to the migration root) is a prompt-body file. Only paths
// under prompts/ qualify — the migrator also moves artifacts into
// shared/ and those are agent outputs, not prompt bodies, so they're not
// scanned.
func isPromptBodyDestination(relTo string) bool {
	return strings.HasPrefix(relTo, "prompts/") || strings.HasPrefix(relTo, "prompts"+string(os.PathSeparator))
}
