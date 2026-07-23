package gitutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// testRepo creates a temp git repo with one initial commit and returns
// its path. Skips the test if `git` isn't on PATH.
func testRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	cmds := [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"commit", "--allow-empty", "-q", "-m", "init"},
	}
	for _, args := range cmds {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestDirty_CleanRepo(t *testing.T) {
	repo := testRepo(t)
	if got := Dirty(repo); got != "false" {
		t.Errorf("Dirty(clean): got %q want false", got)
	}
}

func TestDirty_UntrackedFile(t *testing.T) {
	repo := testRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("x"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if got := Dirty(repo); got != "true" {
		t.Errorf("Dirty(untracked): got %q want true", got)
	}
}

func TestDirty_NonRepo(t *testing.T) {
	// A plain tempdir (no `git init`) is reported as clean — same shape
	// as HeadHash returning "" for non-repos.
	if got := Dirty(t.TempDir()); got != "false" {
		t.Errorf("Dirty(non-repo): got %q want false", got)
	}
}

func TestUncommittedTrackedChanges_Clean(t *testing.T) {
	repo := testRepo(t)
	got, err := UncommittedTrackedChanges(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("clean repo: got %d entries, want 0: %v", len(got), got)
	}
}

func TestUncommittedTrackedChanges_UntrackedIgnored(t *testing.T) {
	repo := testRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("x"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := UncommittedTrackedChanges(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("untracked-only: got %d entries, want 0 (untracked is not tracked): %v", len(got), got)
	}
}

func TestUncommittedTrackedChanges_ModifiedTracked(t *testing.T) {
	repo := testRepo(t)
	path := filepath.Join(repo, "tracked.txt")
	if err := os.WriteFile(path, []byte("v1"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	for _, args := range [][]string{{"add", "tracked.txt"}, {"commit", "-q", "-m", "add tracked"}} {
		c := exec.Command("git", args...)
		c.Dir = repo
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(path, []byte("v2"), 0600); err != nil {
		t.Fatalf("WriteFile v2: %v", err)
	}
	got, err := UncommittedTrackedChanges(repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("modified tracked: got %d entries, want 1: %v", len(got), got)
	}
	if want := " M tracked.txt"; got[0] != want {
		t.Errorf("modified tracked line: got %q want %q", got[0], want)
	}
}

func TestHeadShort_NonEmpty(t *testing.T) {
	repo := testRepo(t)
	short := HeadShort(repo)
	if short == "" {
		t.Fatalf("HeadShort returned empty in a repo with a commit")
	}
	if len(short) > 12 {
		t.Errorf("HeadShort %q unexpectedly long; git --short typically yields 7 chars", short)
	}
}

func TestHeadShort_NonRepo(t *testing.T) {
	if got := HeadShort(t.TempDir()); got != "" {
		t.Errorf("HeadShort(non-repo): got %q want empty", got)
	}
}
