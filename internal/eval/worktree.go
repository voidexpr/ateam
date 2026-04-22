package eval

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ateam/internal/root"
)

// DefaultWorktreeBase returns the default base directory for eval worktrees
// for a given project. The absolute project path is flattened so different
// projects on the same machine don't clash.
func DefaultWorktreeBase(projectDir string) string {
	abs, err := filepath.Abs(projectDir)
	if err != nil {
		abs = projectDir
	}
	flat := strings.ReplaceAll(strings.TrimPrefix(abs, string(os.PathSeparator)), string(os.PathSeparator), "_")
	return filepath.Join(os.TempDir(), "ateam-worktree", flat)
}

// SetupWorktrees creates two detached git worktrees (base + candidate) under
// baseDir, copies the parent project's .ateam/ into each (minus state/log
// files), and returns ready-to-use envs for each. baseDir is created if
// absent.
//
// The returned envs are derived from source — they carry the same org, config,
// and project identity, with ProjectDir/SourceDir pointed at the worktree.
// This avoids LookupFrom walking up from /tmp/... and failing to discover
// the user's .ateamorg at $HOME.
//
// Validation:
//   - source repo must have no uncommitted changes
//   - baseDir must not be inside the source git repo (would nest repos)
func SetupWorktrees(source *root.ResolvedEnv, baseDir string) (baseEnv, candEnv *root.ResolvedEnv, err error) {
	if source.SourceDir == "" {
		return nil, nil, fmt.Errorf("eval --git-worktree requires a project source directory")
	}

	repoRoot, err := gitRepoRoot(source.SourceDir)
	if err != nil {
		return nil, nil, fmt.Errorf("source dir is not in a git repo: %w", err)
	}
	repoRootReal := realPath(repoRoot)

	if err := ensureCleanWorkTree(repoRoot); err != nil {
		return nil, nil, err
	}

	if baseDir == "" {
		baseDir = DefaultWorktreeBase(source.SourceDir)
	}
	baseDirAbs, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve --git-worktree-base: %w", err)
	}
	if isInside(realPath(baseDirAbs), repoRootReal) {
		return nil, nil, fmt.Errorf("--git-worktree-base %s is inside the source git repo %s — choose a path outside the repo to avoid nested git repos", baseDirAbs, repoRoot)
	}
	if err := os.MkdirAll(baseDirAbs, 0755); err != nil {
		return nil, nil, fmt.Errorf("create worktree base %s: %w", baseDirAbs, err)
	}

	baseWT := filepath.Join(baseDirAbs, "base")
	candWT := filepath.Join(baseDirAbs, "candidate")
	ateamDirName := filepath.Base(source.ProjectDir)

	for _, path := range []string{baseWT, candWT} {
		if err := addDetachedWorktree(repoRoot, path); err != nil {
			return nil, nil, err
		}
		wtAteam := filepath.Join(path, ateamDirName)
		// Start from a clean .ateam/ so checked-out state files (if any) don't
		// carry over alongside our copy.
		if err := os.RemoveAll(wtAteam); err != nil {
			return nil, nil, fmt.Errorf("reset .ateam/ in %s: %w", path, err)
		}
		if err := copyAteamExcludingState(source.ProjectDir, wtAteam); err != nil {
			return nil, nil, fmt.Errorf("copy .ateam/ into %s: %w", path, err)
		}
	}

	return worktreeEnv(source, baseWT), worktreeEnv(source, candWT), nil
}

// worktreeEnv derives a ResolvedEnv for a worktree from the source env, keeping
// OrgDir and Config (identical across worktrees since .ateam/ is a direct copy)
// and pointing ProjectDir/SourceDir at the worktree path. This avoids a second
// org-discovery walk from /tmp/... where .ateamorg would never be found.
func worktreeEnv(source *root.ResolvedEnv, worktreeDir string) *root.ResolvedEnv {
	e := *source
	e.ProjectDir = filepath.Join(worktreeDir, filepath.Base(source.ProjectDir))
	e.SourceDir = worktreeDir
	if source.GitRepoDir != "" {
		e.GitRepoDir = worktreeDir
	}
	return &e
}

// gitRepoRoot returns the top-level of the git repo containing dir.
func gitRepoRoot(dir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git rev-parse: %s", strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// ensureCleanWorkTree errors if `git status --porcelain` reports anything.
func ensureCleanWorkTree(repoRoot string) error {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = repoRoot
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git status: %w", err)
	}
	if strings.TrimSpace(out.String()) != "" {
		return fmt.Errorf("source repo %s has uncommitted changes — commit or stash before running eval --git-worktree", repoRoot)
	}
	return nil
}

// addDetachedWorktree creates a detached worktree at path from the current
// HEAD of repoRoot. If path already exists as a worktree, it is removed first
// (detached worktrees are cheap to recreate and this avoids stale state).
func addDetachedWorktree(repoRoot, path string) error {
	if _, err := os.Stat(path); err == nil {
		rm := exec.Command("git", "worktree", "remove", "--force", path)
		rm.Dir = repoRoot
		rm.Stderr = os.Stderr
		// Ignore error: `git worktree remove` may fail if the dir exists but
		// isn't registered as a worktree; we fall back to os.RemoveAll.
		_ = rm.Run()
		if _, err := os.Stat(path); err == nil {
			if err := os.RemoveAll(path); err != nil {
				return fmt.Errorf("remove stale worktree dir %s: %w", path, err)
			}
		}
	}
	add := exec.Command("git", "worktree", "add", "--detach", path, "HEAD")
	add.Dir = repoRoot
	var stderr bytes.Buffer
	add.Stderr = &stderr
	if err := add.Run(); err != nil {
		return fmt.Errorf("git worktree add %s: %s", path, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// excludedAteamEntry returns true if the path (relative to .ateam/) should be
// skipped when copying the parent's .ateam/ into a worktree. These are state
// and log files whose presence could skew the eval.
func excludedAteamEntry(rel string) bool {
	rel = filepath.ToSlash(rel)
	switch {
	case strings.HasPrefix(rel, "state.sqlite"):
		return true
	case rel == "logs" || strings.HasPrefix(rel, "logs/"):
		return true
	case rel == "eval" || strings.HasPrefix(rel, "eval/"):
		return true
	}
	// roles/<id>/report.md and roles/<id>/history/ — strip per-role state.
	if strings.HasPrefix(rel, "roles/") {
		parts := strings.Split(rel, "/")
		if len(parts) >= 3 {
			last := parts[2]
			if last == "report.md" {
				return true
			}
			if last == "history" {
				return true
			}
			if len(parts) > 3 && parts[2] == "history" {
				return true
			}
		}
	}
	return false
}

// copyAteamExcludingState copies src (.ateam/) into dst, skipping state/log
// files (see excludedAteamEntry).
func copyAteamExcludingState(src, dst string) error {
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if excludedAteamEntry(rel) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// isInside returns true if child is equal to or a subdirectory of parent.
// Both paths should be absolute (and ideally symlink-resolved via realPath).
func isInside(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	return rel == "." || (!strings.HasPrefix(rel, "../") && rel != "..")
}

// realPath resolves symlinks in p. If p doesn't exist, it climbs up to the
// first existing ancestor, resolves that, then re-appends the remaining
// components. Ensures paths under symlinked roots (e.g. /tmp → /private/tmp
// on macOS) compare consistently even when the target path is not yet
// created.
func realPath(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	parent, last := filepath.Split(p)
	parent = strings.TrimRight(parent, string(os.PathSeparator))
	if parent == "" || parent == p {
		return p
	}
	return filepath.Join(realPath(parent), last)
}
