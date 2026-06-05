package runtime

import (
	"fmt"
	"os"
	"regexp"
	"sort"
)

// knownRunnerTemplates is the closed set of `{{ALL_CAPS}}` tokens the
// runner's TemplateVars replacer fills at exec time (see
// internal/runner/template.go::TemplateVars.Replacer). Anything outside
// this set in a user-authored runtime.hcl is almost certainly a typo —
// the prompt lifecycle refactor (Step 6) removed the engine-side
// ALL_CAPS compat shim, so an unrecognized {{FOO}} now just lands in the
// rendered output literally instead of being mapped to a dotted form.
//
// Kept as a string set (not a Go-level reference to runner.TemplateVars)
// to dodge the runtime → runner import that would otherwise pull the
// runner machinery into config-loading.
var knownRunnerTemplates = map[string]struct{}{
	"PROJECT_NAME":                     {},
	"PROJECT_FULL_PATH":                {},
	"PROJECT_DIR":                      {},
	"ROLE":                             {},
	"ACTION":                           {},
	"BATCH":                            {},
	"TIMESTAMP":                        {},
	"PROFILE":                          {},
	"EXEC_ID":                          {},
	"AGENT":                            {},
	"MODEL":                            {},
	"EFFORT":                           {},
	"MAX_BUDGET_USD":                   {},
	"MAX_BUDGET_USD_BATCH":             {},
	"SUBRUN_ARGS":                      {},
	"CONTAINER_TYPE":                   {},
	"CONTAINER_NAME":                   {},
	"OUTPUT_DIR":                       {},
	"OUTPUT_FILE":                      {},
	"PROMPT_FILE":                      {},
	"EXECUTION_DIR":                    {},
	"ATEAM_OWN_README":                 {},
	"ATEAM_OWN_COMMANDS":               {},
	"ATEAM_OWN_CONFIG":                 {},
	"ATEAM_OWN_ISOLATION":              {},
	"ATEAM_OWN_ROLES":                  {},
	"AUTO_ROLES_MARKER":                {},
	"ATEAM_AUTO_ROLES_COMMANDS_OUTPUT": {},
	// runner.go::buildRequest still understands these as Docker-exec
	// templating; surfaces explicitly so they don't false-alarm.
	"CONTAINER": {},
	"CMD":       {},
}

var allCapsTokenRE = regexp.MustCompile(`{{([A-Z][A-Z0-9_]*)}}`)

// detectStrayAllCaps scans data for `{{ALL_CAPS}}` tokens that aren't in
// the known runner template set. Returns a sorted, deduplicated list of
// the offending names (without braces). An empty result means the file is
// clean.
//
// Heuristic: only matches the bare `{{NAME}}` shape — directives with
// args (`{{include foo}}`) and dotted forms (`{{prompt.name}}`) are
// excluded by the regex.
func detectStrayAllCaps(data []byte) []string {
	matches := allCapsTokenRE.FindAllSubmatch(data, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	for _, m := range matches {
		name := string(m[1])
		if _, ok := knownRunnerTemplates[name]; ok {
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

// warnStrayAllCaps emits a stderr line per stray ALL_CAPS token found in
// a user-author HCL source. Called from mergeHCLFile so embedded defaults
// and org-level defaults stay quiet — only the layers an operator
// directly edits trip the warning.
func warnStrayAllCaps(filename string, data []byte) {
	stray := detectStrayAllCaps(data)
	if len(stray) == 0 {
		return
	}
	for _, name := range stray {
		fmt.Fprintf(os.Stderr,
			"warning: %s references unknown ALL_CAPS template {{%s}}. The engine no longer rewrites ALL_CAPS — confirm this token is intentional or switch to the dotted form (e.g. {{prompt.name}}).\n",
			filename, name)
	}
}
