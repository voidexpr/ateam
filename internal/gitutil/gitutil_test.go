package gitutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	// Walk up from the test file's working directory to find the repo root.
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	return dir + "/../.."
}

func TestGetProjectMeta(t *testing.T) {
	meta, err := GetProjectMeta(repoRoot(t))
	if err != nil {
		t.Fatalf("GetProjectMeta: %v", err)
	}

	if meta.CommitHash == "" {
		t.Error("CommitHash is empty")
	}
	if len(meta.CommitHash) != 40 {
		t.Errorf("CommitHash length = %d, want 40", len(meta.CommitHash))
	}
	if matched, _ := regexp.MatchString(`^[0-9a-f]{40}$`, meta.CommitHash); !matched {
		t.Errorf("CommitHash %q is not a valid hex SHA", meta.CommitHash)
	}

	if meta.CommitMessage == "" {
		t.Error("CommitMessage is empty")
	}

	if meta.CommitDate == "" {
		t.Error("CommitDate is empty")
	}
	// Timestamp should be formatted as YYYY-MM-DD_HH-MM-SS
	if matched, _ := regexp.MatchString(`^\d{4}-\d{2}-\d{2}_\d{2}-\d{2}-\d{2}$`, meta.CommitDate); !matched {
		t.Errorf("CommitDate %q does not match expected format YYYY-MM-DD_HH-MM-SS", meta.CommitDate)
	}
}

func TestGetProjectMetaInvalidDir(t *testing.T) {
	_, err := GetProjectMeta("/nonexistent-dir-that-should-not-exist")
	if err == nil {
		t.Error("expected error for invalid directory, got nil")
	}
}

// initTempRepo creates a fresh git repo with one commit in a t.TempDir() and
// returns its path. Skips the test if git is unavailable on the host.
func initTempRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	cmds := [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init"},
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

func TestHeadHashTempRepo(t *testing.T) {
	dir := initTempRepo(t)
	hash := HeadHash(dir)
	if hash == "" {
		t.Fatal("HeadHash returned empty string for valid repo")
	}
	if matched, _ := regexp.MatchString(`^[0-9a-f]{40}$`, hash); !matched {
		t.Errorf("HeadHash %q is not a valid hex SHA", hash)
	}
}

func TestHeadHashNonRepo(t *testing.T) {
	dir := t.TempDir()
	// Subdirectory of a temp dir that itself is not a git repo. HeadHash
	// is documented to return "" in this case rather than erroring.
	if hash := HeadHash(filepath.Join(dir, "missing")); hash != "" {
		t.Errorf("HeadHash on non-repo path returned %q, want empty", hash)
	}
	if hash := HeadHash(dir); hash != "" {
		t.Errorf("HeadHash on non-repo dir returned %q, want empty", hash)
	}
}
