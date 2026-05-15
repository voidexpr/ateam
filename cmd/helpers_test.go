package cmd

import (
	"os/exec"
	"testing"
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
