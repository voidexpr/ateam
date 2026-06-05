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

// Slot names for ResolvedFile.Slot. These are the same string values the
// composition machinery has always emitted into Section.Slot, exported so
// new Assembler impls can build ResolvedFile values without re-typing
// magic strings. Per-dir slots are constructed as
// `SlotDirPre + ":" + dir` (and similar for post) by Assemble; impls that
// only emit role_main use SlotRoleMain directly.
const (
	SlotRootPre  = "root_pre"
	SlotDirPre   = "dir_pre"
	SlotRoleMain = "role_main"
	SlotRolePost = "role_post"
	SlotDirPost  = "dir_post"
)

// ResolvedFile is one entry in an Assembler.Resolve result. It carries
// what the orchestrator needs to read and the metadata --paths / Inspect
// consume.
type ResolvedFile struct {
	Slot   string // root_pre | dir_pre:<dir> | role_main | role_post | dir_post:<dir>
	Anchor string // project | org | embedded | external | impl-defined
	Path   string // anchor-relative
	FS     fs.FS  // FS to read content from
}

// Assembler resolves a logical name (or filesystem path) into the ordered
// file list that composes the assembled prompt. It owns the lookup
// strategy; it does NOT own rendering — that's the orchestrator's job
// (prompts.PromptFile).
//
// FindOrphans is unchanged from the historical contract — the inspection
// path consumes it directly.
//
// Anchors returns the underlying chain for impls that walk anchors;
// single-file impls return nil.
type Assembler interface {
	Resolve(name string) ([]ResolvedFile, error)
	// ResolveFramingOnly returns the pre/post framing fragments for
	// `name` WITHOUT requiring a `<role>.prompt.md` file to exist. Used
	// by orchestrators when an operator supplies CustomBody (the
	// role_main file is replaced inline and need not exist on disk).
	// Impls without a fragment universe (BasicAssembler) return nil.
	ResolveFramingOnly(name string) ([]ResolvedFile, error)
	FindOrphans() ([]*OrphanError, error)
	Anchors() []Anchor
}

// MultiAnchorAssembler resolves prompt files across an ordered list of
// anchors. It is the chain-walking concrete impl that powers the standard
// project → org → embedded lookup.
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
type MultiAnchorAssembler struct {
	anchors []Anchor
}

// New builds a MultiAnchorAssembler. Anchors must be non-nil and listed
// most-specific first.
func New(anchors []Anchor) *MultiAnchorAssembler {
	dup := make([]Anchor, len(anchors))
	copy(dup, anchors)
	return &MultiAnchorAssembler{anchors: dup}
}

// Anchors returns the anchor list in construction order (most-specific first).
// Useful for tracing / preview.
func (a *MultiAnchorAssembler) Anchors() []Anchor {
	dup := make([]Anchor, len(a.anchors))
	copy(dup, a.anchors)
	return dup
}

// FirstMatch returns the most-specific anchor's version of `path`. ok=false
// when no anchor contains the file. fs.ErrNotExist is the only non-fatal
// error swallowed; other read errors propagate.
func (a *MultiAnchorAssembler) FirstMatch(path string) (Match, bool, error) {
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

// AllMatches returns every file matching any of `patterns` across all anchors,
// deduplicating by path. Each path is emitted once at the slot of the
// most-general anchor that has it; the content comes from the most-specific
// anchor that has it (so an override doesn't move the file's position, only
// replaces its content). Within each anchor's slot, files sort lexically.
//
// Multiple patterns are unioned per anchor before sorting, so callers can pass
// both a singleton and a fragment glob (e.g. "_pre.md" and "_pre.*.md") and
// get them composed together in lexical order.
//
// Empty result on no matches is not an error.
func (a *MultiAnchorAssembler) AllMatches(patterns ...string) ([]Match, error) {
	// First pass: for each path, identify the most-specific anchor that has it
	// (content source).
	contentSrc := make(map[string]int)
	for idx, anc := range a.anchors {
		matches, err := globAny(anc.FS, patterns)
		if err != nil {
			return nil, fmt.Errorf("glob %v in anchor %s: %w", patterns, anc.Name, err)
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
		matches, err := globAny(anc.FS, patterns)
		if err != nil {
			return nil, fmt.Errorf("glob %v in anchor %s: %w", patterns, anc.Name, err)
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

// globAny returns the union of fs.Glob results for every pattern against fsys,
// deduplicating paths a single file may match under more than one pattern.
func globAny(fsys fs.FS, patterns []string) ([]string, error) {
	var out []string
	seen := make(map[string]bool)
	for _, p := range patterns {
		matches, err := fs.Glob(fsys, p)
		if err != nil {
			return nil, err
		}
		for _, m := range matches {
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	return out, nil
}
