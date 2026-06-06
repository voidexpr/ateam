package root

import (
	"fmt"
	"os"
	"path/filepath"
)

// cliShimRel is the path of the directory holding the ateam shim
// symlink, relative to a project's ProjectDir (i.e. .ateam/).
const cliShimRel = "cache/bin"

// EnsureCLIShim makes sure <ProjectDir>/cache/bin/ateam is a symlink
// pointing at the currently running ateam binary, so child processes
// that look up `ateam` via PATH find the same binary the parent uses.
//
// Idempotent: the fast path is a single Readlink. The write path only
// runs when the symlink is missing or points at a different target
// (first use, or after an ateam binary upgrade).
//
// Returns "" without error in these cases — sub-agent ateam invocations
// then fall through to the host PATH as before:
//
//   - env has no project (scratch mode, env == nil, etc.)
//   - os.Executable() or EvalSymlinks fail
//   - the filesystem isn't writable for the shim path (permission,
//     ENOSPC, read-only mount, ...)
//
// Filesystem errors print a single-line warning to stderr and do NOT
// propagate — the caller can still launch agents.
func EnsureCLIShim(env *ResolvedEnv) string {
	if env == nil || !env.HasProject() {
		return ""
	}
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ateam shim: os.Executable: %v (continuing without PATH shim)\n", err)
		return ""
	}
	target, err := filepath.EvalSymlinks(exe)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ateam shim: EvalSymlinks(%s): %v (continuing without PATH shim)\n", exe, err)
		return ""
	}
	shimDir := filepath.Join(env.ProjectDir, cliShimRel)
	link := filepath.Join(shimDir, "ateam")

	// Fast path: symlink already points at the right target.
	if existing, err := os.Readlink(link); err == nil && existing == target {
		return shimDir
	}

	if err := os.MkdirAll(shimDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "ateam shim: mkdir %s: %v (continuing without PATH shim)\n", shimDir, err)
		return ""
	}

	// Atomic replace via temp + rename. Rename is atomic on the same
	// filesystem; both paths sit under shimDir so the guarantee holds.
	tmp := link + ".tmp"
	_ = os.Remove(tmp) // best effort: ENOENT is fine
	if err := os.Symlink(target, tmp); err != nil {
		fmt.Fprintf(os.Stderr, "ateam shim: symlink %s -> %s: %v (continuing without PATH shim)\n", tmp, target, err)
		return ""
	}
	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp)
		fmt.Fprintf(os.Stderr, "ateam shim: rename %s -> %s: %v (continuing without PATH shim)\n", tmp, link, err)
		return ""
	}
	return shimDir
}
