package cmd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/display"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
)

const (
	autoRolesReviewMaxBytes      = 32 * 1024 // ~8K tokens
	autoRolesExecReportMaxLines  = 80
	autoRolesGitLogMaxLines      = 200
	autoRolesGitDiffStatMaxLines = 200
	autoRolesFallbackBaseCommits = 5 // when no prior review exists, diff against HEAD~5
)

// buildAutoRolesContext gathers every input the --auto-roles planner agent
// needs into a single markdown body. Injected into the prompt as
// {{ATEAM_AUTO_ROLES_COMMANDS_OUTPUT}}, replacing the prior "Inspect (do this
// first)" tool-call sequence so the agent can decide in one round-trip.
//
// Returns a hard error only when the project DB is unreachable. Every other
// per-section failure (missing review file, not in a git repo, etc.) is
// inlined as a labeled placeholder so the agent still sees what is available.
func buildAutoRolesContext(env *root.ResolvedEnv) (string, error) {
	db, err := openStateDB(env)
	if err != nil {
		return "", fmt.Errorf("open project DB for auto-roles context: %w", err)
	}
	defer db.Close()

	baseHash, baseLabel := autoRolesGitBase(env, db)

	var b strings.Builder

	fmt.Fprintln(&b, "### Roles configured for this project")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "```")
	var configRoles map[string]string
	if env.Config != nil {
		configRoles = env.Config.Roles
	}
	allKnown := prompts.AllKnownRoleIDs(configRoles, env.ProjectDir, env.OrgDir)
	writeRoleListing(&b, configRoles, allKnown)
	fmt.Fprintln(&b, "```")
	fmt.Fprintln(&b)

	fmt.Fprintln(&b, "### Per-role report freshness")
	fmt.Fprintln(&b)
	writeRoleReportFreshness(&b, env.ProjectDir)
	fmt.Fprintln(&b)

	fmt.Fprintln(&b, "### Latest review (`.ateam/supervisor/review.md`)")
	fmt.Fprintln(&b)
	writeReviewContent(&b, env.ProjectDir)
	fmt.Fprintln(&b)

	fmt.Fprintln(&b, "### Latest code-cycle execution report")
	fmt.Fprintln(&b)
	writeLatestExecutionReport(&b, env.ProjectDir)
	fmt.Fprintln(&b)

	fmt.Fprintf(&b, "### Git base for diff: `%s` (%s)\n\n", baseHash, baseLabel)

	if baseHash == "" {
		// Single-commit repo: root == HEAD, so the range syntax would be empty.
		// Show the initial commit directly instead.
		fmt.Fprintln(&b, "### Git log (initial commit; `git log -1 HEAD --stat`)")
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "```")
		writeGitCommand(&b, env.WorkDir, autoRolesGitLogMaxLines, "log", "-1", "HEAD", "--stat")
		fmt.Fprintln(&b, "```")
		fmt.Fprintln(&b)

		fmt.Fprintln(&b, "### Git diff stat (initial commit; `git show --stat HEAD`)")
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "```")
		writeGitCommand(&b, env.WorkDir, autoRolesGitDiffStatMaxLines, "show", "--stat", "HEAD")
		fmt.Fprintln(&b, "```")
	} else {
		fmt.Fprintf(&b, "### Git log since base (`git log %s..HEAD --stat`)\n\n", baseHash)
		fmt.Fprintln(&b, "```")
		writeGitCommand(&b, env.WorkDir, autoRolesGitLogMaxLines, "log", baseHash+"..HEAD", "--stat")
		fmt.Fprintln(&b, "```")
		fmt.Fprintln(&b)

		fmt.Fprintf(&b, "### Git diff stat since base (`git diff %s..HEAD --stat`)\n\n", baseHash)
		fmt.Fprintln(&b, "```")
		writeGitCommand(&b, env.WorkDir, autoRolesGitDiffStatMaxLines, "diff", baseHash+"..HEAD", "--stat")
		fmt.Fprintln(&b, "```")
	}

	return b.String(), nil
}

// autoRolesGitBase returns the commit hash to use as the diff base, plus a
// human-readable label explaining where it came from. Lookup order:
//  1. The git_start_hash of the most recent successful review run for this project.
//  2. Fallback: HEAD~autoRolesFallbackBaseCommits (if the repo has enough commits).
//  3. Fallback: the initial (root) commit when the repo has 2–N commits (root != HEAD).
//  4. Empty string when the repo has exactly one commit (root == HEAD); callers must
//     show HEAD directly because the <root>..HEAD range would be empty.
func autoRolesGitBase(env *root.ResolvedEnv, db *calldb.CallDB) (hash, label string) {
	rows, err := db.RecentRuns(calldb.RecentFilter{
		ProjectID:   env.ProjectID(),
		Action:      runner.ActionReview,
		Limit:       1,
		SuccessOnly: true,
	})
	if err == nil && len(rows) > 0 && rows[0].GitStartHash != "" {
		return rows[0].GitStartHash, fmt.Sprintf("from review agent_exec %d at %s", rows[0].ID, rows[0].StartedAt)
	}
	fallbackRef := fmt.Sprintf("HEAD~%d", autoRolesFallbackBaseCommits)
	if gitRefExists(env.WorkDir, fallbackRef) {
		return fallbackRef, fmt.Sprintf("no prior review found; falling back to %s", fallbackRef)
	}
	if rootHash := gitRootCommit(env.WorkDir); rootHash != "" {
		if rootHash == gitCurrentHead(env.WorkDir) {
			// Single-commit repo: root IS HEAD, so <root>..HEAD is always empty.
			// Return an empty hash so buildAutoRolesContext shows the commit directly.
			return "", "no prior review; single-commit repo, showing initial commit directly"
		}
		return rootHash, fmt.Sprintf("no prior review found; %s unavailable, falling back to initial commit", fallbackRef)
	}
	return fallbackRef, fmt.Sprintf("no prior review found; falling back to %s", fallbackRef)
}

// gitCurrentHead returns the full SHA of HEAD, or empty string on failure.
func gitCurrentHead(workDir string) string {
	var buf bytes.Buffer
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = workDir
	cmd.Stdout = &buf
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(buf.String())
}

// gitRefExists reports whether refName resolves to a commit in workDir.
func gitRefExists(workDir, refName string) bool {
	cmd := exec.Command("git", "rev-parse", "--verify", refName)
	cmd.Dir = workDir
	return cmd.Run() == nil
}

// gitRootCommit returns the hash of the oldest commit reachable from HEAD.
func gitRootCommit(workDir string) string {
	var buf bytes.Buffer
	cmd := exec.Command("git", "rev-list", "--max-parents=0", "HEAD")
	cmd.Dir = workDir
	cmd.Stdout = &buf
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(buf.String())
}

// writeRoleReportFreshness lists each .ateam/roles/<role>/report.md with its
// age and size. Newest first.
func writeRoleReportFreshness(b *strings.Builder, projectDir string) {
	rolesDir := filepath.Join(projectDir, "roles")
	entries, err := os.ReadDir(rolesDir)
	if err != nil {
		fmt.Fprintf(b, "_(no roles directory at %s: %v)_\n", rolesDir, err)
		return
	}
	type rep struct {
		role string
		path string
		info os.FileInfo
	}
	var reports []rep
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(rolesDir, e.Name(), "report.md")
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		reports = append(reports, rep{role: e.Name(), path: p, info: info})
	}
	if len(reports) == 0 {
		fmt.Fprintln(b, "_(no per-role reports yet)_")
		return
	}
	sort.Slice(reports, func(i, j int) bool {
		return reports[i].info.ModTime().After(reports[j].info.ModTime())
	})
	fmt.Fprintln(b, "```")
	for _, r := range reports {
		fmt.Fprintf(b, "%-30s  %-22s  %s\n", r.role, display.FmtDateAge(r.info.ModTime()), display.FmtBytes(int(r.info.Size())))
	}
	fmt.Fprintln(b, "```")
}

// writeReviewContent inlines the latest supervisor review (truncated if huge).
func writeReviewContent(b *strings.Builder, projectDir string) {
	path := filepath.Join(projectDir, "supervisor", "review.md")
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(b, "_(no review yet: %v)_\n", err)
		return
	}
	content := string(data)
	if len(content) > autoRolesReviewMaxBytes {
		content = content[:autoRolesReviewMaxBytes] + fmt.Sprintf("\n\n_(truncated: review.md is %d bytes; showing first %d)_", len(data), autoRolesReviewMaxBytes)
	}
	fmt.Fprintln(b, content)
}

// writeLatestExecutionReport finds the most recent execution_report.md across
// .ateam/runtime/<id>/, .ateam/shared/code/<id>/, and the pre-Step-4
// .ateam/supervisor/code/<id>/ fallback. Each layout puts the file exactly
// one level deep, so a glob is cheaper and clearer than a recursive walk.
func writeLatestExecutionReport(b *strings.Builder, projectDir string) {
	var candidates []string
	for _, pattern := range []string{
		filepath.Join(projectDir, "runtime", "*", "execution_report.md"),
		filepath.Join(projectDir, "shared", "code", "*", "execution_report.md"),
		filepath.Join(projectDir, "supervisor", "code", "*", "execution_report.md"),
	} {
		matches, _ := filepath.Glob(pattern) // bad-pattern error is impossible with fixed patterns
		candidates = append(candidates, matches...)
	}
	var newestPath string
	var newestModTime time.Time
	for _, p := range candidates {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		if newestPath == "" || info.ModTime().After(newestModTime) {
			newestPath = p
			newestModTime = info.ModTime()
		}
	}
	if newestPath == "" {
		fmt.Fprintln(b, "_(no prior code cycle)_")
		return
	}
	data, err := os.ReadFile(newestPath)
	if err != nil {
		fmt.Fprintf(b, "_(found %s but cannot read: %v)_\n", newestPath, err)
		return
	}
	fmt.Fprintf(b, "Path: `%s` (%s)\n\n", newestPath, display.FmtDateAge(newestModTime))
	fmt.Fprintln(b, "```")
	writeTruncatedLines(b, string(data), autoRolesExecReportMaxLines)
	fmt.Fprintln(b, "```")
}

// writeGitCommand runs `git <args...>` in workDir and writes the output to b,
// truncating to maxLines if needed. Failures are inlined as a labeled line.
func writeGitCommand(b *strings.Builder, workDir string, maxLines int, args ...string) {
	var buf bytes.Buffer
	cmd := exec.Command("git", args...)
	cmd.Dir = workDir
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(b, "_(git %s failed: %v)_\n%s", strings.Join(args, " "), err, buf.String())
		return
	}
	writeTruncatedLines(b, buf.String(), maxLines)
}

// writeTruncatedLines writes s to b, capping at maxLines and appending a
// "_(truncated: N more lines)_" marker when the cap fires. Shared between
// the execution-report header and the git command output to keep the marker
// format consistent across the bundle.
func writeTruncatedLines(b *strings.Builder, s string, maxLines int) {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		fmt.Fprintln(b)
		return
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= maxLines {
		fmt.Fprintln(b, s)
		return
	}
	fmt.Fprintln(b, strings.Join(lines[:maxLines], "\n"))
	fmt.Fprintf(b, "_(truncated: %d more lines)_\n", len(lines)-maxLines)
}
