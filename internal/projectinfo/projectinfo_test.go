package projectinfo

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initTempRepo creates a fresh git repo in t.TempDir() with the supplied
// files at the given relative paths, plus one initial commit. Returns the
// resolved repo path. Skips the test if git is unavailable.
func initTempRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	// macOS TempDir resolves through /var → /private/var; git's --show-toplevel
	// returns the resolved path. Pre-resolve so callers can compare paths.
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	for rel, content := range files {
		full := filepath.Join(resolved, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", full, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %q: %v", full, err)
		}
	}
	cmds := [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "-A"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "initial commit"},
	}
	for _, args := range cmds {
		c := exec.Command("git", args...)
		c.Dir = resolved
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return resolved
}

// addCommit makes one additional commit touching `path` with `content`.
func addCommit(t *testing.T, dir, path, content, msg string) {
	t.Helper()
	full := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cmds := [][]string{
		{"add", "-A"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", msg},
	}
	for _, args := range cmds {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func TestCollectBasicGoRepo(t *testing.T) {
	dir := initTempRepo(t, map[string]string{
		"README.md":      "# test\n",
		"go.mod":         "module example.com/test\n\ngo 1.21\n",
		"main.go":        "package main\n",
		"internal/x/x.go": "package x\n",
		"Makefile":       "test:\n\techo ok\n",
	})

	info, err := Collect(dir)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if !info.GitRepo {
		t.Error("GitRepo = false, want true")
	}
	if info.Dir != dir {
		t.Errorf("Dir = %q, want %q", info.Dir, dir)
	}
	if info.WorkingTreeStatus != "clean" {
		t.Errorf("WorkingTreeStatus = %q, want %q", info.WorkingTreeStatus, "clean")
	}
	if info.Branch != "main" {
		t.Errorf("Branch = %q, want %q", info.Branch, "main")
	}
	if len(info.HeadHash) != 40 {
		t.Errorf("HeadHash length = %d, want 40 (%q)", len(info.HeadHash), info.HeadHash)
	}
	if info.HeadSubject != "initial commit" {
		t.Errorf("HeadSubject = %q, want %q", info.HeadSubject, "initial commit")
	}
	if info.TrackedFileCount != 5 {
		t.Errorf("TrackedFileCount = %d, want 5", info.TrackedFileCount)
	}

	wantManifests := []string{"Makefile", "go.mod"}
	if !equalStrings(info.Manifests, wantManifests) {
		t.Errorf("Manifests = %v, want %v", info.Manifests, wantManifests)
	}

	wantDocs := []string{"README.md"}
	if !equalStrings(info.DocsAtRoot, wantDocs) {
		t.Errorf("DocsAtRoot = %v, want %v", info.DocsAtRoot, wantDocs)
	}

	// Top-level: README.md, go.mod, internal, main.go, Makefile. Sort order
	// is the platform's bytewise sort, which puts capitals before lower.
	if got, want := len(info.TopLevelEntries), 5; got != want {
		t.Errorf("TopLevelEntries len = %d, want %d (%v)", got, want, info.TopLevelEntries)
	}

	if len(info.RecentCommits) != 1 {
		t.Errorf("RecentCommits len = %d, want 1", len(info.RecentCommits))
	} else if info.RecentCommits[0].Subject != "initial commit" {
		t.Errorf("RecentCommits[0].Subject = %q", info.RecentCommits[0].Subject)
	}
}

func TestCollectRecentCommitsOrdering(t *testing.T) {
	dir := initTempRepo(t, map[string]string{"README.md": "v0\n"})
	addCommit(t, dir, "README.md", "v1\n", "second")
	addCommit(t, dir, "README.md", "v2\n", "third")

	info, err := Collect(dir)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := len(info.RecentCommits), 3; got != want {
		t.Fatalf("RecentCommits len = %d, want %d", got, want)
	}
	wantSubjects := []string{"third", "second", "initial commit"}
	for i, w := range wantSubjects {
		if info.RecentCommits[i].Subject != w {
			t.Errorf("RecentCommits[%d].Subject = %q, want %q", i, info.RecentCommits[i].Subject, w)
		}
	}
}

func TestCollectHiddenAllowlist(t *testing.T) {
	dir := initTempRepo(t, map[string]string{
		".github/workflows/ci.yml": "name: ci\n",
		".hidden-other/file":       "x",
		"README.md":                "# r\n",
	})

	info, err := Collect(dir)
	if err != nil {
		t.Fatal(err)
	}

	hasGithub := false
	hasOther := false
	for _, e := range info.TopLevelEntries {
		if e == ".github" {
			hasGithub = true
		}
		if e == ".hidden-other" {
			hasOther = true
		}
	}
	if !hasGithub {
		t.Errorf(".github missing from TopLevelEntries: %v", info.TopLevelEntries)
	}
	if hasOther {
		t.Errorf(".hidden-other should be excluded: %v", info.TopLevelEntries)
	}
}

func TestCollectDirtyWorkingTree(t *testing.T) {
	dir := initTempRepo(t, map[string]string{"README.md": "x\n"})
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("modified\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	info, err := Collect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(info.WorkingTreeStatus, "dirty") {
		t.Errorf("WorkingTreeStatus = %q, want prefix %q", info.WorkingTreeStatus, "dirty")
	}
}

func TestCollectNonRepoDirectory(t *testing.T) {
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resolved, "notes.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	info, err := Collect(resolved)
	if err != nil {
		t.Fatalf("Collect on non-repo: %v", err)
	}
	if info.GitRepo {
		t.Error("GitRepo = true on non-repo, want false")
	}
	if info.WorkingTreeStatus != "unknown" {
		t.Errorf("WorkingTreeStatus = %q, want %q", info.WorkingTreeStatus, "unknown")
	}
	if info.HeadHash != "" {
		t.Errorf("HeadHash = %q, want empty on non-repo", info.HeadHash)
	}
	if info.TrackedFileCount != 0 {
		t.Errorf("TrackedFileCount = %d, want 0 on non-repo", info.TrackedFileCount)
	}
	// Top-level / docs / manifests are still populated for non-repos.
	if got := len(info.TopLevelEntries); got == 0 {
		t.Errorf("TopLevelEntries empty on non-repo with files")
	}
}

func TestCollectNonExistentDirectory(t *testing.T) {
	_, err := Collect("/nonexistent-dir-that-should-not-exist-12345")
	if err == nil {
		t.Error("expected error for non-existent dir, got nil")
	}
}

func TestMarkdownNonEmpty(t *testing.T) {
	dir := initTempRepo(t, map[string]string{
		"README.md": "# r\n",
		"go.mod":    "module x\n\ngo 1.21\n",
	})
	info, err := Collect(dir)
	if err != nil {
		t.Fatal(err)
	}
	md := info.Markdown()
	for _, want := range []string{
		"## Quick orientation",
		"Top-level:",
		"Branch: main",
		"Manifests detected:",
		"Docs at root:",
		"Working tree: clean",
		"go.mod",
		"README.md",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("Markdown output missing %q:\n%s", want, md)
		}
	}
}

func TestMarkdownNonRepo(t *testing.T) {
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resolved, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	info, err := Collect(resolved)
	if err != nil {
		t.Fatal(err)
	}
	md := info.Markdown()
	if !strings.Contains(md, "not a git repository") {
		t.Errorf("non-repo Markdown missing the not-a-git-repo notice:\n%s", md)
	}
	if strings.Contains(md, "Recent commits") {
		t.Errorf("non-repo Markdown should not list commits:\n%s", md)
	}
}

func TestJSONRoundtrip(t *testing.T) {
	dir := initTempRepo(t, map[string]string{
		"go.mod":   "module x\n",
		"README.md": "# x\n",
	})
	info, err := Collect(dir)
	if err != nil {
		t.Fatal(err)
	}
	s, err := info.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var round Info
	if err := json.Unmarshal([]byte(s), &round); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if round.Dir != info.Dir {
		t.Errorf("round.Dir = %q, want %q", round.Dir, info.Dir)
	}
	if round.HeadHash != info.HeadHash {
		t.Errorf("HeadHash mismatch after JSON roundtrip")
	}
	if len(round.RecentCommits) != len(info.RecentCommits) {
		t.Errorf("RecentCommits len = %d, want %d", len(round.RecentCommits), len(info.RecentCommits))
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
