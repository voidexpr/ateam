package assembler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// TestTempAnchorAssembler_ResolveExternalDir verifies the temp-anchor
// path: a `.prompt.md` file in ExternalDir surfaces as role_main with
// Anchor=external, while inherited dir-level fragments from Inner still
// compose. Pins the "external prepended to inner chain" semantics the
// `ateam exec @PATH.prompt.md` dispatch relies on.
func TestTempAnchorAssembler_ResolveExternalDir(t *testing.T) {
	extDir := t.TempDir()
	mustWrite(t, filepath.Join(extDir, "widget.prompt.md"), "WIDGET BODY")
	mustWrite(t, filepath.Join(extDir, "widget.pre.intro.md"), "WIDGET INTRO")
	mustWrite(t, filepath.Join(extDir, "widget.post.outro.md"), "WIDGET OUTRO")

	innerFS := fstest.MapFS{
		"prompts/_pre.shared.md": &fstest.MapFile{Data: []byte("FROM CHAIN")},
	}
	inner := New(BuildAnchors("", "", innerFS))

	ta := NewTempAnchor(extDir, inner)
	files, err := ta.Resolve("widget.prompt.md")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	var foundMain, foundChainPre, foundExtPre, foundExtPost bool
	var roleMainAnchor string
	for _, f := range files {
		switch {
		case f.Slot == SlotRoleMain:
			foundMain = true
			roleMainAnchor = f.Anchor
		case f.Slot == "role_pre" && strings.Contains(f.Path, "widget.pre.intro"):
			foundExtPre = true
		case f.Slot == "role_post" && strings.Contains(f.Path, "widget.post.outro"):
			foundExtPost = true
		case f.Slot == SlotRootPre && strings.Contains(f.Path, "_pre.shared"):
			foundChainPre = true
		}
	}
	if !foundMain || roleMainAnchor != "external" {
		t.Errorf("role_main missing or wrong anchor: %v", files)
	}
	if !foundExtPre {
		t.Errorf("widget.pre.intro.md from external missing: %v", files)
	}
	if !foundExtPost {
		t.Errorf("widget.post.outro.md from external missing: %v", files)
	}
	if !foundChainPre {
		t.Errorf("inherited inner _pre.shared.md missing: %v", files)
	}
}

// TestTempAnchorAssembler_ResolveFramingOnly checks that the
// framing-only path delegates correctly and never errors on a missing
// role main file.
func TestTempAnchorAssembler_ResolveFramingOnly(t *testing.T) {
	extDir := t.TempDir()
	mustWrite(t, filepath.Join(extDir, "widget.pre.only.md"), "PRE ONLY")
	// No widget.prompt.md — that's the point.

	inner := New(BuildAnchors("", "", fstest.MapFS{}))
	ta := NewTempAnchor(extDir, inner)
	files, err := ta.ResolveFramingOnly("widget.prompt.md")
	if err != nil {
		t.Fatalf("ResolveFramingOnly: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected at least one framing file, got none")
	}
}

// TestTempAnchorAssembler_FindOrphansScansBothSides pins implementer
// note 7: FindOrphans on TempAnchor returns external orphans AND
// inner orphans — dropping either half silently loses half the
// warning surface.
func TestTempAnchorAssembler_FindOrphansScansBothSides(t *testing.T) {
	extDir := t.TempDir()
	// `widget` has no .prompt.md anywhere → its .pre.md is an orphan.
	mustWrite(t, filepath.Join(extDir, "widget.pre.scope.md"), "EXT ORPHAN")

	// Inner anchor with its own orphan (`other.pre.foo.md` with no
	// matching `other.prompt.md`).
	innerFS := fstest.MapFS{
		"prompts/other.pre.foo.md": &fstest.MapFile{Data: []byte("INNER ORPHAN")},
	}
	inner := New(BuildAnchors("", "", innerFS))

	ta := NewTempAnchor(extDir, inner)
	orphans, err := ta.FindOrphans()
	if err != nil {
		t.Fatalf("FindOrphans: %v", err)
	}
	var sawExternal, sawInner bool
	for _, o := range orphans {
		if o.Role == "widget" {
			sawExternal = true
		}
		if o.Role == "other" {
			sawInner = true
		}
	}
	if !sawExternal {
		t.Errorf("expected external orphan for `widget`, got: %v", orphans)
	}
	if !sawInner {
		t.Errorf("expected inner orphan for `other`, got: %v", orphans)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
