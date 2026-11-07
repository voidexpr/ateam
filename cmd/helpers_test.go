package cmd

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runtime"
)

// initTestGitRepo runs `git init` + an empty commit in dir so the action
// commands' post-resolveEnv `requireGitRepo` check passes. Tests that don't
// care about git contents can call this once after creating their project
// fixture. Skips silently when git is unavailable.
func initTestGitRepo(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git CLI required")
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
		}
	}
}

// testHome creates an isolated user ateam directory under parent, points
// ATEAM_HOME at it, and returns it. Projects live anywhere; the home layer
// is what --home / ATEAM_HOME selects.
func testHome(t *testing.T, parent string) string {
	t.Helper()
	home := filepath.Join(parent, ".ateam")
	if err := root.EnsureHome(home); err != nil {
		t.Fatalf("EnsureHome: %v", err)
	}
	t.Setenv(runtime.HomeEnv, home)
	return home
}

// isolateHome points ATEAM_HOME at an empty temp dir so a fixture that builds
// its own .ateam/ by hand never resolves against the developer's real home.
func isolateHome(t *testing.T) {
	t.Helper()
	t.Setenv(runtime.HomeEnv, filepath.Join(t.TempDir(), ".ateam"))
}
