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

	// Enumerate returns a snapshot of every namespaced key/value
	// currently visible. The shape is ns → key → value; namespaces
	// with no keys are omitted. Implementations clone internal state
	// so callers can't mutate the returned maps.
	//
	// The env.* namespace is intentionally excluded — it's
	// callback-based with no closed set, so enumeration would be
	// either misleading (a partial answer) or expensive (every
	// possible env var). Consumers that need env values use Resolve.
	//
	// Used for diagnostics (--paths metadata, verify error
	// suggestions naming sibling keys in the same namespace) and as
	// the spec's `CreateVars` foundation for future script-Prompt
	// impls that need to enumerate every available value.
	Enumerate() map[string]map[string]string
}

// MapVars is a Vars built from per-namespace maps. The closed set of recognized
// namespaces is fixed by this type and matches the spec.
//
// args.* and roles.* are the factory-curated namespaces: the verb's
// CLI surface exposes specific values into the prompt without leaking
// the whole option struct. `args.batch`, `roles.enabled`, etc. are
// rendered the same way as the env-derived namespaces.
type MapVars struct {
	Prompt    map[string]string
	Exec      map[string]string
	Project   map[string]string
	Git       map[string]string
	Container map[string]string
	Ateam     map[string]string
	Role      map[string]string
	Args      map[string]string
	Roles     map[string]string
	// EnvLookup resolves {{env.NAME}}. If nil, env lookups error (callers that
	// don't want env access should leave it nil so missing envs are loud).
	EnvLookup func(name string) (string, bool)
}

// Enumerate implements Vars. Returns a clone of every populated
// per-namespace map; the env.* namespace is excluded (lookup-only).
func (v MapVars) Enumerate() map[string]map[string]string {
	out := map[string]map[string]string{}
	add := func(name string, m map[string]string) {
		if len(m) == 0 {
			return
		}
		clone := make(map[string]string, len(m))
		for k, val := range m {
			clone[k] = val
		}
		out[name] = clone
	}
	add("prompt", v.Prompt)
	add("exec", v.Exec)
	add("project", v.Project)
	add("git", v.Git)
	add("container", v.Container)
	add("ateam", v.Ateam)
	add("role", v.Role)
	add("args", v.Args)
	add("roles", v.Roles)
	return out
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
	case "args":
		m = v.Args
	case "roles":
		m = v.Roles
	default:
		return "", false, nil
	}
	val, ok := m[key]
	if !ok {
		return "", true, fmt.Errorf("{{%s.%s}}: unknown key in %s namespace", ns, key, ns)
	}
	return val, true, nil
}

// Dispatcher resolves `{{dynamic.NAME args...}}` directives. The engine
// expands variables inside the arg list, tokenizes the result with shell-like
// quoting rules, then calls Dispatch. The prompts package supplies a
// concrete adapter that wraps a PromptDynamic map plus a ResolveContext —
// keeping the engine free of any reference to dynamics-side types.
//
// When the engine has no dispatcher configured, `{{dynamic.NAME ...}}`
// passes through verbatim (same shape as any other unknown namespace) so
// callers that don't supply one keep their existing behavior.
type Dispatcher interface {
	Dispatch(name string, args []string) (string, error)
}

// Engine renders prompt templates: variable substitution plus the include
// directive family. Construct one per assembly run; safe to reuse across
// renders against the same anchor list.
type Engine struct {
	asm        *Assembler
	maxDepth   int
	dispatcher Dispatcher
}

// NewEngine builds a renderer using a's anchors for include resolution.
// maxDepth=0 picks DefaultMaxIncludeDepth.
func NewEngine(a *Assembler, maxDepth int) *Engine {
	if maxDepth <= 0 {
		maxDepth = DefaultMaxIncludeDepth
	}
	return &Engine{asm: a, maxDepth: maxDepth}
}

// WithDispatcher attaches a dynamic-function dispatcher and returns the
// engine for chaining. Passing nil clears the dispatcher.
func (e *Engine) WithDispatcher(d Dispatcher) *Engine {
	e.dispatcher = d
	return e
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
	head, rest := splitHead(directive)

	switch head {
	case "include":
		return e.include(rest, vars, depth, false)
	case "include?":
		return e.include(rest, vars, depth, true)
	case "include_glob":
		return e.includeGlob(rest, vars, depth)
	}

	// Variable substitution / dynamic dispatch — both require dotted form.
	dot := strings.IndexByte(head, '.')
	if dot <= 0 || dot == len(head)-1 {
		// No dot: the engine no longer recognizes legacy ALL_CAPS
		// (varmap.go is gone). Tokens pass through verbatim so the
		// runner-side substitution layer (runner/template.go's
		// ResolveTemplateString) can still fill {{BATCH}}, {{EXEC_ID}},
		// etc. at execution time. Authoring tools and runtime.hcl
		// validation surface the migration error separately.
		return "{{" + directive + "}}", nil
	}
	ns := head[:dot]
	key := head[dot+1:]

	if ns == "dynamic" {
		return e.dynamic(key, rest, vars, depth)
	}

	// `{{ns.key ? default}}` — when rest begins with `?`, treat what
	// follows as the default to render when the resolved value is empty.
	// Anything else after `ns.key` is an unknown shape that passes through.
	var defaultText string
	haveDefault := false
	if rest != "" {
		if strings.HasPrefix(rest, "?") {
			defaultText = strings.TrimLeft(rest[1:], " \t")
			haveDefault = true
		} else {
			return "{{" + directive + "}}", nil
		}
	}
	val, known, err := vars.Resolve(ns, key)
	if err != nil {
		return "", err
	}
	if !known {
		return "{{" + directive + "}}", nil
	}
	if haveDefault && val == "" {
		return e.render(defaultText, vars, depth+1)
	}
	return val, nil
}

// splitHead splits a directive's content into the leading token and the
// rest. The leading token runs to the first ASCII whitespace; rest is the
// remainder with one leading run of whitespace consumed.
func splitHead(directive string) (head, rest string) {
	space := strings.IndexAny(directive, " \t\n")
	if space < 0 {
		return directive, ""
	}
	return directive[:space], strings.TrimLeft(directive[space+1:], " \t\n")
}

func (e *Engine) include(arg string, vars Vars, depth int, optional bool) (string, error) {
	if e.asm == nil {
		return "", fmt.Errorf("{{include %s}}: no assembler configured", arg)
	}
	// `{{include path ? TEXT}}` — split off the fallback before any
	// variable expansion so a literal `?` in an expanded path can't be
	// mistaken for a separator.
	pathArg, fallback, hasFallback := splitFallback(arg)

	// Two-pass: substitute vars in the path first, then resolve.
	expanded, err := e.render(pathArg, vars, depth+1)
	if err != nil {
		return "", err
	}
	// Spec: tokenize after var expansion. Includes take exactly one arg —
	// authors quote paths that contain whitespace.
	args, err := tokenize(expanded)
	if err != nil {
		return "", fmt.Errorf("{{include %s}}: %w", arg, err)
	}
	if len(args) == 0 {
		return "", fmt.Errorf("{{include}}: empty path")
	}
	if len(args) > 1 {
		return "", fmt.Errorf("{{include %s}}: expected one path arg, got %d (quote paths containing whitespace)", arg, len(args))
	}
	path := args[0]
	m, ok, err := e.asm.FirstMatch(path)
	if err != nil {
		return "", fmt.Errorf("{{include %s}}: %w", path, err)
	}
	if !ok {
		if hasFallback {
			return e.render(fallback, vars, depth+1)
		}
		if optional {
			return "", nil
		}
		return "", fmt.Errorf("{{include %s}}: not found in any anchor", path)
	}
	return e.render(string(m.Content), vars, depth+1)
}

// dynamic dispatches `{{dynamic.NAME args...}}`. With no dispatcher set,
// the directive passes through verbatim so callers that don't wire dynamics
// keep their behavior.
func (e *Engine) dynamic(name, rest string, vars Vars, depth int) (string, error) {
	if e.dispatcher == nil {
		if rest == "" {
			return "{{dynamic." + name + "}}", nil
		}
		return "{{dynamic." + name + " " + rest + "}}", nil
	}
	expanded, err := e.render(rest, vars, depth+1)
	if err != nil {
		return "", err
	}
	args, err := tokenize(expanded)
	if err != nil {
		return "", fmt.Errorf("{{dynamic.%s ...}}: %w", name, err)
	}
	return e.dispatcher.Dispatch(name, args)
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

// splitFallback splits "head ? tail" on the first ` ? ` separator that
// appears at the top level — outside balanced `{{...}}` and outside quoted
// strings. Returns (s, "", false) when no top-level separator is present.
func splitFallback(s string) (head, tail string, ok bool) {
	var inSingle, inDouble bool
	braceDepth := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !inSingle && !inDouble && i+1 < len(s) {
			if c == '{' && s[i+1] == '{' {
				braceDepth++
				i++
				continue
			}
			if c == '}' && s[i+1] == '}' && braceDepth > 0 {
				braceDepth--
				i++
				continue
			}
		}
		if braceDepth > 0 {
			continue
		}
		if !inDouble && c == '\'' {
			inSingle = !inSingle
			continue
		}
		if !inSingle && c == '"' {
			inDouble = !inDouble
			continue
		}
		if inSingle || inDouble {
			continue
		}
		if c == '?' && i > 0 && isASCIISpace(s[i-1]) && i+1 < len(s) && isASCIISpace(s[i+1]) {
			return strings.TrimRight(s[:i-1], " \t\n"), strings.TrimLeft(s[i+2:], " \t\n"), true
		}
	}
	return s, "", false
}

func isASCIISpace(b byte) bool {
	switch b {
	case ' ', '\t', '\n':
		return true
	}
	return false
}

// tokenize splits s into whitespace-separated tokens with shell-like quoting:
//   - whitespace splits tokens outside quotes
//   - single or double quotes preserve internal whitespace
//   - inside a quoted run, `\\`, `\"`, `\'` are escape sequences; other
//     backslashes are emitted literally
//   - outside quotes, backslashes have no special meaning
//
// Returns an error on an unterminated quoted run.
func tokenize(s string) ([]string, error) {
	var args []string
	var cur strings.Builder
	inQuote := byte(0)
	inToken := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inQuote != 0 {
			if c == '\\' && i+1 < len(s) {
				next := s[i+1]
				if next == '\\' || next == inQuote {
					cur.WriteByte(next)
					i++
					continue
				}
			}
			if c == inQuote {
				inQuote = 0
				continue
			}
			cur.WriteByte(c)
			continue
		}
		if c == ' ' || c == '\t' || c == '\n' {
			if inToken {
				args = append(args, cur.String())
				cur.Reset()
				inToken = false
			}
			continue
		}
		if c == '"' || c == '\'' {
			inQuote = c
			inToken = true
			continue
		}
		cur.WriteByte(c)
		inToken = true
	}
	if inQuote != 0 {
		return nil, fmt.Errorf("unterminated quote (%c)", inQuote)
	}
	if inToken {
		args = append(args, cur.String())
	}
	return args, nil
}
