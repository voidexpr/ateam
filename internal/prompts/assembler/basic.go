package assembler

import (
	"errors"
	"io/fs"
)

// BasicAssembler returns exactly one ResolvedFile pointing at Path on
// FS, with Slot=role_main. No framing fragments, no anchor walk. Use
// when an operator wants "this one file rendered through the engine but
// no surrounding pre/post" — the cmd layer reaches for this when none
// of the standard composition is desired.
type BasicAssembler struct {
	FS   fs.FS
	Path string
}

// Anchors returns nil — BasicAssembler doesn't walk a chain. Inspection
// callers handle the empty list (the single ResolvedFile carries its
// own FS).
func (b *BasicAssembler) Anchors() []Anchor { return nil }

// Resolve ignores `name` — BasicAssembler is constructed with the file
// it returns. Callers passing a non-empty name silently get the
// configured Path; the spec's "Assembler owns lookup strategy" means
// the strategy here is "always the same file".
func (b *BasicAssembler) Resolve(_ string) ([]ResolvedFile, error) {
	if b.FS == nil {
		return nil, errors.New("BasicAssembler: FS is nil")
	}
	if b.Path == "" {
		return nil, errors.New("BasicAssembler: Path is empty")
	}
	return []ResolvedFile{{
		Slot:   SlotRoleMain,
		Anchor: "basic",
		Path:   b.Path,
		FS:     b.FS,
	}}, nil
}

// ResolveFramingOnly returns nil — BasicAssembler is single-file by
// definition, so there are no framing fragments.
func (b *BasicAssembler) ResolveFramingOnly(_ string) ([]ResolvedFile, error) { return nil, nil }

// FindOrphans returns nil — single-file impl has no fragment universe
// to scan.
func (b *BasicAssembler) FindOrphans() ([]*OrphanError, error) { return nil, nil }
