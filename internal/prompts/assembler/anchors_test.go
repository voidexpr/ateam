package assembler

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestBuildAnchorsAllPresent(t *testing.T) {
	tmp := t.TempDir()
	projectDir := filepath.Join(tmp, "proj")
	orgDir := filepath.Join(tmp, "org")
	for _, d := range []string{
		filepath.Join(projectDir, "prompts"),
		filepath.Join(orgDir, "prompts"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(projectDir, "prompts", "x.md"), []byte("P"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orgDir, "prompts", "y.md"), []byte("O"), 0o644); err != nil {
		t.Fatal(err)
	}
	embedded := fstest.MapFS{
		"prompts/z.md": &fstest.MapFile{Data: []byte("E")},
	}

	anchors := BuildAnchors(projectDir, orgDir, embedded)
	if len(anchors) != 3 {
		t.Fatalf("len = %d, want 3", len(anchors))
	}
	if anchors[0].Name != "project" || anchors[1].Name != "org" || anchors[2].Name != "embedded" {
		t.Fatalf("anchor order: %s, %s, %s", anchors[0].Name, anchors[1].Name, anchors[2].Name)
	}

	a := New(anchors)
	if m, ok, _ := a.FirstMatch("x.md"); !ok || string(m.Content) != "P" {
		t.Errorf("project lookup failed: ok=%v content=%q", ok, m.Content)
	}
	if m, ok, _ := a.FirstMatch("y.md"); !ok || string(m.Content) != "O" {
		t.Errorf("org lookup failed: ok=%v content=%q", ok, m.Content)
	}
	if m, ok, _ := a.FirstMatch("z.md"); !ok || string(m.Content) != "E" {
		t.Errorf("embedded lookup failed: ok=%v content=%q", ok, m.Content)
	}
}

func TestBuildAnchorsMissingPromptsSubdirsAreOK(t *testing.T) {
	// projectDir and orgDir exist, but neither has a prompts/ subdir.
	// BuildAnchors should still return anchors; reads just return not-found.
	tmp := t.TempDir()
	projectDir := filepath.Join(tmp, "proj")
	orgDir := filepath.Join(tmp, "org")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(orgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	anchors := BuildAnchors(projectDir, orgDir, nil)
	if len(anchors) != 2 {
		t.Fatalf("len = %d, want 2", len(anchors))
	}
	a := New(anchors)
	_, ok, err := a.FirstMatch("anything.md")
	if err != nil {
		t.Fatalf("missing-subdir read should not error, got %v", err)
	}
	if ok {
		t.Fatal("expected ok=false")
	}
}

func TestBuildAnchorsOmitsEmptyOrg(t *testing.T) {
	tmp := t.TempDir()
	projectDir := filepath.Join(tmp, "proj")
	os.MkdirAll(projectDir, 0o755)
	anchors := BuildAnchors(projectDir, "", nil)
	if len(anchors) != 1 || anchors[0].Name != "project" {
		t.Fatalf("got %+v, want one project anchor", anchors)
	}
}

func TestBuildAnchorsAllEmpty(t *testing.T) {
	anchors := BuildAnchors("", "", nil)
	if len(anchors) != 0 {
		t.Fatalf("expected zero anchors, got %d", len(anchors))
	}
}

func TestBuildAnchorsEmbeddedOnly(t *testing.T) {
	embedded := fstest.MapFS{
		"prompts/_pre.context.md": &fstest.MapFile{Data: []byte("ctx")},
	}
	anchors := BuildAnchors("", "", embedded)
	if len(anchors) != 1 || anchors[0].Name != "embedded" {
		t.Fatalf("got %+v, want embedded-only", anchors)
	}
	a := New(anchors)
	if m, ok, _ := a.FirstMatch("_pre.context.md"); !ok || string(m.Content) != "ctx" {
		t.Errorf("embedded-only lookup failed: ok=%v content=%q", ok, m.Content)
	}
}
