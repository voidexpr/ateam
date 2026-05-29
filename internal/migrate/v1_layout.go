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
//
// renameLegacyReportFiles always runs (even when NeedsMigration is false)
// because projects already on the v1 layout may still hold the pre-Step-6
// `shared/report/<role>/report.md` filename; the rename pass collapses
// them to the spec's `<role>.md`.
func V1Layout(root string) (Result, error) {
	var r Result
	if NeedsMigration(root) {
		if err := moveStatic(root, &r); err != nil {
			return r, err
		}
		if err := moveStaticDirs(root, &r); err != nil {
			return r, err
		}
		if err := moveRoles(root, &r); err != nil {
			return r, err
		}
		if err := cleanup(root, &r); err != nil {
			return r, err
		}
	}
	if err := renameLegacyReportFiles(root, &r); err != nil {
		return r, err
	}
	return r, nil
}

// renameLegacyReportFiles walks shared/report/<R>/ and renames any
// `report.md` to `<R>.md` (the spec filename). Idempotent: a per-role
// `<R>.md` already on disk takes precedence — the older `report.md` is
// removed when its content matches (cleanup) or renamed to
// `report.md.legacy` with a warning when it differs (manual reconciliation).
// Quiet no-op when the shared/report tree doesn't exist yet.
func renameLegacyReportFiles(root string, r *Result) error {
	sharedReportDir := filepath.Join(root, "shared", "report")
	entries, err := os.ReadDir(sharedReportDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read shared/report: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		role := e.Name()
		from := filepath.Join("shared", "report", role, "report.md")
		to := filepath.Join("shared", "report", role, role+".md")
		if _, err := move(root, from, to, r); err != nil {
			return err
		}
	}
	return nil
}

// move performs a single rename if `from` exists. Returns:
//   - moved=true if the rename happened
//   - moved=false, nil error if `from` is missing (already migrated or never present)
//   - moved=false, nil error if `to` already exists (source resolved per
//     resolveExistingTarget — removed when identical, renamed to <src>.legacy
//     when different, left untouched when even the .legacy slot is taken)
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
		return false, resolveExistingTarget(src, dst, from, to, r)
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
	// Base prompts get the post-format slot: they're predominantly output /
	// validation / format rules that the v1 layout puts AFTER the role body.
	// Pre-style framing for report (intro, source location, calibration)
	// lives in defaults' shipped _pre.intro.md and isn't user-overridable
	// from the base prompt.
	{From: "report_base_prompt.md", To: "prompts/report/_post.format.md"},
	{From: "code_base_prompt.md", To: "prompts/code/_post.format.md"},
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

// staticDirMigrations are directory renames (not single files). The migrator
// moves the entire tree atomically with os.Rename — works same-filesystem,
// errors on EXDEV with a "move it manually" message. Target-exists is a
// loud warning (no automatic merge); the user reconciles by hand.
//
// supervisor/code/ holds per-exec sub-run history (code_prompt.md +
// execution_report.md + …). It's the last directory tree under supervisor/
// and moves to shared/code/ to align with the rest of the v1 artifact
// layout.
var staticDirMigrations = []Move{
	{From: "supervisor/code", To: "shared/code"},
}

// roleMigrations are renames templated over each <R> dir under roles/.
// Source is relative to roles/<R>/; target is relative to root, with `{role}`
// substituted in (allows the same role to appear more than once in a path —
// notably the spec's `shared/report/<R>/<R>.md`).
//
// The report.md destination uses the spec's `<role>.md` filename
// (shared/report/<R>/<R>.md) to match what the runner now writes via
// PrimaryOutputName(OutputKindReport, promptName). Projects that ran
// through a pre-Step-6 binary will have files at the older
// `shared/report/<R>/report.md` path — renameReportFiles catches those
// on the next migration invocation.
var roleMigrations = []struct {
	from string // under roles/<R>/
	to   string // under root, with `{role}` placeholders
}{
	{from: "report_prompt.md", to: "prompts/report/{role}.prompt.md"},
	{from: "code_prompt.md", to: "prompts/code/{role}.prompt.md"},
	{from: "report_extra_prompt.md", to: "prompts/report/{role}.post.extra.md"},
	{from: "code_extra_prompt.md", to: "prompts/code/{role}.post.extra.md"},
	{from: "report.md", to: "shared/report/{role}/{role}.md"},
}

func moveStatic(root string, r *Result) error {
	for _, m := range staticMigrations {
		if _, err := move(root, m.From, m.To, r); err != nil {
			return err
		}
	}
	return nil
}

func moveStaticDirs(root string, r *Result) error {
	for _, m := range staticDirMigrations {
		if err := moveDir(root, m.From, m.To, r); err != nil {
			return err
		}
	}
	return nil
}

// moveDir is the directory counterpart to move(). os.Rename on a directory
// moves the whole tree atomically when both paths are on the same
// filesystem; cross-device moves (rare for paths inside one .ateam/) error
// out with a "move it manually" message rather than attempting a recursive
// copy. Target-exists is a loud warning — directories are too expensive to
// recursively diff, so the user reconciles by hand.
func moveDir(root, from, to string, r *Result) error {
	src := filepath.Join(root, from)
	dst := filepath.Join(root, to)

	info, err := os.Lstat(src)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat %s: %w", from, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s exists but is not a directory; expected to migrate a directory tree", from)
	}
	if _, err := os.Lstat(dst); err == nil {
		r.Warnings = append(r.Warnings,
			fmt.Sprintf("skipped dir move %s → %s: target already exists (manual reconciliation needed; rename or merge by hand, then re-run)", from, to))
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", to, err)
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(to), err)
	}
	if err := os.Rename(src, dst); err != nil {
		if errors.Is(err, syscall.EXDEV) {
			return fmt.Errorf("cross-device dir move %s → %s not supported; move it manually with `mv %s %s` then re-run ateam", from, to, src, dst)
		}
		return fmt.Errorf("rename %s → %s: %w (migration paused; re-run ateam to continue)", from, to, err)
	}
	r.Moved = append(r.Moved, Move{From: from, To: to})
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
			to := strings.ReplaceAll(rm.to, "{role}", role)
			if _, err := move(root, from, to, r); err != nil {
				return err
			}
		}
	}
	return nil
}

// junkLeftoverFiles are well-known runtime artifacts that lingered under the
// old layout and are not migrated anywhere — overwritten on every run, no
// value once the layout flips. Removing them lets the otherwise-empty
// roles/<R>/ and supervisor/ dirs get cleaned up by the empty-dir pass.
//
//   - roles/<R>/last_run_output.md / last_run_error.md: per-role stderr
//     forensics from before the v1 logs/<exec_id>/ tree took over.
//   - supervisor/code_output.md / code_verification_report.md: per-run
//     supervisor summaries replaced by .ateam/runtime/<exec_id>/ artifacts.
var junkPerRole = []string{"last_run_output.md", "last_run_error.md"}
var junkSupervisor = []string{"code_output.md", "code_verification_report.md"}

// cleanup removes history/ subdirs (dropped per spec), drops the well-known
// per-role and supervisor junk files listed above, then removes any now-empty
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
			// roles/<R>/last_run_*.md — drop.
			for _, f := range junkPerRole {
				p := filepath.Join(rolesDir, e.Name(), f)
				if removed, err := removeFileIfExists(p); err != nil {
					return err
				} else if removed {
					rel, _ := filepath.Rel(root, p)
					r.RemovedDirs = append(r.RemovedDirs, rel)
				}
			}
		}
	}
	// 2. supervisor/history/ — drop.
	if removed, err := removeIfExists(filepath.Join(root, "supervisor", "history")); err != nil {
		return err
	} else if removed {
		r.RemovedDirs = append(r.RemovedDirs, "supervisor/history")
	}
	// 2b. supervisor/code_output.md, code_verification_report.md — drop.
	for _, f := range junkSupervisor {
		p := filepath.Join(root, "supervisor", f)
		if removed, err := removeFileIfExists(p); err != nil {
			return err
		} else if removed {
			r.RemovedDirs = append(r.RemovedDirs, "supervisor/"+f)
		}
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

// resolveExistingTarget handles the case where `dst` already exists during a
// move. Three branches:
//   - identical content → remove the legacy source (cleanup; no warning, the
//     migration is effectively complete)
//   - different content → rename source to <src>.legacy and warn; user can
//     inspect both files
//   - <src>.legacy also occupied → record the original "manual cleanup" warning
//     and leave both files in place
//
// Returns nil on success (any branch); only filesystem errors propagate.
func resolveExistingTarget(src, dst, from, to string, r *Result) error {
	srcData, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read %s: %w", from, err)
	}
	dstData, err := os.ReadFile(dst)
	if err != nil {
		return fmt.Errorf("read %s: %w", to, err)
	}
	if string(srcData) == string(dstData) {
		if err := os.Remove(src); err != nil {
			return fmt.Errorf("remove duplicate %s: %w", from, err)
		}
		return nil
	}
	legacy := src + ".legacy"
	if _, err := os.Lstat(legacy); err == nil {
		r.Warnings = append(r.Warnings,
			fmt.Sprintf("skipped move %s → %s: target exists with different content AND %s.legacy is taken (manual cleanup needed)", from, to, from))
		return nil
	}
	if err := os.Rename(src, legacy); err != nil {
		return fmt.Errorf("rename %s → %s.legacy: %w", from, from, err)
	}
	r.Warnings = append(r.Warnings,
		fmt.Sprintf("kept %s.legacy: target %s already exists with different content; inspect both and remove the .legacy file when satisfied", from, to))
	return nil
}

// removeFileIfExists deletes a single file (not a directory). Returns
// (true, nil) when the file existed and was removed, (false, nil) when it
// was absent, or (false, err) on permission / unexpected errors.
func removeFileIfExists(path string) (bool, error) {
	if _, err := os.Lstat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if err := os.Remove(path); err != nil {
		return false, fmt.Errorf("remove %s: %w", path, err)
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
