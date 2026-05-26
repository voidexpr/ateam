package assembler

import (
	"fmt"
	"strings"
)

// DefaultMaxIncludeDepth caps recursive {{include}} / {{include_glob}} expansion.
// Cycles surface as "include depth exceeded" errors.
const DefaultMaxIncludeDepth = 16

// Vars resolves `{{namespace.key}}` template lookups. The interface is shaped
// so the engine can implement the spec's namespace rule: unknown namespace →
// pass through literal; known namespace + unknown key → error.
type Vars interface {
	// Resolve looks up ns.key. Return shape:
	//   (value, true, nil)  — found, substitute value
	//   ("",    true, err)  — known namespace but missing key (or env miss); engine surfaces err
	//   ("",    false, nil) — namespace not recognized; engine emits the directive verbatim
	Resolve(ns, key string) (value string, known bool, err error)
}

// MapVars is a Vars built from per-namespace maps. The closed set of recognized
// namespaces is fixed by this type and matches the spec.
type MapVars struct {
	Prompt    map[string]string
	Exec      map[string]string
	Project   map[string]string
	Git       map[string]string
	Container map[string]string
	Ateam     map[string]string
	Role      map[string]string
	// EnvLookup resolves {{env.NAME}}. If nil, env lookups error (callers that
	// don't want env access should leave it nil so missing envs are loud).
	EnvLookup func(name string) (string, bool)
}

// Resolve implements Vars.
func (v MapVars) Resolve(ns, key string) (string, bool, error) {
	if ns == "env" {
		if v.EnvLookup == nil {
			return "", true, fmt.Errorf("{{env.%s}}: no environment lookup configured", key)
		}
		val, ok := v.EnvLookup(key)
		if !ok {
			return "", true, fmt.Errorf("{{env.%s}}: environment variable not set", key)
		}
		return val, true, nil
	}
	var m map[string]string
	switch ns {
	case "prompt":
		m = v.Prompt
	case "exec":
		m = v.Exec
	case "project":
		m = v.Project
	case "git":
		m = v.Git
	case "container":
		m = v.Container
	case "ateam":
		m = v.Ateam
	case "role":
		m = v.Role
	default:
		return "", false, nil
	}
	val, ok := m[key]
	if !ok {
		return "", true, fmt.Errorf("{{%s.%s}}: unknown key in %s namespace", ns, key, ns)
	}
	return val, true, nil
}

// Engine renders prompt templates: variable substitution plus the include
// directive family. Construct one per assembly run; safe to reuse across
// renders against the same anchor list.
type Engine struct {
	asm      *Assembler
	maxDepth int
}

// NewEngine builds a renderer using a's anchors for include resolution.
// maxDepth=0 picks DefaultMaxIncludeDepth.
func NewEngine(a *Assembler, maxDepth int) *Engine {
	if maxDepth <= 0 {
		maxDepth = DefaultMaxIncludeDepth
	}
	return &Engine{asm: a, maxDepth: maxDepth}
}

// Render expands `{{...}}` directives in content using vars for substitution
// and the engine's assembler for include resolution.
func (e *Engine) Render(content string, vars Vars) (string, error) {
	return e.render(content, vars, 0)
}

func (e *Engine) render(content string, vars Vars, depth int) (string, error) {
	if depth > e.maxDepth {
		return "", fmt.Errorf("include depth exceeded %d (cycle or runaway nesting)", e.maxDepth)
	}
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
			// Unterminated `{{` — keep as literal (defensive; lets agent-emitted text survive).
			out.WriteString(content[i+j:])
			break
		}
		directive := strings.TrimSpace(content[start:closeIdx])
		replacement, err := e.resolve(directive, vars, depth)
		if err != nil {
			return "", err
		}
		out.WriteString(replacement)
		i = closeIdx + 2
	}
	return out.String(), nil
}

// findClosingBrace returns the index of the `}}` that balances the `{{` whose
// content starts at `start`. Counts nested `{{ ... }}` pairs so directives like
// `{{include {{prompt.name}}.prompt.md}}` resolve to the outer brace. Returns
// -1 when unbalanced.
func findClosingBrace(content string, start int) int {
	depth := 1
	i := start
	for i < len(content)-1 {
		switch content[i : i+2] {
		case "{{":
			depth++
			i += 2
		case "}}":
			depth--
			if depth == 0 {
				return i
			}
			i += 2
		default:
			i++
		}
	}
	return -1
}

func (e *Engine) resolve(directive string, vars Vars, depth int) (string, error) {
	if space := strings.IndexAny(directive, " \t"); space > 0 {
		cmd := directive[:space]
		arg := strings.TrimSpace(directive[space+1:])
		switch cmd {
		case "include":
			return e.include(arg, vars, depth, false)
		case "include?":
			return e.include(arg, vars, depth, true)
		case "include_glob":
			return e.includeGlob(arg, vars, depth)
		}
		// Unknown directive — pass through verbatim.
		return "{{" + directive + "}}", nil
	}

	// No-space token. Variable lookup requires a `.`; otherwise pass through
	// (legacy ALL_CAPS that hasn't been migrated, or agent-emitted text).
	dot := strings.IndexByte(directive, '.')
	if dot <= 0 || dot == len(directive)-1 {
		return "{{" + directive + "}}", nil
	}
	ns, key := directive[:dot], directive[dot+1:]
	val, known, err := vars.Resolve(ns, key)
	if err != nil {
		return "", err
	}
	if !known {
		return "{{" + directive + "}}", nil
	}
	return val, nil
}

func (e *Engine) include(arg string, vars Vars, depth int, optional bool) (string, error) {
	if e.asm == nil {
		return "", fmt.Errorf("{{include %s}}: no assembler configured", arg)
	}
	// Two-pass: substitute vars in the path first, then resolve.
	path, err := e.render(arg, vars, depth+1)
	if err != nil {
		return "", err
	}
	m, ok, err := e.asm.FirstMatch(path)
	if err != nil {
		return "", fmt.Errorf("{{include %s}}: %w", path, err)
	}
	if !ok {
		if optional {
			return "", nil
		}
		return "", fmt.Errorf("{{include %s}}: not found in any anchor", path)
	}
	return e.render(string(m.Content), vars, depth+1)
}

func (e *Engine) includeGlob(arg string, vars Vars, depth int) (string, error) {
	if e.asm == nil {
		return "", fmt.Errorf("{{include_glob %s}}: no assembler configured", arg)
	}
	pattern, err := e.render(arg, vars, depth+1)
	if err != nil {
		return "", err
	}
	matches, err := e.asm.AllMatches(pattern)
	if err != nil {
		return "", fmt.Errorf("{{include_glob %s}}: %w", pattern, err)
	}
	if len(matches) == 0 {
		return "", nil
	}
	parts := make([]string, 0, len(matches))
	for _, m := range matches {
		rendered, err := e.render(string(m.Content), vars, depth+1)
		if err != nil {
			return "", fmt.Errorf("{{include_glob %s}}: rendering %s from %s: %w", pattern, m.Path, m.Anchor, err)
		}
		parts = append(parts, rendered)
	}
	return strings.Join(parts, "\n\n"), nil
}
