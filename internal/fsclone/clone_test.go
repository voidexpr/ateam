package fsclone

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCloneCopiesContent(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.md")
	dst := filepath.Join(dir, "out", "dst.md")

	want := []byte("hello world\n")
	if err := os.WriteFile(src, want, 0600); err != nil {
		t.Fatal(err)
	}

	if err := Clone(src, dst); err != nil {
		t.Fatalf("Clone: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("ReadFile dst: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("Clone produced wrong content: got %q, want %q", got, want)
	}
}

func TestCloneOverwritesExistingDestination(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.md")
	dst := filepath.Join(dir, "dst.md")

	if err := os.WriteFile(src, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("old contents"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := Clone(src, dst); err != nil {
		t.Fatalf("Clone: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("ReadFile dst: %v", err)
	}
	if string(got) != "new" {
		t.Errorf("dst not overwritten: got %q", got)
	}
}

func TestCloneCreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.md")
	dst := filepath.Join(dir, "deep", "nested", "dst.md")

	if err := os.WriteFile(src, []byte("ok"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Clone(src, dst); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Errorf("dst not created in nested path: %v", err)
	}
}
