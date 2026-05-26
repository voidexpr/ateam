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
// Errors when no `<role>.prompt.md` is found in any anchor. Missing dir-level
// or role-level fragments simply contribute nothing (no error).
func (a *Assembler) Assemble(promptPath string, vars Vars, engine *Engine) (AssembleResult, error) {
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
		rendered, err := engine.Render(raw, vars)
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

	// 1. Dir-level pres, root → leaf.
	for i := 0; i <= len(dirs); i++ {
		dir := strings.Join(dirs[:i], "/")
		pattern := joinName(dir, "_pre.*.md")
		matches, err := a.AllMatches(pattern)
		if err != nil {
			return AssembleResult{}, fmt.Errorf("glob %q: %w", pattern, err)
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
	rolePrePattern := joinName(dir, role+".pre.*.md")
	rolePres, err := a.AllMatches(rolePrePattern)
	if err != nil {
		return AssembleResult{}, fmt.Errorf("glob %q: %w", rolePrePattern, err)
	}
	if err := addMatches("role_pre", rolePres); err != nil {
		return AssembleResult{}, err
	}

	// 3. Role main (first-match wins).
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

	// 4. Role-level posts.
	rolePostPattern := joinName(dir, role+".post.*.md")
	rolePosts, err := a.AllMatches(rolePostPattern)
	if err != nil {
		return AssembleResult{}, fmt.Errorf("glob %q: %w", rolePostPattern, err)
	}
	if err := addMatches("role_post", rolePosts); err != nil {
		return AssembleResult{}, err
	}

	// 5. Dir-level posts, leaf → root.
	for i := len(dirs); i >= 0; i-- {
		d := strings.Join(dirs[:i], "/")
		pattern := joinName(d, "_post.*.md")
		matches, err := a.AllMatches(pattern)
		if err != nil {
			return AssembleResult{}, fmt.Errorf("glob %q: %w", pattern, err)
		}
		slot := "root_post"
		if d != "" {
			slot = "dir_post:" + d
		}
		if err := addMatches(slot, matches); err != nil {
			return AssembleResult{}, err
		}
	}

	parts2 := make([]string, len(sections))
	for i, s := range sections {
		parts2[i] = s.Content
	}
	return AssembleResult{
		Prompt:   strings.Join(parts2, "\n\n"),
		Sections: sections,
	}, nil
}

// joinName joins a directory and a filename pattern. Empty dir returns just
// the name (root-level pattern).
func joinName(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}
