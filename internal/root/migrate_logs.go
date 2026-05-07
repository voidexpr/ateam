package root

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ateam/internal/calldb"
)

// layoutSentinel marks that MigrateLogsLayout has already run for a project.
const layoutSentinel = "logs/.layout-v2"

// MigrateLogsLayout brings a project's log directory up to the logs/<exec_id>/
// layout. It is sentinel-guarded so repeat invocations are cheap no-ops.
//
// What it does, idempotently:
//  1. For every agent_execs row whose stream_file ends in "_stream.jsonl"
//     (the legacy <TS>_<ACTION>_ prefix layout), move stream/stderr/settings/
//     exec.md into logs/<id>/ with the new filenames (stream.jsonl, stderr.out,
//     settings.json, cmd.md). The cmd.md content is left untouched per design.
//  2. Delete legacy *_prompt.md files in roles/<id>/history/ and supervisor/
//     history/ — those archives are replaced by logs/<id>/prompt.md going
//     forward. Output history files (<TS>.<kind>.md) are preserved.
//  3. Delete legacy canonical *_error.md files (report_error.md, review_error.md,
//     verify_error.md, code_error.md, auto_setup_error.md). Failure context now
//     lives in logs/<id>/{cmd.md,stderr.out,stream.jsonl}.
//  4. Remove the runner.log entirely — agent_execs is the source of truth.
//  5. Best-effort cleanup of now-empty legacy log subdirectories.
//
// Per-row failures are logged to stderr but do not abort migration.
func MigrateLogsLayout(env *ResolvedEnv, db *calldb.CallDB) error {
	if env == nil || env.ProjectDir == "" || db == nil {
		return nil
	}
	sentinel := filepath.Join(env.ProjectDir, layoutSentinel)
	if _, err := os.Stat(sentinel); err == nil {
		return nil
	}

	if !needsMigration(env.ProjectDir) {
		return writeSentinel(sentinel)
	}

	if err := migrateRows(env, db); err != nil {
		return err
	}
	deleteLegacyHistoryPrompts(env.ProjectDir)
	deleteLegacyErrorFiles(env.ProjectDir)
	_ = os.Remove(filepath.Join(env.ProjectDir, "logs", "runner.log"))
	cleanupEmptyLegacyDirs(env.ProjectDir)

	return writeSentinel(sentinel)
}

func writeSentinel(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("layout v2 — logs/<exec_id>/\n"), 0600)
}

func needsMigration(projectDir string) bool {
	for _, sub := range []string{"logs/roles", "logs/parallel", "logs/run", "logs/supervisor"} {
		if _, err := os.Stat(filepath.Join(projectDir, sub)); err == nil {
			return true
		}
	}
	if _, err := os.Stat(filepath.Join(projectDir, "logs", "runner.log")); err == nil {
		return true
	}
	return false
}

func migrateRows(env *ResolvedEnv, db *calldb.CallDB) error {
	type legacyRow struct {
		id           int64
		role, action string
		startedAt    string
		streamFile   string
	}

	// Collect rows into a slice and close the cursor BEFORE issuing any
	// UPDATE — calldb runs with SetMaxOpenConns(1), so an open SELECT cursor
	// blocks all writes on the same connection.
	rawDB := db.RawDB()
	rows, err := rawDB.Query("SELECT id, role, action, started_at, stream_file FROM agent_execs WHERE stream_file LIKE '%_stream.jsonl'")
	if err != nil {
		return fmt.Errorf("query legacy rows: %w", err)
	}
	var legacy []legacyRow
	for rows.Next() {
		var r legacyRow
		if err := rows.Scan(&r.id, &r.role, &r.action, &r.startedAt, &r.streamFile); err != nil {
			rows.Close()
			return fmt.Errorf("scan legacy row: %w", err)
		}
		legacy = append(legacy, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, r := range legacy {
		oldStream := ResolveStreamPath(env.ProjectDir, env.OrgDir, r.streamFile)
		if _, err := os.Stat(oldStream); err != nil {
			continue
		}
		newDir := filepath.Join(env.ProjectDir, "logs", strconv.FormatInt(r.id, 10))
		if err := os.MkdirAll(newDir, 0700); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: mkdir %s: %v\n", newDir, err)
			continue
		}
		prefix := strings.TrimSuffix(oldStream, "_stream.jsonl")
		moves := []struct{ src, dst string }{
			{oldStream, filepath.Join(newDir, "stream.jsonl")},
			{prefix + "_stderr.log", filepath.Join(newDir, "stderr.out")},
			{prefix + "_settings.json", filepath.Join(newDir, "settings.json")},
			{prefix + "_exec.md", filepath.Join(newDir, "cmd.md")},
		}
		for _, m := range moves {
			if _, err := os.Stat(m.src); err != nil {
				continue
			}
			if err := os.Rename(m.src, m.dst); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: rename %s -> %s: %v\n", m.src, m.dst, err)
			}
		}
		// Locate the legacy prompt by ±5s match against the row's started_at.
		if promptPath := findLegacyPrompt(env.ProjectDir, r.role, r.action, r.startedAt); promptPath != "" {
			dst := filepath.Join(newDir, "prompt.md")
			if err := os.Rename(promptPath, dst); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: rename %s -> %s: %v\n", promptPath, dst, err)
			}
		}
		// Update stream_file to the new path.
		newRel, err := filepath.Rel(env.ProjectDir, filepath.Join(newDir, "stream.jsonl"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: rel for %s: %v\n", newDir, err)
			continue
		}
		if err := db.UpdateStreamFile(r.id, newRel); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: update stream_file for exec %d: %v\n", r.id, err)
		}
		// Populate output_file when the canonical/history file still exists.
		// Without this, the web UI's run-page output link can't recover the
		// path for migrated legacy rows (the new stream filename no longer
		// carries a timestamp the resolver could fuzzy-match on).
		if outRel := findLegacyOutput(env.ProjectDir, r.role, r.action, r.startedAt); outRel != "" {
			if err := db.UpdateOutputFile(r.id, outRel); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: update output_file for exec %d: %v\n", r.id, err)
			}
		}
	}
	return nil
}

// findLegacyOutput returns the project-relative path to a row's archived
// output file (e.g. roles/security/history/<TS>.report.md). Returns "" when
// no matching file exists. Uses ±5s skew like findLegacyPrompt for
// resilience against second-level timestamp drift.
func findLegacyOutput(projectDir, role, action, startedAt string) string {
	t, err := time.Parse(time.RFC3339, startedAt)
	if err != nil {
		return ""
	}
	histDir := legacyHistoryDir(projectDir, role, action)
	suffix := legacyOutputSuffix(action)
	if histDir == "" || suffix == "" {
		return ""
	}
	hit := FindHistoryFileWithSkew(histDir, t, suffix)
	if hit == "" {
		return ""
	}
	rel, err := filepath.Rel(projectDir, hit)
	if err != nil {
		return ""
	}
	return rel
}

// legacyOutputSuffix maps an action to the kind suffix the legacy code wrote
// for its primary output file in <role>/history/ or supervisor/history/.
func legacyOutputSuffix(action string) string {
	switch action {
	case "report":
		return "report.md"
	case "review":
		return "review.md"
	case "verify":
		return "verify.md"
	case "run":
		return "run_output.md"
	default:
		return ""
	}
}

// findLegacyPrompt looks up the timestamped prompt archive for a (role, action)
// pair, allowing a ±5s skew in case the prompt write happened in a different
// second than the row's started_at.
//
// The RFC3339-encoded started_at preserves the timezone of the original run,
// which matches what the legacy archivePrompt baked into the filename. We
// must NOT shift to the current system's local time — that would break
// migrations across DST or TZ moves.
func findLegacyPrompt(projectDir, role, action, startedAt string) string {
	t, err := time.Parse(time.RFC3339, startedAt)
	if err != nil {
		return ""
	}
	histDir := legacyHistoryDir(projectDir, role, action)
	suffix := legacyPromptSuffix(action)
	if histDir == "" || suffix == "" {
		return ""
	}
	return FindHistoryFileWithSkew(histDir, t, suffix)
}

func legacyHistoryDir(projectDir, role, action string) string {
	switch action {
	case "report", "run":
		if role == "" {
			return ""
		}
		return filepath.Join(projectDir, "roles", role, "history")
	case "review", "verify", "code":
		return filepath.Join(projectDir, "supervisor", "history")
	default:
		return ""
	}
}

func legacyPromptSuffix(action string) string {
	switch action {
	case "report":
		return "report_prompt.md"
	case "run":
		return "run_prompt.md"
	case "review":
		return "review_prompt.md"
	case "verify":
		return "code_verify_prompt.md"
	case "code":
		return "code_management_prompt.md"
	default:
		return ""
	}
}

// deleteLegacyHistoryPrompts walks roles/*/history/ and supervisor/history/ and
// removes <TS>.<kind>_prompt.md files. Output files (<TS>.<kind>.md) are kept
// for the web UI's legacy fallback path.
func deleteLegacyHistoryPrompts(projectDir string) {
	rolesDir := filepath.Join(projectDir, "roles")
	entries, _ := os.ReadDir(rolesDir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		removePromptFiles(filepath.Join(rolesDir, e.Name(), "history"))
	}
	removePromptFiles(filepath.Join(projectDir, "supervisor", "history"))
}

func removePromptFiles(dir string) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), "_prompt.md") {
			path := filepath.Join(dir, e.Name())
			if err := os.Remove(path); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: remove %s: %v\n", path, err)
			}
		}
	}
}

// deleteLegacyErrorFiles drops the canonical-only *_error.md files. The new
// model keeps failure context in logs/<exec_id>/{cmd.md,stderr.out,stream.jsonl}.
func deleteLegacyErrorFiles(projectDir string) {
	supervisor := filepath.Join(projectDir, "supervisor")
	for _, name := range []string{"review_error.md", "verify_error.md", "code_error.md", "auto_setup_error.md"} {
		_ = os.Remove(filepath.Join(supervisor, name))
	}
	rolesDir := filepath.Join(projectDir, "roles")
	entries, _ := os.ReadDir(rolesDir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		_ = os.Remove(filepath.Join(rolesDir, e.Name(), "report_error.md"))
	}
}

// cleanupEmptyLegacyDirs removes now-empty legacy log subdirectories. Files
// that have no matching DB row are left in place so we never silently destroy
// forensic data.
func cleanupEmptyLegacyDirs(projectDir string) {
	logsDir := filepath.Join(projectDir, "logs")
	for _, sub := range []string{"roles", "parallel", "run", "supervisor"} {
		removeIfEmpty(filepath.Join(logsDir, sub))
	}
}

func removeIfEmpty(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	if len(entries) > 0 {
		for _, e := range entries {
			if e.IsDir() {
				removeIfEmpty(filepath.Join(dir, e.Name()))
			}
		}
		entries, _ = os.ReadDir(dir)
	}
	if len(entries) == 0 {
		_ = os.Remove(dir)
	}
}
