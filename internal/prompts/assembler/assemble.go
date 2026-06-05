package assembler

import (
	"fmt"
	"strings"
)

// Section is one composed unit in the assembled prompt — a single rendered
// fragment with its provenance for tracing/preview output.
type Section struct {
	Anchor  string // anchor name that owns the content (e.g. "project", "embedded")
	Path    string // path within the anchor (e.g. "report/_pre.intro.md")
	Slot    string // assembly slot: "root_pre", "dir_pre:<dir>", "role_pre", "role_main", "role_post", "dir_post:<dir>", "root_post"
	Content string // rendered content (post-template-substitution)
}

// AssembleResult is the full output of Assemble: the joined prompt text plus
// the per-section breakdown the preview command consumes.
type AssembleResult struct {
	Prompt   string
	Sections []Section
}

// AssembleOptions carries caller-supplied overrides that wrap or replace
// parts of the assembled prompt. nil = no overrides (the default).
//
//   - ReplaceRoleMain swaps in this text as the role's main body — all the
//     surrounding framing (root_pre, dir_pre, role_pre, role_post, dir_post,
//     root_post) still composes normally. Used by `ateam review --prompt`,
//     `ateam code --prompt`, and any future "I want everything except the
//     role body" CLI surface. Frontmatter parsing is skipped for the
//     override (the caller passes raw text); template-variable expansion
//     still runs so {{project.info}} etc. resolve.
//   - PrePrompt is wrapped at the very front of the assembled output,
//     before any anchor-discovered content. Used by `--pre-prompt TEXT` on
//     every prompt-taking command. RAW text — no engine expansion.
//   - PostPrompt is wrapped at the very end, after every anchor-discovered
//     section. Used by `--post-prompt TEXT`. RAW text — no engine expansion.
//
// ReplaceRoleMain still flows through the template engine — it stands
// in for a `<role>.prompt.md` body, so its rendering contract matches
// what a file at that path would get. PrePrompt/PostPrompt are
// operator-supplied wrappers — by design they reach the agent
// verbatim, identical across every Prompt impl. A `{{ns.key}}` token
// inside a wrapper surfaces as a literal `{{ns.key}}` (loud failure,
// not silent empty substitution).
//
// Whitespace-only values are dropped — they don't add an empty section to
// the result.
type AssembleOptions struct {
	ReplaceRoleMain string
	PrePrompt       string
	PostPrompt      string
}

// Assemble walks the anchor chain and composes the prompt at `promptPath`
// per the spec's assembly order:
//
//	[root _pre.*.md]
//	  [dir1 _pre.*.md]
//	    [dir2 _pre.*.md] ...
//	      [<role>.pre.*.md]
//	      [<role>.prompt.md]                (first-match across anchors)
//	      [<role>.post.*.md]
//	    [dir2 _post.*.md] ...
//	  [dir1 _post.*.md]
//	[root _post.*.md]
//
// Fragments at each dir-level / role-level slot compose per AllMatches
// semantics (most-general anchor first, lex within each anchor; same path in
// multiple anchors overloads with the most-specific content). Each fragment
// is rendered through `engine` with `vars`; when `engine` is nil a default
// engine bound to this assembler is created.
//
// `promptPath` is the path without `.prompt.md` extension — e.g.
// "report/security" or "review" — and may be `/`-separated for nested dirs.
//
// Errors when no `<role>.prompt.md` is found in any anchor (unless
// opts.ReplaceRoleMain is set — then the missing file is fine, the
// caller's body stands in). Missing dir-level or role-level fragments
// simply contribute nothing (no error).
//
// opts may be nil for the default "no overrides" path. See AssembleOptions
// for the override surfaces.
func (a *MultiAnchorAssembler) Assemble(promptPath string, vars Vars, engine *Engine, opts *AssembleOptions) (AssembleResult, error) {
	if opts == nil {
		opts = &AssembleOptions{}
	}
	if engine == nil {
		engine = NewEngine(a, 0)
	}
	promptPath = strings.TrimSuffix(promptPath, ".prompt.md")
	promptPath = strings.Trim(promptPath, "/")
	if promptPath == "" {
		return AssembleResult{}, fmt.Errorf("empty prompt path")
	}
	parts := strings.Split(promptPath, "/")
	role := parts[len(parts)-1]
	dirs := parts[:len(parts)-1]

	var sections []Section
	addRendered := func(slot, anchor, path, raw string) error {
		_, body, err := ParseFrontmatter(raw)
		if err != nil {
			loc := path
			if anchor != "" {
				loc = anchor + ":" + path
			}
			return fmt.Errorf("frontmatter %s: %w", loc, err)
		}
		rendered, err := engine.Render(body, vars)
		if err != nil {
			loc := path
			if anchor != "" {
				loc = anchor + ":" + path
			}
			return fmt.Errorf("rendering %s: %w", loc, err)
		}
		if strings.TrimSpace(rendered) == "" {
			return nil
		}
		sections = append(sections, Section{
			Anchor:  anchor,
			Path:    path,
			Slot:    slot,
			Content: rendered,
		})
		return nil
	}
	addMatches := func(slot string, matches []Match) error {
		for _, m := range matches {
			if err := addRendered(slot, m.Anchor, m.Path, string(m.Content)); err != nil {
				return err
			}
		}
		return nil
	}
	// addCLI is for caller-supplied overrides (ReplaceRoleMain). Goes
	// through the same render+whitespace-filter path as anchor content so
	// `{{project.info}}` etc. work inside CLI text, and an empty value
	// silently drops.
	addCLI := func(slot, source, body string) error {
		if strings.TrimSpace(body) == "" {
			return nil
		}
		rendered, err := engine.Render(body, vars)
		if err != nil {
			return fmt.Errorf("rendering %s: %w", slot, err)
		}
		if strings.TrimSpace(rendered) == "" {
			return nil
		}
		sections = append(sections, Section{
			Anchor:  "cli",
			Path:    source,
			Slot:    slot,
			Content: rendered,
		})
		return nil
	}

	// addCLIRaw handles PrePrompt / PostPrompt — operator wrappers
	// reach the agent verbatim, no engine pass. Empty / whitespace-only
	// values still drop silently.
	addCLIRaw := func(slot, source, body string) {
		if strings.TrimSpace(body) == "" {
			return
		}
		sections = append(sections, Section{
			Anchor:  "cli",
			Path:    source,
			Slot:    slot,
			Content: body,
		})
	}

	// 0. CLI pre-prompt — outermost wrapper at the front, RAW.
	addCLIRaw("cli_pre_prompt", "(--pre-prompt)", opts.PrePrompt)

	// 1. Dir-level pres, root → leaf.
	for i := 0; i <= len(dirs); i++ {
		dir := strings.Join(dirs[:i], "/")
		matches, err := a.AllMatches(fragmentGlobs(dir, "_pre")...)
		if err != nil {
			return AssembleResult{}, fmt.Errorf("glob dir pre %q: %w", dir, err)
		}
		slot := "root_pre"
		if dir != "" {
			slot = "dir_pre:" + dir
		}
		if err := addMatches(slot, matches); err != nil {
			return AssembleResult{}, err
		}
	}

	dir := strings.Join(dirs, "/")

	// 2. Role-level pres.
	rolePres, err := a.AllMatches(fragmentGlobs(dir, role+".pre")...)
	if err != nil {
		return AssembleResult{}, fmt.Errorf("glob role pre %q: %w", joinName(dir, role), err)
	}
	if err := addMatches("role_pre", rolePres); err != nil {
		return AssembleResult{}, err
	}

	// 3. Role main: either the CLI override (replaces the file) or the
	// first-match across anchors. Required — the assembler errors when no
	// role body is found OR when the resolved body is empty/whitespace.
	// Empty content is treated as a missing role, not a silent skip, so
	// an accidentally-empty project override doesn't ghost the embedded
	// body without surfacing the problem.
	mainCountBefore := len(sections)
	if opts.ReplaceRoleMain != "" {
		if err := addCLI("role_main", "(--prompt)", opts.ReplaceRoleMain); err != nil {
			return AssembleResult{}, err
		}
		if len(sections) == mainCountBefore {
			return AssembleResult{}, fmt.Errorf("role main override (opts.ReplaceRoleMain) is empty after rendering — provide non-whitespace content or omit the override")
		}
	} else {
		mainPath := joinName(dir, role+".prompt.md")
		main, ok, err := a.FirstMatch(mainPath)
		if err != nil {
			return AssembleResult{}, fmt.Errorf("lookup %q: %w", mainPath, err)
		}
		if !ok {
			return AssembleResult{}, fmt.Errorf("no role main at %s in any anchor", mainPath)
		}
		if err := addRendered("role_main", main.Anchor, main.Path, string(main.Content)); err != nil {
			return AssembleResult{}, err
		}
		if len(sections) == mainCountBefore {
			return AssembleResult{}, fmt.Errorf("role main at %s:%s is empty after rendering — the file exists but contains only whitespace, which would silently shadow any embedded fallback; remove the file or add a body", main.Anchor, main.Path)
		}
	}

	// 4. Role-level posts.
	rolePosts, err := a.AllMatches(fragmentGlobs(dir, role+".post")...)
	if err != nil {
		return AssembleResult{}, fmt.Errorf("glob role post %q: %w", joinName(dir, role), err)
	}
	if err := addMatches("role_post", rolePosts); err != nil {
		return AssembleResult{}, err
	}

	// 5. Dir-level posts, leaf → root.
	for i := len(dirs); i >= 0; i-- {
		d := strings.Join(dirs[:i], "/")
		matches, err := a.AllMatches(fragmentGlobs(d, "_post")...)
		if err != nil {
			return AssembleResult{}, fmt.Errorf("glob dir post %q: %w", d, err)
		}
		slot := "root_post"
		if d != "" {
			slot = "dir_post:" + d
		}
		if err := addMatches(slot, matches); err != nil {
			return AssembleResult{}, err
		}
	}

	// 6. CLI post-prompt — outermost wrapper at the end, RAW.
	addCLIRaw("cli_post_prompt", "(--post-prompt)", opts.PostPrompt)

	parts2 := make([]string, len(sections))
	for i, s := range sections {
		parts2[i] = s.Content
	}
	return AssembleResult{
		Prompt:   strings.Join(parts2, "\n\n---\n\n"),
		Sections: sections,
	}, nil
}

// fragmentGlobs returns the singleton + named-fragment glob pair for a
// pre/post base ("_pre", "_post", "<role>.pre", "<role>.post") at `dir`. The
// singleton (`<base>.md`) and fragments (`<base>.<NAME>.md`) both contribute;
// AllMatches unions and lex-sorts them.
func fragmentGlobs(dir, base string) []string {
	return []string{
		joinName(dir, base+".md"),
		joinName(dir, base+".*.md"),
	}
}

// joinName joins a directory and a filename pattern. Empty dir returns just
// the name (root-level pattern).
func joinName(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}
