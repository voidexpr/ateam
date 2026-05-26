package assembler

import (
	"errors"
	"fmt"
	"io/fs"
	"sort"
)

// Anchor is one search root in the prompt-resolution chain. Anchors are
// listed most-specific first (project → org → ... → embedded). Each FS is
// rooted at the prompts directory: paths inside it look like "report/security.prompt.md",
// "_pre.context.md", etc.
type Anchor struct {
	Name string // human-readable tag for tracing — "project", "org", "embedded", ...
	FS   fs.FS
}

// Match is a file found in some anchor.
type Match struct {
	Anchor  string // Anchor.Name that owns this match's content
	Path    string // path within the anchor's FS, e.g. "report/security.prompt.md"
	Content []byte
}

// Assembler resolves prompt files across an ordered list of anchors.
//
// Two resolution modes:
//   - FirstMatch: most-specific anchor wins (used for `<role>.prompt.md` and
//     for {{include}} / {{include?}}).
//   - AllMatches: every distinct path contributes; if the same path exists in
//     multiple anchors, the most-specific anchor's content wins for that slot
//     (used for `_pre.*.md`, `_post.*.md`, and {{include_glob}}).
//
// Across-anchor ordering for AllMatches is most-general-first (embedded →
// org → project) with lexical order within each anchor — matching the spec's
// "embedded → org → project across anchors" rule. A file whose path appears
// in multiple anchors occupies the slot of the most-specific anchor that has
// it.
type Assembler struct {
	anchors []Anchor
}

// New builds an Assembler. Anchors must be non-nil and listed most-specific first.
func New(anchors []Anchor) *Assembler {
	dup := make([]Anchor, len(anchors))
	copy(dup, anchors)
	return &Assembler{anchors: dup}
}

// Anchors returns the anchor list in construction order (most-specific first).
// Useful for tracing / preview.
func (a *Assembler) Anchors() []Anchor {
	dup := make([]Anchor, len(a.anchors))
	copy(dup, a.anchors)
	return dup
}

// FirstMatch returns the most-specific anchor's version of `path`. ok=false
// when no anchor contains the file. fs.ErrNotExist is the only non-fatal
// error swallowed; other read errors propagate.
func (a *Assembler) FirstMatch(path string) (Match, bool, error) {
	for _, anc := range a.anchors {
		data, err := fs.ReadFile(anc.FS, path)
		if err == nil {
			return Match{Anchor: anc.Name, Path: path, Content: data}, true, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return Match{}, false, fmt.Errorf("read %s in anchor %s: %w", path, anc.Name, err)
		}
	}
	return Match{}, false, nil
}

// AllMatches returns every file matching `pattern` across all anchors,
// deduplicating by path. Each path is emitted once at the slot of the
// most-general anchor that has it; the content comes from the most-specific
// anchor that has it (so an override doesn't move the file's position, only
// replaces its content). Within each anchor's slot, files sort lexically.
//
// Empty result on no matches is not an error.
func (a *Assembler) AllMatches(pattern string) ([]Match, error) {
	// First pass: for each path, identify the most-specific anchor that has it
	// (content source).
	contentSrc := make(map[string]int)
	for idx, anc := range a.anchors {
		matches, err := fs.Glob(anc.FS, pattern)
		if err != nil {
			return nil, fmt.Errorf("glob %q in anchor %s: %w", pattern, anc.Name, err)
		}
		for _, m := range matches {
			if _, seen := contentSrc[m]; !seen {
				contentSrc[m] = idx
			}
		}
	}

	// Second pass: walk most-general first; emit each path at the first slot
	// where it appears, content taken from its most-specific anchor.
	emitted := make(map[string]bool)
	var out []Match
	for idx := len(a.anchors) - 1; idx >= 0; idx-- {
		anc := a.anchors[idx]
		matches, err := fs.Glob(anc.FS, pattern)
		if err != nil {
			return nil, fmt.Errorf("glob %q in anchor %s: %w", pattern, anc.Name, err)
		}
		sort.Strings(matches)
		for _, m := range matches {
			if emitted[m] {
				continue
			}
			srcAnc := a.anchors[contentSrc[m]]
			data, err := fs.ReadFile(srcAnc.FS, m)
			if err != nil {
				return nil, fmt.Errorf("read %s in anchor %s: %w", m, srcAnc.Name, err)
			}
			out = append(out, Match{Anchor: srcAnc.Name, Path: m, Content: data})
			emitted[m] = true
		}
	}
	return out, nil
}
