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
		"prompts/code.prompt.md",
		"prompts/report/_post.extra.md",
		"prompts/review.prompt.md",
		"prompts/review.post.extra.md",
		"prompts/code_management.prompt.md",
		"prompts/code_verify.prompt.md",
		"prompts/auto_setup.prompt.md",
		"prompts/exec_debug.prompt.md",
		"shared/review.md",
		"shared/verify.md",
		"shared/auto_setup.md",
	}
	for _, p := range wantTargets {
		if !exists(root, p) {
			t.Errorf("missing %s", p)
		}
	}
	// Pre-flat per-action dirs must not exist after migration.
	for _, p := range []string{"shared/review", "shared/verify", "shared/auto_setup"} {
		if exists(root, p) {
			t.Errorf("pre-flat dir %s should not be created", p)
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
		"shared/report/security.md",
		"prompts/report/code.bugs.prompt.md",
	} {
		if !exists(root, want) {
			t.Errorf("missing %s", want)
		}
	}
	// v1 flat layout: no per-role subdir under shared/report/.
	if exists(root, "shared/report/security") {
		t.Error("pre-flat shared/report/security/ should not exist after migration")
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

// V1 migration is structural only — ALL_CAPS variables stay in place
// because the engine no longer rewrites them either. The migrator emits
// a Warning per migrated prompt file naming each surviving legacy token
// so the operator can convert by hand before the next run.
func TestV1LayoutLeavesContentUnchanged(t *testing.T) {
	root := t.TempDir()
	original := "Review {{ROLE}} for {{PROJECT_NAME}}\nOutput to {{OUTPUT_DIR}}\nSource: {{SOURCE_DIR}}"
	writeTree(t, root, map[string]string{
		"supervisor/review_prompt.md": original,
	})
	r, err := V1Layout(root)
	if err != nil {
		t.Fatal(err)
	}
	got := read(t, root, "prompts/review.prompt.md")
	if got != original {
		t.Fatalf("content should be unchanged after structural migration:\n got:  %q\n want: %q", got, original)
	}
	// Migrator warns about the four legacy tokens still in the body so
	// the operator knows the agent would otherwise see them literally.
	wantTokens := []string{"OUTPUT_DIR", "PROJECT_NAME", "ROLE", "SOURCE_DIR"}
	var found string
	for _, w := range r.Warnings {
		if strings.Contains(w, "prompts/review.prompt.md") && strings.Contains(w, "legacy ALL_CAPS") {
			found = w
			break
		}
	}
	if found == "" {
		t.Fatalf("expected a legacy-token warning for prompts/review.prompt.md, got warnings: %v", r.Warnings)
	}
	for _, tok := range wantTokens {
		if !strings.Contains(found, "{{"+tok+"}}") {
			t.Errorf("legacy-token warning missing %s; got: %q", tok, found)
		}
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

// TestV1LayoutIdempotentAfterArtifacts mirrors a real-world cycle: the
// migrator runs against a pre-v1 project, the user then runs `ateam
// report` + `ateam review` (here simulated by writing the v1 artifacts
// those commands produce), and the migrator runs again on the next ateam
// invocation. The second pass must be a no-op — even with real artifact
// directories present, NeedsMigration must return false and
// renameLegacyReportFiles must find nothing to do. This is Step 8's
// "idempotence under load" verification.
func TestV1LayoutIdempotentAfterArtifacts(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		// Pre-v1 layout for migration.
		"roles/security/report_prompt.md":      "security body",
		"supervisor/review_prompt.md":          "review body",
		"supervisor/code_management_prompt.md": "code mgmt body",
	})

	// Pass 1: structural migration.
	r1, err := V1Layout(root)
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if !r1.Changed() {
		t.Fatal("first pass should change things")
	}

	// Simulate `ateam report --roles security` + `ateam review` running:
	// each writes its primary artifact under shared/ at the v1 flat path.
	writeTree(t, root, map[string]string{
		"shared/report/security.md": "# Security Report\n\nFindings...",
		"shared/review.md":          "# Supervisor Review\n\nDecisions...",
		// And a code session from `ateam code` would land here (per-session
		// dir is the one shared/ layout that keeps a subdir per run).
		"shared/code/42/execution_report.md":    "# Execution Report\n\nDone.",
		"shared/code/42/01_task_code_prompt.md": "first task body",
		// Runtime artifacts too (per-exec scratch, untouched by migrator).
		"runtime/42/execution_report.md": "report",
		"logs/42/agent.jsonl":            `{"ts":"...","event":"..."}`,
	})

	// Pass 2: must be a complete no-op.
	r2, err := V1Layout(root)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if r2.Changed() {
		t.Errorf("second pass should be a no-op despite real artifacts on disk, got Result=%+v", r2)
	}

	// Verify artifacts survived untouched.
	for path, want := range map[string]string{
		"shared/report/security.md":          "# Security Report\n\nFindings...",
		"shared/review.md":                   "# Supervisor Review\n\nDecisions...",
		"shared/code/42/execution_report.md": "# Execution Report\n\nDone.",
		"runtime/42/execution_report.md":     "report",
	} {
		if got := read(t, root, path); got != want {
			t.Errorf("%s drifted: got %q, want %q", path, got, want)
		}
	}
}

// TestV1LayoutMovesCodeSessions covers Step 4: the supervisor/code/<exec>/
// tree is the last directory under supervisor/ and moves to shared/code/.
// Whole subtree (per-exec files: code_prompt.md, execution_report.md, …)
// rides along atomically.
func TestV1LayoutMovesCodeSessions(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"supervisor/code/597/code_prompt.md":      "first task",
		"supervisor/code/597/execution_report.md": "report content",
		"supervisor/code/598/01_other_prompt.md":  "second task",
	})

	r, err := V1Layout(root)
	if err != nil {
		t.Fatal(err)
	}

	for path, want := range map[string]string{
		"shared/code/597/code_prompt.md":      "first task",
		"shared/code/597/execution_report.md": "report content",
		"shared/code/598/01_other_prompt.md":  "second task",
	} {
		if !exists(root, path) {
			t.Errorf("missing %s after migration", path)
			continue
		}
		if got := read(t, root, path); got != want {
			t.Errorf("%s content = %q, want %q", path, got, want)
		}
	}

	if exists(root, "supervisor/code") {
		t.Error("supervisor/code/ should be moved away, not left behind")
	}
	if exists(root, "supervisor") {
		t.Error("now-empty supervisor/ should be removed by cleanup")
	}
	if !r.Changed() {
		t.Error("expected Changed=true")
	}

	// Idempotence: re-running V1Layout is a no-op once the dir is at the new
	// location and supervisor/ has been swept away.
	r2, err := V1Layout(root)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Changed() {
		t.Errorf("second pass should be a no-op, got Result=%+v", r2)
	}
}

// TestV1LayoutCodeDirTargetExists covers the conflict path: a project that
// ran a pre-Step-4 binary AND a post-Step-4 binary ends up with sessions at
// both supervisor/code/ and shared/code/. The migrator warns + leaves both
// in place rather than attempting an automatic merge — exec_ids are
// disjoint in practice, so the user can `mv supervisor/code/* shared/code/`
// themselves to consolidate.
func TestV1LayoutCodeDirTargetExists(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"supervisor/code/100/code_prompt.md": "old session",
		"shared/code/200/code_prompt.md":     "new session",
	})

	r, err := V1Layout(root)
	if err != nil {
		t.Fatal(err)
	}

	// Both paths still exist.
	if !exists(root, "supervisor/code/100/code_prompt.md") {
		t.Error("legacy supervisor/code/100/ should be preserved on conflict")
	}
	if !exists(root, "shared/code/200/code_prompt.md") {
		t.Error("v1 shared/code/200/ should be preserved on conflict")
	}
	// Warning surfaces.
	if len(r.Warnings) == 0 || !strings.Contains(r.Warnings[0], "supervisor/code") {
		t.Errorf("expected warning about supervisor/code conflict, got %v", r.Warnings)
	}
}

// TestV1LayoutFlattenSharedReports covers projects already on a pre-flat v1
// layout: shared/report/<R>/<R>.md (current v1 spec) or the older
// shared/report/<R>/report.md (pre-Step-6 transitional). NeedsMigration is
// false in both cases, but the flatten pass still runs and hoists the file
// to shared/report/<R>.md, then removes the now-empty <R>/ dir.
func TestV1LayoutFlattenSharedReports(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		// pre-flat spec layout
		"shared/report/security/security.md": "spec filename",
		// pre-flat with older transitional filename
		"shared/report/code.bugs/report.md": "older filename",
	})

	r, err := V1Layout(root)
	if err != nil {
		t.Fatal(err)
	}

	for flat, want := range map[string]string{
		"shared/report/security.md":  "spec filename",
		"shared/report/code.bugs.md": "older filename",
	} {
		if !exists(root, flat) {
			t.Errorf("expected %s after flatten", flat)
			continue
		}
		if got := read(t, root, flat); got != want {
			t.Errorf("%s content = %q, want %q", flat, got, want)
		}
	}
	// Old nested dirs should be gone.
	for _, oldDir := range []string{"shared/report/security", "shared/report/code.bugs"} {
		if exists(root, oldDir) {
			t.Errorf("pre-flat dir %s should have been removed", oldDir)
		}
	}
	if !r.Changed() {
		t.Error("expected Changed=true for the flatten pass")
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

// TestV1LayoutFlattenSharedSingletons mirrors the report flatten for the
// supervisor singletons: shared/review/review.md, shared/verify/verify.md,
// shared/auto_setup/auto_setup.md → flat siblings under shared/.
func TestV1LayoutFlattenSharedSingletons(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"shared/review/review.md":         "review body",
		"shared/verify/verify.md":         "verify body",
		"shared/auto_setup/auto_setup.md": "setup body",
	})

	r, err := V1Layout(root)
	if err != nil {
		t.Fatal(err)
	}

	for flat, want := range map[string]string{
		"shared/review.md":     "review body",
		"shared/verify.md":     "verify body",
		"shared/auto_setup.md": "setup body",
	} {
		if got := read(t, root, flat); got != want {
			t.Errorf("%s = %q, want %q", flat, got, want)
		}
	}
	for _, oldDir := range []string{"shared/review", "shared/verify", "shared/auto_setup"} {
		if exists(root, oldDir) {
			t.Errorf("pre-flat dir %s should have been removed", oldDir)
		}
	}
	if !r.Changed() {
		t.Error("expected Changed=true")
	}

	r2, err := V1Layout(root)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Changed() {
		t.Errorf("second pass should be no-op, got %+v", r2)
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
		t.Skip("fixture has already been migrated to v1 layout; nothing to exercise")
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
		"shared/review.md",
		"shared/verify.md",
		"prompts/review.post.extra.md",
	} {
		if !exists(dst, p) {
			t.Errorf("expected %s after migration", p)
		}
	}
	// At least one role report output landed flat under shared/report/.
	matches, err := filepath.Glob(filepath.Join(dst, "shared/report/*.md"))
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

	want := "shared/auto_setup.md"
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

// TestRoleNamedReportNotDeleted guards the data-loss edge where a role is
// literally named "report": the source path collapses to
// shared/report/report/report.md and the flat target to
// shared/report/report.md. Without care, the duplicate-source-name list in
// flattenSharedReports could attempt the same move twice or the from==to
// guard in move() could misfire. The file must end up at the flat path with
// its content intact, and re-running must be a no-op.
func TestRoleNamedReportNotDeleted(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join("shared", "report", "report", "report.md")
	flat := filepath.Join("shared", "report", "report.md")
	writeTree(t, root, map[string]string{nested: "the only report\n"})

	// Pass 1: nested → flat.
	if _, err := V1Layout(root); err != nil {
		t.Fatalf("V1Layout pass 0: %v", err)
	}
	if !exists(root, flat) {
		t.Fatalf("pass 0: report for role \"report\" missing at flat path %s", flat)
	}
	if got := read(t, root, flat); got != "the only report\n" {
		t.Fatalf("pass 0: report content changed: %q", got)
	}

	// Pass 2: idempotent — must not destroy the file we just hoisted.
	if _, err := V1Layout(root); err != nil {
		t.Fatalf("V1Layout pass 1: %v", err)
	}
	if !exists(root, flat) {
		t.Fatalf("pass 1: flat report deleted on re-run")
	}
	if got := read(t, root, flat); got != "the only report\n" {
		t.Fatalf("pass 1: report content changed: %q", got)
	}
}

// TestDirectoryAtFileTargetWarns guards against a directory sitting where a
// file move expects its target: move() must warn and skip rather than
// os.ReadFile the directory and abort the whole migration with an EISDIR.
// Setup: flattenSharedReports wants to hoist shared/report/security/security.md
// to shared/report/security.md, but a directory already squats the flat target.
func TestDirectoryAtFileTargetWarns(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		filepath.Join("shared", "report", "security", "security.md"):  "nested body\n",
		filepath.Join("shared", "report", "security.md", "stray.txt"): "x\n",
	})

	r, err := V1Layout(root)
	if err != nil {
		t.Fatalf("V1Layout returned error instead of warning: %v", err)
	}
	if len(r.Warnings) == 0 {
		t.Fatalf("expected a warning for the directory-at-target conflict, got none")
	}
	if !strings.Contains(r.Warnings[0], "directory") {
		t.Errorf("warning should mention the directory conflict, got %q", r.Warnings[0])
	}
}
