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
