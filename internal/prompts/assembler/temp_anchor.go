package assembler

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TempAnchorAssembler wraps an inner Assembler and prepends a single
// "external" anchor rooted at ExternalDir. Used for `ateam exec @PATH`
// and `ateam prompt @PATH` where PATH points at a `.prompt.md` file
// outside every standard anchor: the file's parent dir becomes the
// most-specific anchor so sibling `<basename>.pre.*.md` and dir-level
// `_pre.*.md` fragments next to the file compose alongside the
// inherited framing from Inner.
//
// Inner is the chain to walk after the external anchor — typically
// `env.Assembler()` so the standard project → org → embedded chain
// still applies. Inner must not be nil.
//
// The wrapper synthesizes a fresh MultiAnchorAssembler internally
// (external prepended to Inner.Anchors()) and delegates to it. That
// design keeps Resolve trivial AND lets the underlying chain machinery
// (FirstMatch, AllMatches) handle "external overrides project" with the
// same most-specific-wins semantics that govern every other anchor
// pair.
type TempAnchorAssembler struct {
	Inner       Assembler
	ExternalDir string

	// chained caches the synthesized MultiAnchorAssembler so repeated
	// Resolve / ResolveFramingOnly / Anchors calls within a single
	// run don't re-allocate the anchor list. Built lazily via chain().
	// Inner is treated as immutable after construction — same contract
	// MultiAnchorAssembler's anchors slice has.
	chained *MultiAnchorAssembler
}

// NewTempAnchor builds a TempAnchorAssembler with the supplied external
// directory and inner chain. Both must be non-empty / non-nil.
func NewTempAnchor(externalDir string, inner Assembler) *TempAnchorAssembler {
	return &TempAnchorAssembler{Inner: inner, ExternalDir: externalDir}
}

// IsFilesystemPath reports whether `path` is a filesystem-shape
// .prompt.md reference (ends in ".prompt.md" AND contains a path
// separator or starts with "."). The cmd-layer dispatch
// (`ateam exec @PATH`, `ateam prompt @PATH`) and the PromptFile
// backward-compat shim both decide TempAnchor injection from this
// predicate — keeping it in one place prevents drift.
//
// A bare logical name like "review" is NOT filesystem-shape and
// resolves via the standard anchor walk.
func IsFilesystemPath(path string) bool {
	if !strings.HasSuffix(path, ".prompt.md") {
		return false
	}
	return strings.ContainsRune(path, '/') || strings.HasPrefix(path, ".")
}

// chain returns the cached synthesized MultiAnchorAssembler, building
// it once on first use. Errors when Inner is nil — every method that
// reaches the chain hits the same guard via this helper.
func (t *TempAnchorAssembler) chain() (*MultiAnchorAssembler, error) {
	if t.chained != nil {
		return t.chained, nil
	}
	if t.Inner == nil {
		return nil, errors.New("TempAnchorAssembler: Inner is nil")
	}
	anchors := append([]Anchor{t.externalAnchor()}, t.Inner.Anchors()...)
	t.chained = New(anchors)
	return t.chained, nil
}

// Anchors returns the synthesized chain (external prepended to inner),
// via the cached MultiAnchorAssembler so repeated calls don't
// re-allocate.
func (t *TempAnchorAssembler) Anchors() []Anchor {
	c, err := t.chain()
	if err != nil {
		return nil
	}
	return c.Anchors()
}

// Resolve delegates to the cached synthesized chain. `name` becomes the
// role basename (filename without `.prompt.md`) so the standard assembly
// walks the same way it would for a logical name.
func (t *TempAnchorAssembler) Resolve(name string) ([]ResolvedFile, error) {
	c, err := t.chain()
	if err != nil {
		return nil, err
	}
	role, err := roleFromTempAnchorName(name)
	if err != nil {
		return nil, err
	}
	return c.Resolve(role)
}

// ResolveFramingOnly delegates to the cached synthesized chain —
// fragments from both the external dir AND the inner chain compose,
// never errors on missing main.
func (t *TempAnchorAssembler) ResolveFramingOnly(name string) ([]ResolvedFile, error) {
	c, err := t.chain()
	if err != nil {
		return nil, err
	}
	role, err := roleFromTempAnchorName(name)
	if err != nil {
		return nil, err
	}
	return c.ResolveFramingOnly(role)
}

// FindOrphans scans the external dir for orphan fragments AND delegates
// to Inner. Per implementer note 7: maximize warning surface — drop
// either half and the operator silently loses half the diagnostic.
func (t *TempAnchorAssembler) FindOrphans() ([]*OrphanError, error) {
	if t.Inner == nil {
		return nil, errors.New("TempAnchorAssembler: Inner is nil")
	}
	externalOnly := New([]Anchor{t.externalAnchor()})
	external, err := externalOnly.FindOrphans()
	if err != nil {
		return nil, fmt.Errorf("temp-anchor external scan: %w", err)
	}
	inner, err := t.Inner.FindOrphans()
	if err != nil {
		return nil, fmt.Errorf("temp-anchor inner scan: %w", err)
	}
	return append(external, inner...), nil
}

func (t *TempAnchorAssembler) externalAnchor() Anchor {
	dir := t.ExternalDir
	if dir == "" {
		dir = "."
	}
	return Anchor{Name: "external", FS: os.DirFS(dir)}
}

// roleFromTempAnchorName normalizes the assembler name for the
// synthesized chain. Two shapes:
//
//   - Filesystem-path .prompt.md (e.g. "./foo.prompt.md",
//     "/tmp/foo.prompt.md") — the external anchor is rooted at the
//     file's parent dir, so the chain only needs the basename. Strip
//     dirs AND the suffix.
//   - Logical name (e.g. "review", "report/security") — keep dirs
//     intact; the inner chain walks them as the standard
//     dir-then-role lookup.
//
// Distinguishing rule: filesystem shape requires the `.prompt.md`
// suffix. A bare logical multi-segment name has no suffix and goes
// through verbatim (after trimming surrounding slashes). Without this
// split, a caller passing PromptFile{Path:"report/security"} through a
// TempAnchorAssembler would silently lose the "report/" dir.
func roleFromTempAnchorName(name string) (string, error) {
	if strings.HasSuffix(name, ".prompt.md") {
		role := strings.TrimSuffix(filepath.Base(name), ".prompt.md")
		if role == "" {
			return "", fmt.Errorf("TempAnchorAssembler: empty role basename in %q", name)
		}
		return role, nil
	}
	role := strings.Trim(name, "/")
	if role == "" {
		return "", fmt.Errorf("TempAnchorAssembler: empty role name in %q", name)
	}
	return role, nil
}
