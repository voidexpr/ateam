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

// TestTempAnchorAssembler_LogicalMultiSegmentName pins that a logical
// multi-segment name (no .prompt.md suffix) passes through with its
// directory intact. Without this, a caller wrapping a logical name in
// a TempAnchorAssembler would silently lose the dir component and the
// inner chain would walk for the bare role at every anchor root.
func TestTempAnchorAssembler_LogicalMultiSegmentName(t *testing.T) {
	innerFS := fstest.MapFS{
		"prompts/report/security.prompt.md": &fstest.MapFile{Data: []byte("REPORT-SEC BODY")},
		"prompts/security.prompt.md":        &fstest.MapFile{Data: []byte("ROOT-SEC BODY (wrong)")},
	}
	inner := New(BuildAnchors("", "", innerFS))

	extDir := t.TempDir()
	ta := NewTempAnchor(extDir, inner)
	files, err := ta.Resolve("report/security")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	var mainPath string
	for _, f := range files {
		if f.Slot == SlotRoleMain {
			mainPath = f.Path
		}
	}
	if mainPath != "report/security.prompt.md" {
		t.Errorf("role_main resolved to %q; expected report/security.prompt.md (dir component must NOT be dropped)", mainPath)
	}
}

// TestIsFilesystemPath pins the predicate's closed truth-table — the
// canonical home for dispatch sites that decide TempAnchor injection
// from path shape (cmd/exec_bundle.go::buildArgPrompt, cmd/prompt.go::
// runPromptLiteralFile, PromptFile's backward-compat shim). Divergence
// between dispatch sites would route some paths through the wrong
// branch, so the rule lives once here.
func TestIsFilesystemPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"./foo.prompt.md", true},
		{"../foo.prompt.md", true},
		{".prompt.md", true},
		{"dir/foo.prompt.md", true},
		{"/abs/path/foo.prompt.md", true},
		{"sub/dir/foo.prompt.md", true},
		{"foo.prompt.md", false},
		{"review", false},
		{"./foo.md", false},
		{"./foo", false},
		{"./foo.prompt", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			if got := IsFilesystemPath(tc.path); got != tc.want {
				t.Errorf("IsFilesystemPath(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
