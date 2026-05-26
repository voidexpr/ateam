// Package migrate handles in-place upgrades of pre-v1 .ateam / .ateamorg
// layouts to the v1 filename-driven layout described in
// plans/Feature_prompt_report_fs_refactor.md.
//
// The migrator is invoked on first env materialization (see
// internal/root/resolve.go). It is idempotent: a partial run that errored
// mid-way leaves moved files in place; re-running picks up where it left off.
package migrate

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Result summarizes a migration pass. Empty Result with nil error means no
// migration was needed.
//
// v1 migrations are structural only — file moves + history cleanup. Variable
// renames ({{ROLE}} → {{prompt.name}} etc.) are handled at render time by the
// engine's ALL_CAPS compat shim. A separate mechanical pass can run
// assembler.RewriteContent over user prompts in a follow-up; the data type
// here doesn't track rewrites since this migrator doesn't perform them.
type Result struct {
	// Moved is the list of (old → new) renames performed, with paths relative
	// to the migration root.
	Moved []Move
	// RemovedDirs lists directories cleaned up after migration (empty roles/,
	// supervisor/, history/ subtrees).
	RemovedDirs []string
	// Warnings are non-fatal issues (target already exists, etc.). The
	// migration continued past these.
	Warnings []string
}

// Move records a single from→to rename.
type Move struct {
	From string // path relative to migration root, e.g. "supervisor/review_prompt.md"
	To   string // path relative to migration root, e.g. "prompts/review.prompt.md"
}

// Changed reports whether the pass actually changed anything on disk.
func (r Result) Changed() bool {
	return len(r.Moved) > 0 || len(r.RemovedDirs) > 0
}

// NeedsMigration returns true if root contains any pre-v1 layout indicators.
// Cheap check — does not read file contents.
func NeedsMigration(root string) bool {
	indicators := []string{
		"roles",
		"supervisor",
		"report_base_prompt.md",
		"code_base_prompt.md",
		"report_extra_prompt.md",
		"code_extra_prompt.md",
		"setup_overview.md",
	}
	for _, ind := range indicators {
		if _, err := os.Lstat(filepath.Join(root, ind)); err == nil {
			return true
		}
	}
	return false
}

// V1Layout migrates `root` (a .ateam or .ateamorg directory) to the v1 layout.
// Idempotent: re-run picks up where a previous interrupted pass left off.
//
// On the first hard error (e.g. permission denied on a target), the function
// returns immediately with whatever was already done in Result. The error
// message tells the user to re-run after fixing the underlying cause.
func V1Layout(root string) (Result, error) {
	var r Result
	if !NeedsMigration(root) {
		return r, nil
	}
	if err := moveStatic(root, &r); err != nil {
		return r, err
	}
	if err := moveRoles(root, &r); err != nil {
		return r, err
	}
	if err := cleanup(root, &r); err != nil {
		return r, err
	}
	return r, nil
}

// move performs a single rename if `from` exists. Returns:
//   - moved=true if the rename happened
//   - moved=false, nil error if `from` is missing (already migrated or never present)
//   - moved=false, nil error + warning recorded if `to` already exists (skip)
//   - moved=false, non-nil error on filesystem failure
//
// Cross-device renames fall back to copy+delete; same-FS renames are atomic.
func move(root, from, to string, r *Result) (bool, error) {
	src := filepath.Join(root, from)
	dst := filepath.Join(root, to)

	if _, err := os.Lstat(src); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("stat %s: %w", from, err)
	}
	if _, err := os.Lstat(dst); err == nil {
		r.Warnings = append(r.Warnings,
			fmt.Sprintf("skipped move %s → %s: target already exists (manual cleanup needed)", from, to))
		return false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("stat %s: %w", to, err)
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, fmt.Errorf("mkdir %s: %w", filepath.Dir(to), err)
	}

	if err := os.Rename(src, dst); err != nil {
		// EXDEV (cross-device) is the recoverable case — fall back to copy+delete.
		if errors.Is(err, syscall.EXDEV) {
			if err := copyFile(src, dst); err != nil {
				return false, fmt.Errorf("copy %s → %s: %w", from, to, err)
			}
			if err := os.Remove(src); err != nil {
				return false, fmt.Errorf("remove %s after copy: %w", from, err)
			}
		} else {
			return false, fmt.Errorf("rename %s → %s: %w (migration paused; re-run ateam to continue)", from, to, err)
		}
	}
	r.Moved = append(r.Moved, Move{From: from, To: to})
	return true, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// staticMigrations are renames whose source path is fixed (no <role> glob).
var staticMigrations = []Move{
	{From: "report_base_prompt.md", To: "prompts/report/_pre.base.md"},
	{From: "code_base_prompt.md", To: "prompts/code/_pre.base.md"},
	{From: "report_extra_prompt.md", To: "prompts/report/_post.extra.md"},
	{From: "code_extra_prompt.md", To: "prompts/code/_post.extra.md"},

	{From: "supervisor/review_prompt.md", To: "prompts/review.prompt.md"},
	{From: "supervisor/review_extra_prompt.md", To: "prompts/review.post.extra.md"},
	{From: "supervisor/code_management_prompt.md", To: "prompts/code_management.prompt.md"},
	{From: "supervisor/code_management_extra_prompt.md", To: "prompts/code_management.post.extra.md"},
	{From: "supervisor/code_verify_prompt.md", To: "prompts/code_verify.prompt.md"},
	{From: "supervisor/code_verify_extra_prompt.md", To: "prompts/code_verify.post.extra.md"},
	{From: "supervisor/auto_setup_prompt.md", To: "prompts/auto_setup.prompt.md"},
	{From: "supervisor/auto_setup_extra_prompt.md", To: "prompts/auto_setup.post.extra.md"},
	{From: "supervisor/exec_debug_prompt.md", To: "prompts/exec_debug.prompt.md"},
	{From: "supervisor/exec_debug_extra_prompt.md", To: "prompts/exec_debug.post.extra.md"},
	{From: "supervisor/report_commissioning_prompt.md", To: "prompts/report_commissioning.prompt.md"},
	{From: "supervisor/report_commissioning_extra_prompt.md", To: "prompts/report_commissioning.post.extra.md"},
	{From: "supervisor/report_auto_roles_prompt.md", To: "prompts/report_auto_roles.prompt.md"},

	{From: "supervisor/review.md", To: "shared/review/review.md"},
	{From: "supervisor/verify.md", To: "shared/verify/verify.md"},

	{From: "setup_overview.md", To: "shared/auto_setup/auto_setup.md"},
}

// roleMigrations are renames templated over each <R> dir under roles/.
// Source is relative to roles/<R>/; target is relative to root, with <R>
// substituted in.
var roleMigrations = []struct {
	from string // under roles/<R>/
	to   string // under root, with %s = role name
}{
	{from: "report_prompt.md", to: "prompts/report/%s.prompt.md"},
	{from: "code_prompt.md", to: "prompts/code/%s.prompt.md"},
	{from: "report_extra_prompt.md", to: "prompts/report/%s.post.extra.md"},
	{from: "code_extra_prompt.md", to: "prompts/code/%s.post.extra.md"},
	{from: "report.md", to: "shared/report/%s/%s.md"},
}

func moveStatic(root string, r *Result) error {
	for _, m := range staticMigrations {
		if _, err := move(root, m.From, m.To, r); err != nil {
			return err
		}
	}
	return nil
}

func moveRoles(root string, r *Result) error {
	rolesDir := filepath.Join(root, "roles")
	entries, err := os.ReadDir(rolesDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read roles/: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		role := e.Name()
		for _, rm := range roleMigrations {
			from := filepath.Join("roles", role, rm.from)
			var to string
			if strings.Count(rm.to, "%s") == 2 {
				to = fmt.Sprintf(rm.to, role, role)
			} else {
				to = fmt.Sprintf(rm.to, role)
			}
			if _, err := move(root, from, to, r); err != nil {
				return err
			}
		}
	}
	return nil
}

// cleanup removes history/ subdirs (dropped per spec) and any now-empty
// roles/<R>/, roles/, supervisor/ directories. Non-empty parents are left
// alone — unknown files stay where they are.
func cleanup(root string, r *Result) error {
	// 1. roles/<R>/history/ — drop.
	rolesDir := filepath.Join(root, "roles")
	if entries, err := os.ReadDir(rolesDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			hist := filepath.Join(rolesDir, e.Name(), "history")
			if removed, err := removeIfExists(hist); err != nil {
				return err
			} else if removed {
				rel, _ := filepath.Rel(root, hist)
				r.RemovedDirs = append(r.RemovedDirs, rel)
			}
		}
	}
	// 2. supervisor/history/ — drop.
	if removed, err := removeIfExists(filepath.Join(root, "supervisor", "history")); err != nil {
		return err
	} else if removed {
		r.RemovedDirs = append(r.RemovedDirs, "supervisor/history")
	}
	// 3. Empty roles/<R>/, roles/, supervisor/.
	if entries, err := os.ReadDir(rolesDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			roleDir := filepath.Join(rolesDir, e.Name())
			if removed, err := removeIfEmpty(roleDir); err != nil {
				return err
			} else if removed {
				rel, _ := filepath.Rel(root, roleDir)
				r.RemovedDirs = append(r.RemovedDirs, rel)
			}
		}
	}
	for _, dir := range []string{"roles", "supervisor"} {
		full := filepath.Join(root, dir)
		if removed, err := removeIfEmpty(full); err != nil {
			return err
		} else if removed {
			r.RemovedDirs = append(r.RemovedDirs, dir)
		}
	}
	return nil
}

func removeIfExists(dir string) (bool, error) {
	if _, err := os.Lstat(dir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if err := os.RemoveAll(dir); err != nil {
		return false, fmt.Errorf("remove %s: %w", dir, err)
	}
	return true, nil
}

func removeIfEmpty(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if len(entries) > 0 {
		return false, nil
	}
	if err := os.Remove(dir); err != nil {
		return false, fmt.Errorf("rmdir %s: %w", dir, err)
	}
	return true, nil
}
