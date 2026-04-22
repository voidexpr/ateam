package eval

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/root"
)

// initGitRepo creates a new git repo at dir with one committed file. Returns
// the absolute path to the repo root.
func initGitRepo(t *testing.T, dir string) string {
	t.Helper()
	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	runGit("add", ".")
	runGit("commit", "-q", "-m", "init")
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestDefaultWorktreeBase(t *testing.T) {
	got := DefaultWorktreeBase("/path/to/project/.ateam")
	wantSuffix := "ateam-worktree" + string(os.PathSeparator) + "path_to_project_.ateam"
	if !strings.HasSuffix(got, wantSuffix) {
		t.Errorf("DefaultWorktreeBase = %q, want suffix %q", got, wantSuffix)
	}
}

// newTestEnv builds a ResolvedEnv rooted at a fresh git repo with a minimal
// .ateam/ project directory containing some state files to test the exclusion
// logic.
func newTestEnv(t *testing.T) (*root.ResolvedEnv, string) {
	t.Helper()
	repoDir := t.TempDir()
	initGitRepo(t, repoDir)

	projectDir := filepath.Join(repoDir, ".ateam")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeFile(t, filepath.Join(projectDir, "config.toml"), "[project]\nname = \"test\"\n")
	writeFile(t, filepath.Join(projectDir, "state.sqlite"), "binary-stuff")
	writeFile(t, filepath.Join(projectDir, "logs", "runner.log"), "log contents")
	writeFile(t, filepath.Join(projectDir, "roles", "security", "report_prompt.md"), "prompt content")
	writeFile(t, filepath.Join(projectDir, "roles", "security", "report.md"), "previous report")
	writeFile(t, filepath.Join(projectDir, "roles", "security", "history", "2026-01-01.report.md"), "history entry")
	writeFile(t, filepath.Join(projectDir, "eval", "old.md"), "old eval")

	// Commit the .ateam/ dir so the worktree has a clean state.
	cmd := exec.Command("git", "add", ".")
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	cmd = exec.Command("git", "commit", "-q", "-m", "add ateam")
	cmd.Dir = repoDir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	env := &root.ResolvedEnv{
		ProjectDir: projectDir,
		SourceDir:  repoDir,
	}
	return env, repoDir
}

func TestSetupWorktreesSuccess(t *testing.T) {
	env, _ := newTestEnv(t)
	base := filepath.Join(t.TempDir(), "worktrees")

	baseEnv, candEnv, err := SetupWorktrees(env, base)
	if err != nil {
		t.Fatalf("SetupWorktrees: %v", err)
	}
	defer cleanupWorktrees(t, env.SourceDir, baseEnv.SourceDir, candEnv.SourceDir)

	if baseEnv.OrgDir != env.OrgDir || candEnv.OrgDir != env.OrgDir {
		t.Errorf("worktree envs should inherit OrgDir from source: base=%q cand=%q want=%q",
			baseEnv.OrgDir, candEnv.OrgDir, env.OrgDir)
	}

	for _, wt := range []string{baseEnv.SourceDir, candEnv.SourceDir} {
		if _, err := os.Stat(filepath.Join(wt, "README.md")); err != nil {
			t.Errorf("worktree %s missing README.md: %v", wt, err)
		}
		// .ateam/ should be present via the worktree checkout AND also via our copy.
		// Since we copy into <wt>/.ateam, config should be there.
		ateam := filepath.Join(wt, ".ateam")
		if _, err := os.Stat(filepath.Join(ateam, "config.toml")); err != nil {
			t.Errorf("%s: config.toml not copied: %v", wt, err)
		}
		if _, err := os.Stat(filepath.Join(ateam, "roles", "security", "report_prompt.md")); err != nil {
			t.Errorf("%s: role prompt not copied: %v", wt, err)
		}
		// These should NOT be copied.
		if _, err := os.Stat(filepath.Join(ateam, "state.sqlite")); !os.IsNotExist(err) {
			t.Errorf("%s: state.sqlite should not be present (err=%v)", wt, err)
		}
		if _, err := os.Stat(filepath.Join(ateam, "logs")); !os.IsNotExist(err) {
			t.Errorf("%s: logs/ should not be present (err=%v)", wt, err)
		}
		if _, err := os.Stat(filepath.Join(ateam, "roles", "security", "report.md")); !os.IsNotExist(err) {
			t.Errorf("%s: roles/security/report.md should not be present (err=%v)", wt, err)
		}
		if _, err := os.Stat(filepath.Join(ateam, "roles", "security", "history")); !os.IsNotExist(err) {
			t.Errorf("%s: roles/security/history should not be present (err=%v)", wt, err)
		}
		if _, err := os.Stat(filepath.Join(ateam, "eval")); !os.IsNotExist(err) {
			t.Errorf("%s: eval/ should not be present (err=%v)", wt, err)
		}
	}
}

func TestSetupWorktreesRejectsDirtyTree(t *testing.T) {
	env, repoRoot := newTestEnv(t)
	// Make the repo dirty.
	if err := os.WriteFile(filepath.Join(repoRoot, "README.md"), []byte("modified"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, _, err := SetupWorktrees(env, filepath.Join(t.TempDir(), "worktrees"))
	if err == nil {
		t.Fatal("expected error for dirty tree, got nil")
	}
	if !strings.Contains(err.Error(), "uncommitted changes") {
		t.Errorf("error should mention uncommitted changes, got: %v", err)
	}
}

func TestSetupWorktreesRejectsBaseInsideRepo(t *testing.T) {
	env, repoRoot := newTestEnv(t)
	// Place --git-worktree-base inside the source repo.
	insideBase := filepath.Join(repoRoot, "subdir", "evalworktrees")

	_, _, err := SetupWorktrees(env, insideBase)
	if err == nil {
		t.Fatal("expected error when base dir is inside source repo, got nil")
	}
	if !strings.Contains(err.Error(), "inside the source git repo") {
		t.Errorf("error should mention nesting, got: %v", err)
	}
}

func TestExcludedAteamEntry(t *testing.T) {
	cases := []struct {
		rel  string
		want bool
	}{
		{"config.toml", false},
		{"state.sqlite", true},
		{"state.sqlite-wal", true},
		{"state.sqlite-shm", true},
		{"logs", true},
		{"logs/foo.log", true},
		{"eval", true},
		{"eval/something", true},
		{"roles/security/report_prompt.md", false},
		{"roles/security/report.md", true},
		{"roles/security/history", true},
		{"roles/security/history/2026-01-01.md", true},
		{"roles", false},
		{"roles/security", false},
	}
	for _, c := range cases {
		if got := excludedAteamEntry(c.rel); got != c.want {
			t.Errorf("excludedAteamEntry(%q) = %v, want %v", c.rel, got, c.want)
		}
	}
}

// cleanupWorktrees removes any worktrees created during a test so the temp
// repo can be cleaned up by t.TempDir(). Ignores errors.
func cleanupWorktrees(t *testing.T, repoRoot string, paths ...string) {
	t.Helper()
	for _, p := range paths {
		cmd := exec.Command("git", "worktree", "remove", "--force", p)
		cmd.Dir = repoRoot
		_ = cmd.Run()
	}
}
