// Package gitutil provides utility functions for querying git repository metadata such as commit history and project state.
package gitutil

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const timestampFormat = "2006-01-02_15-04-05"

type ProjectMeta struct {
	CommitHash    string
	CommitDate    string
	CommitMessage string
	Uncommitted   []string
}

func GetProjectMeta(dir string) (*ProjectMeta, error) {
	logCmd := exec.Command("git", "log", "-1", "--format=%H%n%aI%n%s")
	logCmd.Dir = dir
	logOut, err := logCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git log failed: %w", err)
	}

	lines := strings.SplitN(strings.TrimSpace(string(logOut)), "\n", 3)
	if len(lines) < 3 {
		return nil, fmt.Errorf("unexpected git log output")
	}

	commitDate := lines[1]
	if t, err := time.Parse(time.RFC3339, lines[1]); err == nil {
		commitDate = t.Format(timestampFormat)
	}

	meta := &ProjectMeta{
		CommitHash:    lines[0],
		CommitDate:    commitDate,
		CommitMessage: lines[2],
	}

	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = dir
	statusOut, err := statusCmd.Output()
	if err != nil {
		return meta, nil
	}

	for _, l := range strings.Split(strings.TrimSpace(string(statusOut)), "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			meta.Uncommitted = append(meta.Uncommitted, l)
		}
	}

	return meta, nil
}

// HeadHash returns the current HEAD commit hash for the repo containing dir.
// Returns "" if git is unavailable, dir is not in a repo, or the call fails.
func HeadHash(dir string) string {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// CurrentBranch returns the branch name at HEAD for the repo containing dir.
// Returns "" if git is unavailable, dir is not in a repo, the call fails, or
// HEAD is detached (rev-parse's literal "HEAD" is normalized to "").
func CurrentBranch(dir string) string {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	b := strings.TrimSpace(string(out))
	if b == "HEAD" {
		return ""
	}
	return b
}

// HeadShort returns the abbreviated HEAD commit hash (typically 7 chars
// per git's default abbreviation length). Returns "" if git is unavailable,
// dir is not in a repo, or the call fails. Distinct from
// `HeadHash(dir)[:7]` because git's --short respects core.abbrev, which a
// user or workflow may have tuned.
func HeadShort(dir string) string {
	cmd := exec.Command("git", "rev-parse", "--short", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// UncommittedTrackedChanges returns the porcelain status lines for tracked
// files with uncommitted modifications, ignoring untracked files. Returns a
// nil slice when the tree is clean. Callers should ensure dir is inside a
// git repo (e.g. via requireGitRepo / TopLevel) — this function surfaces
// the underlying git error if the command fails so the caller can decide
// whether that's fatal.
func UncommittedTrackedChanges(dir string) ([]string, error) {
	cmd := exec.Command("git", "status", "--porcelain", "--untracked-files=no")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git status failed: %w", err)
	}
	// Preserve the porcelain two-column status prefix — column 1 is the index
	// status, column 2 is the worktree status, so " M foo" (unstaged mod) and
	// "M  foo" (staged mod) mean different things and TrimSpace would erase
	// the distinction. Only trim trailing whitespace / drop empty lines.
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if l = strings.TrimRight(l, "\r"); l != "" {
			lines = append(lines, l)
		}
	}
	return lines, nil
}

// Dirty reports whether the working tree has uncommitted changes.
// "false" when the tree is clean OR when dir is not in a repo OR when
// git is unavailable — callers checking against "true" therefore err on
// the side of treating non-repos as clean, which matches how
// gitutil.HeadHash / CurrentBranch return empty on the same edge.
//
// Returns the literal strings "true" / "false" so the value can land
// directly in a {{git.dirty}} template substitution without further
// stringification.
func Dirty(dir string) string {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "false"
	}
	if strings.TrimSpace(string(out)) == "" {
		return "false"
	}
	return "true"
}

// TopLevel returns the absolute path of the git repo containing dir.
// Returns "" if git CLI is missing, dir is not in a repo, or the call fails.
// For a git worktree, this returns the worktree's own root (worktrees are
// first-class git roots that share infrastructure via `--git-common-dir`).
func TopLevel(dir string) string {
	if dir == "" {
		return ""
	}
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
