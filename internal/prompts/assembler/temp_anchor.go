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
}

// NewTempAnchor builds a TempAnchorAssembler with the supplied external
// directory and inner chain. Both must be non-empty / non-nil.
func NewTempAnchor(externalDir string, inner Assembler) *TempAnchorAssembler {
	return &TempAnchorAssembler{Inner: inner, ExternalDir: externalDir}
}

// Anchors returns the synthesized chain (external prepended to inner).
func (t *TempAnchorAssembler) Anchors() []Anchor {
	return append([]Anchor{t.externalAnchor()}, t.Inner.Anchors()...)
}

// Resolve composes via a fresh MultiAnchorAssembler built from the
// synthesized chain. `name` becomes the role basename (filename without
// `.prompt.md`) so the standard assembly walks the same way it would
// for a logical name.
func (t *TempAnchorAssembler) Resolve(name string) ([]ResolvedFile, error) {
	if t.Inner == nil {
		return nil, errors.New("TempAnchorAssembler: Inner is nil")
	}
	role, err := roleFromTempAnchorName(name)
	if err != nil {
		return nil, err
	}
	chained := New(t.Anchors())
	return chained.Resolve(role)
}

// ResolveFramingOnly delegates to the synthesized chain — fragments
// from both the external dir AND the inner chain compose, never errors
// on missing main.
func (t *TempAnchorAssembler) ResolveFramingOnly(name string) ([]ResolvedFile, error) {
	if t.Inner == nil {
		return nil, errors.New("TempAnchorAssembler: Inner is nil")
	}
	role, err := roleFromTempAnchorName(name)
	if err != nil {
		return nil, err
	}
	chained := New(t.Anchors())
	return chained.ResolveFramingOnly(role)
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

// roleFromTempAnchorName accepts either a logical name (no path) or a
// filesystem path ending in `.prompt.md`; returns the role basename so
// the inner walk uses logical-name semantics.
func roleFromTempAnchorName(name string) (string, error) {
	role := strings.TrimSuffix(filepath.Base(name), ".prompt.md")
	if role == "" {
		return "", fmt.Errorf("TempAnchorAssembler: empty role basename in %q", name)
	}
	return role, nil
}
