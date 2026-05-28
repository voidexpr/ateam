package migrate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeTree creates files (and their parent dirs) under root.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// exists reports whether root/rel is present on disk.
func exists(root, rel string) bool {
	_, err := os.Lstat(filepath.Join(root, rel))
	return err == nil
}

// read returns the content of root/rel (fatals on missing).
func read(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestNeedsMigration(t *testing.T) {
	t.Run("empty dir", func(t *testing.T) {
		if NeedsMigration(t.TempDir()) {
			t.Fatal("empty dir should not need migration")
		}
	})
	t.Run("has roles", func(t *testing.T) {
		root := t.TempDir()
		os.Mkdir(filepath.Join(root, "roles"), 0o755)
		if !NeedsMigration(root) {
			t.Fatal("roles/ presence should trigger migration")
		}
	})
	t.Run("has setup_overview", func(t *testing.T) {
		root := t.TempDir()
		os.WriteFile(filepath.Join(root, "setup_overview.md"), nil, 0o644)
		if !NeedsMigration(root) {
			t.Fatal("setup_overview.md should trigger migration")
		}
	})
	t.Run("only v1 layout", func(t *testing.T) {
		root := t.TempDir()
		os.MkdirAll(filepath.Join(root, "prompts", "report"), 0o755)
		if NeedsMigration(root) {
			t.Fatal("v1-only layout should not need migration")
		}
	})
}

func TestV1LayoutStaticMoves(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"report_base_prompt.md":                "report base",
		"code_base_prompt.md":                  "code base",
		"report_extra_prompt.md":               "report extra",
		"supervisor/review_prompt.md":          "review body",
		"supervisor/review_extra_prompt.md":    "review extra",
		"supervisor/code_management_prompt.md": "code mgmt",
		"supervisor/code_verify_prompt.md":     "verify body",
		"supervisor/auto_setup_prompt.md":      "auto setup",
		"supervisor/exec_debug_prompt.md":      "exec debug",
		"supervisor/review.md":                 "old review output",
		"supervisor/verify.md":                 "old verify output",
		"setup_overview.md":                    "overview",
	})

	r, err := V1Layout(root)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Changed() {
		t.Fatal("expected changes")
	}

	wantTargets := []string{
		"prompts/report/_post.format.md",
		"prompts/code/_post.format.md",
		"prompts/report/_post.extra.md",
		"prompts/review.prompt.md",
		"prompts/review.post.extra.md",
		"prompts/code_management.prompt.md",
		"prompts/code_verify.prompt.md",
		"prompts/auto_setup.prompt.md",
		"prompts/exec_debug.prompt.md",
		"shared/review/review.md",
		"shared/verify/verify.md",
		"shared/auto_setup/auto_setup.md",
	}
	for _, p := range wantTargets {
		if !exists(root, p) {
			t.Errorf("missing %s", p)
		}
	}

	// Sources should be gone.
	wantGone := []string{
		"report_base_prompt.md",
		"supervisor/review_prompt.md",
		"setup_overview.md",
	}
	for _, p := range wantGone {
		if exists(root, p) {
			t.Errorf("source %s should be removed", p)
		}
	}

	// Content preserved.
	if read(t, root, "prompts/review.prompt.md") != "review body" {
		t.Error("content not preserved on move")
	}

	// supervisor/ should be removed since everything moved.
	if exists(root, "supervisor") {
		t.Error("empty supervisor/ should be removed")
	}
}

func TestV1LayoutRoleMoves(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"roles/security/report_prompt.md":       "security report",
		"roles/security/code_prompt.md":         "security code",
		"roles/security/report_extra_prompt.md": "security extra",
		"roles/security/report.md":              "security output",
		"roles/security/history/2026-01-01.md":  "old history",
		"roles/code.bugs/report_prompt.md":      "dotted role report",
	})
	r, err := V1Layout(root)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"prompts/report/security.prompt.md",
		"prompts/code/security.prompt.md",
		"prompts/report/security.post.extra.md",
		"shared/report/security/security.md",
		"prompts/report/code.bugs.prompt.md",
	} {
		if !exists(root, want) {
			t.Errorf("missing %s", want)
		}
	}
	// Spec filename: <role>.md, not report.md.
	if exists(root, "shared/report/security/report.md") {
		t.Error("shared/report/security/report.md should have been renamed to security.md per spec")
	}

	// history/ dropped.
	if exists(root, "roles/security/history") {
		t.Error("roles/security/history should be removed")
	}
	// Empty role dir cleaned up.
	if exists(root, "roles/security") {
		t.Error("empty roles/security should be removed")
	}
	// roles/ cleaned up.
	if exists(root, "roles") {
		t.Error("empty roles/ should be removed")
	}

	// Sanity on result counts.
	if len(r.Moved) < 5 {
		t.Errorf("expected ≥5 moves, got %d", len(r.Moved))
	}
}

// V1 migration is structural only — ALL_CAPS variables stay in place and are
// handled by the engine's compat shim at render time. Content rewriting is a
// separate mechanical pass that will run later via assembler.RewriteContent.
func TestV1LayoutLeavesContentUnchanged(t *testing.T) {
	root := t.TempDir()
	original := "Review {{ROLE}} for {{PROJECT_NAME}}\nOutput to {{OUTPUT_DIR}}\nSource: {{SOURCE_DIR}}"
	writeTree(t, root, map[string]string{
		"supervisor/review_prompt.md": original,
	})
	if _, err := V1Layout(root); err != nil {
		t.Fatal(err)
	}
	got := read(t, root, "prompts/review.prompt.md")
	if got != original {
		t.Fatalf("content should be unchanged after structural migration:\n got:  %q\n want: %q", got, original)
	}
}

func TestV1LayoutIdempotent(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"supervisor/review_prompt.md":     "body",
		"roles/security/report_prompt.md": "{{ROLE}} body",
	})

	r1, err := V1Layout(root)
	if err != nil {
		t.Fatal(err)
	}
	if !r1.Changed() {
		t.Fatal("first pass should change things")
	}

	r2, err := V1Layout(root)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Changed() {
		t.Fatalf("second pass should be no-op, got Result=%+v", r2)
	}
	if read(t, root, "prompts/report/security.prompt.md") != "{{ROLE}} body" {
		t.Error("content drifted on second pass (should be preserved unchanged)")
	}
}

// TestV1LayoutSecondPassRenamesReportMd covers an already-v1-migrated
// project that ran through a pre-Step-6 binary: shared/report/<R>/report.md
// exists but the spec wants <R>.md. NeedsMigration is false (no pre-v1
// artifacts), but the rename pass still runs and collapses report.md to
// <role>.md.
func TestV1LayoutSecondPassRenamesReportMd(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"shared/report/security/report.md":  "old filename",
		"shared/report/code.bugs/report.md": "dotted role",
	})

	r, err := V1Layout(root)
	if err != nil {
		t.Fatal(err)
	}

	for old, want := range map[string]string{
		"shared/report/security/security.md":   "old filename",
		"shared/report/code.bugs/code.bugs.md": "dotted role",
	} {
		if !exists(root, old) {
			t.Errorf("expected %s after rename", old)
		}
		if got := read(t, root, old); got != want {
			t.Errorf("%s content = %q, want %q", old, got, want)
		}
	}
	if exists(root, "shared/report/security/report.md") {
		t.Error("old shared/report/security/report.md should have been renamed")
	}
	if exists(root, "shared/report/code.bugs/report.md") {
		t.Error("old shared/report/code.bugs/report.md should have been renamed")
	}
	if !r.Changed() {
		t.Error("expected Changed=true for the rename pass")
	}

	// Idempotence: second invocation is a no-op.
	r2, err := V1Layout(root)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Changed() {
		t.Errorf("second pass should be a no-op, got Result=%+v", r2)
	}
}

func TestV1LayoutTargetExistsDifferentContent(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"supervisor/review_prompt.md": "old",
		"prompts/review.prompt.md":    "new — should not be overwritten",
	})
	r, err := V1Layout(root)
	if err != nil {
		t.Fatal(err)
	}
	// Target intact (content differs, never overwritten); source renamed to
	// .legacy so it's out of the read path but inspectable by the user.
	if exists(root, "supervisor/review_prompt.md") {
		t.Error("source should have been renamed to .legacy when target exists with different content")
	}
	if !exists(root, "supervisor/review_prompt.md.legacy") {
		t.Error("source should be preserved at .legacy")
	}
	if read(t, root, "supervisor/review_prompt.md.legacy") != "old" {
		t.Error(".legacy file should hold the original source content")
	}
	if read(t, root, "prompts/review.prompt.md") != "new — should not be overwritten" {
		t.Error("target should not have been overwritten")
	}
	if len(r.Warnings) == 0 {
		t.Error("expected warning about target conflict")
	}
}

func TestV1LayoutTargetExistsIdenticalContent(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"supervisor/review_prompt.md": "same content both places",
		"prompts/review.prompt.md":    "same content both places",
	})
	r, err := V1Layout(root)
	if err != nil {
		t.Fatal(err)
	}
	// Content matches — source removed as duplicate cleanup, no warning.
	if exists(root, "supervisor/review_prompt.md") {
		t.Error("source should have been removed when target has identical content")
	}
	if read(t, root, "prompts/review.prompt.md") != "same content both places" {
		t.Error("target should remain intact")
	}
	for _, warn := range r.Warnings {
		if strings.Contains(warn, "review_prompt.md") {
			t.Errorf("did not expect warning for identical-content cleanup, got: %s", warn)
		}
	}
}

func TestV1LayoutPreservesUnknownFiles(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"supervisor/my_local_notes.md":  "user-authored file",
		"supervisor/other_random.txt":   "another user file",
		"supervisor/review_prompt.md":   "real prompt",
		"roles/security/my_scratch.txt": "user role scratch",
	})
	if _, err := V1Layout(root); err != nil {
		t.Fatal(err)
	}
	// Genuinely unknown files (not in junkSupervisor / junkPerRole) stay where
	// they were; supervisor/ and roles/security/ therefore aren't removed.
	for _, p := range []string{
		"supervisor/my_local_notes.md",
		"supervisor/other_random.txt",
		"roles/security/my_scratch.txt",
	} {
		if !exists(root, p) {
			t.Errorf("unknown file %s should be preserved", p)
		}
	}
	if !exists(root, "supervisor") {
		t.Error("non-empty supervisor/ should not be removed")
	}
	if !exists(root, "roles/security") {
		t.Error("non-empty roles/security/ should not be removed")
	}
}

// TestV1LayoutCleansJunkArtifacts confirms the migrator drops the well-known
// runtime leftovers (last_run_*.md per role, code_output.md +
// code_verification_report.md under supervisor) so otherwise-empty parents
// get cleaned up. Genuine user files are untouched (covered by
// TestV1LayoutPreservesUnknownFiles).
func TestV1LayoutCleansJunkArtifacts(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"roles/security/last_run_output.md":      "stderr from last run",
		"roles/security/last_run_error.md":       "error from last run",
		"supervisor/code_output.md":              "stale code output",
		"supervisor/code_verification_report.md": "stale verify summary",
	})
	if _, err := V1Layout(root); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		"roles/security/last_run_output.md",
		"roles/security/last_run_error.md",
		"supervisor/code_output.md",
		"supervisor/code_verification_report.md",
	} {
		if exists(root, p) {
			t.Errorf("junk file %s should be removed", p)
		}
	}
	// With nothing else under them, roles/ and supervisor/ are cleaned up too.
	if exists(root, "roles") {
		t.Error("roles/ should be removed after junk cleanup")
	}
	if exists(root, "supervisor") {
		t.Error("supervisor/ should be removed after junk cleanup")
	}
}

// TestRsyncFixture runs the migrator against a real .ateam tree copied into a
// tempdir. Source dirs are read-only — even a buggy migrator can't damage them.
// Skips if the source isn't present (CI / fresh checkout).
func TestRsyncFixture(t *testing.T) {
	homeAteam := filepath.Join(os.Getenv("HOME"), "projects", "ateam", ".ateam")
	if _, err := os.Stat(homeAteam); err != nil {
		t.Skipf("fixture not available: %v", err)
	}
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skipf("rsync not available: %v", err)
	}

	tempRoot := t.TempDir()
	dst := filepath.Join(tempRoot, ".ateam")
	cmd := exec.Command("rsync", "-a", "--exclude=state.sqlite*", "--exclude=logs", "--exclude=runtime",
		"--exclude=cache", "--exclude=secrets.env*", homeAteam+"/", dst+"/")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("rsync failed: %v\n%s", err, out)
	}

	if !NeedsMigration(dst) {
		t.Fatal("real fixture should need migration")
	}

	r1, err := V1Layout(dst)
	if err != nil {
		t.Fatalf("first pass failed: %v", err)
	}
	if !r1.Changed() {
		t.Fatal("expected first pass to change things")
	}
	t.Logf("first pass: %d moves, %d removed dirs, %d warnings",
		len(r1.Moved), len(r1.RemovedDirs), len(r1.Warnings))

	// Spot-checks based on what this real fixture contains: it ships role
	// report outputs + supervisor review/verify outputs + a review extras
	// fragment, but relies on embedded defaults for the prompts themselves.
	for _, p := range []string{
		"shared/review/review.md",
		"shared/verify/verify.md",
		"prompts/review.post.extra.md",
	} {
		if !exists(dst, p) {
			t.Errorf("expected %s after migration", p)
		}
	}
	// At least one role report output landed in shared/report/.
	matches, err := filepath.Glob(filepath.Join(dst, "shared/report/*/*.md"))
	if err != nil || len(matches) == 0 {
		t.Errorf("expected migrated role report outputs, got %v (err %v)", matches, err)
	}

	// Second pass must be a no-op.
	r2, err := V1Layout(dst)
	if err != nil {
		t.Fatalf("second pass failed: %v", err)
	}
	if r2.Changed() {
		t.Fatalf("second pass should be no-op, got %+v", r2)
	}
}

// TestRsyncListmanagerFixture exercises setup_overview.md migration on the
// listmanager fixture.
func TestRsyncListmanagerFixture(t *testing.T) {
	src := filepath.Join(os.Getenv("HOME"), "projects", "ateam", "test_data", "projects", "listmanager", ".ateam")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture not available: %v", err)
	}
	if _, err := os.Stat(filepath.Join(src, "setup_overview.md")); err != nil {
		t.Skipf("listmanager fixture missing setup_overview.md: %v", err)
	}
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skipf("rsync not available: %v", err)
	}

	dst := filepath.Join(t.TempDir(), ".ateam")
	cmd := exec.Command("rsync", "-a", "--exclude=state.sqlite*", "--exclude=logs", "--exclude=runtime",
		"--exclude=cache", src+"/", dst+"/")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("rsync failed: %v\n%s", err, out)
	}

	if _, err := V1Layout(dst); err != nil {
		t.Fatal(err)
	}

	want := "shared/auto_setup/auto_setup.md"
	if !exists(dst, want) {
		t.Errorf("listmanager setup_overview.md should have moved to %s", want)
	}
	if exists(dst, "setup_overview.md") {
		t.Error("setup_overview.md should be removed at root after migration")
	}
}

func TestMoveSourceMissingIsNoop(t *testing.T) {
	root := t.TempDir()
	var r Result
	moved, err := move(root, "missing.md", "prompts/foo.md", &r)
	if err != nil {
		t.Fatal(err)
	}
	if moved {
		t.Fatal("expected moved=false")
	}
	if len(r.Moved) != 0 {
		t.Fatal("expected no Moved entries")
	}
}

func TestMoveWarnsOnExistingTarget(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"src.md": "source",
		"dst.md": "dest",
	})
	var r Result
	moved, err := move(root, "src.md", "dst.md", &r)
	if err != nil {
		t.Fatal(err)
	}
	if moved {
		t.Fatal("expected moved=false when target exists")
	}
	if len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "already exists") {
		t.Errorf("expected target-exists warning, got %v", r.Warnings)
	}
}
