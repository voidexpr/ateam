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

// NeedsMigration returns true if root contains any layout indicators that
// the static-migration pass knows how to rewrite (legacy v0 entries plus
// the v1 dir-level code framing files relocated to top-level singletons in
// the post-`code.prompt.md` reshuffle). Cheap check — does not read file
// contents.
func NeedsMigration(root string) bool {
	indicators := []string{
		// Pre-v1 layout (v0 → v1 first pass).
		"roles",
		"supervisor",
		"report_base_prompt.md",
		"code_base_prompt.md",
		"report_extra_prompt.md",
		"code_extra_prompt.md",
		"setup_overview.md",
		// v1 → v1+: code framing moved from a dir-level fragment to a
		// top-level singleton. Detect either user-installed override.
		"prompts/code/_post.format.md",
		"prompts/code/_post.extra.md",
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
// flattenSharedLayout always runs (even when NeedsMigration is false)
// because projects already on a pre-flat v1 layout still hold artifacts at
// shared/<action>/<action>.md (and the older shared/report/<role>/report.md
// transitional filename); the flatten pass collapses them to
// shared/report/<role>.md and shared/<action>.md.
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
	if err := flattenSharedLayout(root, &r); err != nil {
		return r, err
	}
	return r, nil
}

// flattenSharedLayout collapses the pre-flat v1 nested artifact layout to
// the flat layout:
//   - shared/report/<R>/<R>.md  → shared/report/<R>.md
//   - shared/report/<R>/report.md (pre-Step-6 transitional) → shared/report/<R>.md
//   - shared/review/review.md   → shared/review.md
//   - shared/verify/verify.md   → shared/verify.md
//   - shared/auto_setup/auto_setup.md → shared/auto_setup.md
//
// Idempotent: a flat file already on disk takes precedence; the nested
// source is removed when its content matches (cleanup) or renamed to
// `<src>.legacy` with a warning when it differs (manual reconciliation).
// Empty per-role/per-action dirs are removed after their file is hoisted.
// Quiet no-op when none of the source paths exist.
func flattenSharedLayout(root string, r *Result) error {
	if err := flattenSharedReports(root, r); err != nil {
		return err
	}
	for _, action := range []string{"review", "verify", "auto_setup"} {
		from := filepath.Join("shared", action, action+".md")
		to := filepath.Join("shared", action+".md")
		moved, err := move(root, from, to, r)
		if err != nil {
			return err
		}
		if moved {
			recordRemoveIfEmpty(root, filepath.Join("shared", action), r)
		}
	}
	return nil
}

// flattenSharedReports walks shared/report/<R>/ and hoists either
// `<R>.md` or the older `report.md` to shared/report/<R>.md, then removes
// the now-empty <R>/ dir. Both nested filenames may coexist; if so, the
// spec name (<R>.md) wins and report.md is reconciled by move()'s
// resolveExistingTarget against the destination.
func flattenSharedReports(root string, r *Result) error {
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
		dst := filepath.Join("shared", "report", role+".md")
		// Spec filename first so report.md (older) reconciles against an
		// already-promoted <R>.md rather than the other way around.
		for _, srcName := range []string{role + ".md", "report.md"} {
			src := filepath.Join("shared", "report", role, srcName)
			if _, err := move(root, src, dst, r); err != nil {
				return err
			}
		}
		recordRemoveIfEmpty(root, filepath.Join("shared", "report", role), r)
	}
	return nil
}

// recordRemoveIfEmpty calls removeIfEmpty(fullPath) and, on a successful
// rmdir, records the relative path in r.RemovedDirs. Non-rmdir errors are
// downgraded to warnings — the migration shouldn't abort because of
// leftover housekeeping after a successful hoist.
func recordRemoveIfEmpty(root, rel string, r *Result) {
	full := filepath.Join(root, rel)
	removed, err := removeIfEmpty(full)
	if err != nil {
		r.Warnings = append(r.Warnings, fmt.Sprintf("could not remove empty dir %s: %v", rel, err))
		return
	}
	if removed {
		r.RemovedDirs = append(r.RemovedDirs, rel)
	}
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
	// A no-op rename (from == to) can arise when a path templated over a role
	// id collapses the source and destination onto the same file — e.g. a role
	// literally named "report" makes renameLegacyReportFiles compute
	// shared/report/report/report.md for both. Without this guard the
	// dst-exists branch below would treat the file as its own duplicate and
	// delete it. Nothing to do here.
	if from == to {
		return false, nil
	}

	src := filepath.Join(root, from)
	dst := filepath.Join(root, to)

	if _, err := os.Lstat(src); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("stat %s: %w", from, err)
	}
	if info, err := os.Lstat(dst); err == nil {
		// A directory sitting at the file target can't be reconciled by the
		// content-compare path (resolveExistingTarget would os.ReadFile a
		// directory and abort the whole migration with a confusing EISDIR).
		// Warn and skip like moveDir does for an occupied directory target.
		if info.IsDir() {
			r.Warnings = append(r.Warnings,
				fmt.Sprintf("skipped move %s → %s: target exists and is a directory (manual reconciliation needed; rename or merge by hand, then re-run)", from, to))
			return false, nil
		}
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
	{From: "code_base_prompt.md", To: "prompts/code.prompt.md"},
	{From: "report_extra_prompt.md", To: "prompts/report/_post.extra.md"},
	{From: "code_extra_prompt.md", To: "prompts/code.post.extra.md"},
	// v1 → v1+: code's dir-level framing moved to a top-level singleton so
	// `ateam prompt --action code` can address it without a role. The
	// matching dir-level role bodies (code/refactor_small.prompt.md etc.)
	// no longer ship — user-installed ones become orphans the assembler
	// will warn about, but they stay on disk for manual reconciliation.
	{From: "prompts/code/_post.format.md", To: "prompts/code.prompt.md"},
	{From: "prompts/code/_post.extra.md", To: "prompts/code.post.extra.md"},

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

	// v1 flat shared artifacts: single-file outputs land directly under
	// shared/ (no per-action subdir). The pre-flat shared/<action>/<action>.md
	// layout is collapsed by flattenSharedLayout on every migration pass.
	{From: "supervisor/review.md", To: "shared/review.md"},
	{From: "supervisor/verify.md", To: "shared/verify.md"},

	{From: "setup_overview.md", To: "shared/auto_setup.md"},
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
// substituted in.
//
// The report.md destination uses the v1 flat path (shared/report/<R>.md) —
// one file per role, directly under shared/report/. Projects that ran
// through an earlier ateam binary may have files at the older
// shared/report/<R>/<R>.md or shared/report/<R>/report.md paths;
// flattenSharedLayout catches both on the next migration invocation.
var roleMigrations = []struct {
	from string // under roles/<R>/
	to   string // under root, with `{role}` placeholders
}{
	{from: "report_prompt.md", to: "prompts/report/{role}.prompt.md"},
	{from: "code_prompt.md", to: "prompts/code/{role}.prompt.md"},
	{from: "report_extra_prompt.md", to: "prompts/report/{role}.post.extra.md"},
	{from: "code_extra_prompt.md", to: "prompts/code/{role}.post.extra.md"},
	{from: "report.md", to: "shared/report/{role}.md"},
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
