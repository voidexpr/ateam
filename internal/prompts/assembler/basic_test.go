package assembler

import (
	"testing"
	"testing/fstest"
)

// TestBasicAssembler_ResolveReturnsSingleFile pins the BasicAssembler
// contract: one ResolvedFile with Slot=role_main, no framing.
func TestBasicAssembler_ResolveReturnsSingleFile(t *testing.T) {
	fs := fstest.MapFS{
		"only.md": &fstest.MapFile{Data: []byte("THE BODY")},
	}
	b := &BasicAssembler{FS: fs, Path: "only.md"}
	files, err := b.Resolve("ignored")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %d", len(files))
	}
	f := files[0]
	if f.Slot != SlotRoleMain {
		t.Errorf("Slot=%q want %q", f.Slot, SlotRoleMain)
	}
	if f.Path != "only.md" {
		t.Errorf("Path=%q want only.md", f.Path)
	}
	if f.FS == nil {
		t.Error("FS is nil")
	}
}

func TestBasicAssembler_AnchorsAndOrphansEmpty(t *testing.T) {
	b := &BasicAssembler{FS: fstest.MapFS{}, Path: "x.md"}
	if got := b.Anchors(); len(got) != 0 {
		t.Errorf("Anchors() = %v, want empty", got)
	}
	if got, err := b.FindOrphans(); err != nil || len(got) != 0 {
		t.Errorf("FindOrphans() = %v, %v; want empty, nil", got, err)
	}
	if got, err := b.ResolveFramingOnly("x"); err != nil || len(got) != 0 {
		t.Errorf("ResolveFramingOnly = %v, %v; want empty, nil", got, err)
	}
}

func TestBasicAssembler_ResolveErrorsOnEmptyFields(t *testing.T) {
	if _, err := (&BasicAssembler{Path: "x.md"}).Resolve(""); err == nil {
		t.Error("expected error for nil FS")
	}
	if _, err := (&BasicAssembler{FS: fstest.MapFS{}}).Resolve(""); err == nil {
		t.Error("expected error for empty Path")
	}
}
