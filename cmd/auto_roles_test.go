package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ateam/internal/calldb"
)

func TestParseAutoRolesOutput(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantRoles []string
		// wantRationaleSubstring lets the test stay robust to whitespace
		// trimming details. Empty = no rationale check.
		wantRationaleSubstring string
		wantErr                bool
	}{
		{
			name: "well-formed with roles",
			input: `# Recommendation

Recent changes touched the auth flow and added new test branches.

` + "```" + `
ateam report --roles code.bugs,test.recent
ateam report --roles code.bugs,test.recent --review
ateam all --roles code.bugs,test.recent
` + "```" + `

RECOMMENDED_ROLES: code.bugs,test.recent
`,
			wantRoles:              []string{"code.bugs", "test.recent"},
			wantRationaleSubstring: "Recent changes touched the auth flow",
		},
		{
			name: "empty marker means no work",
			input: `Everything is fresh.

RECOMMENDED_ROLES:
`,
			wantRoles:              nil,
			wantRationaleSubstring: "Everything is fresh.",
		},
		{
			name:    "missing marker is an error",
			input:   "I forgot to include the marker line.",
			wantErr: true,
		},
		{
			name: "trailing whitespace on marker is trimmed",
			input: `Rationale.

RECOMMENDED_ROLES:   code.bugs ,  test.gaps   ` + "\t" + `
`,
			wantRoles:              []string{"code.bugs", "test.gaps"},
			wantRationaleSubstring: "Rationale.",
		},
		{
			name: "duplicate marker lines: last wins",
			input: `Rationale.

RECOMMENDED_ROLES: stale.role

RECOMMENDED_ROLES: code.bugs
`,
			wantRoles:              []string{"code.bugs"},
			wantRationaleSubstring: "Rationale.",
		},
		{
			name: "single role",
			input: `Single recommendation.

RECOMMENDED_ROLES: code.recent
`,
			wantRoles: []string{"code.recent"},
		},
		{
			name: "empty entries between commas are dropped",
			input: `Rationale.

RECOMMENDED_ROLES: code.bugs,,test.recent,
`,
			wantRoles: []string{"code.bugs", "test.recent"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rationale, roles, err := parseAutoRolesOutput(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (rationale=%q roles=%v)", rationale, roles)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(roles) != len(tc.wantRoles) {
				t.Errorf("roles = %v, want %v", roles, tc.wantRoles)
			} else {
				for i, r := range roles {
					if r != tc.wantRoles[i] {
						t.Errorf("roles[%d] = %q, want %q", i, r, tc.wantRoles[i])
					}
				}
			}
			if tc.wantRationaleSubstring != "" && !strings.Contains(rationale, tc.wantRationaleSubstring) {
				t.Errorf("rationale missing substring %q; got:\n%s", tc.wantRationaleSubstring, rationale)
			}
		})
	}
}

// TestBuildAutoRolesContextHappyPath verifies that the pre-baked bundle
// includes every section header and reflects the seeded inputs (roles
// listing, a report file age, review content, execution_report path, and
// git log/diff for the base commit looked up from the DB).
func TestBuildAutoRolesContextHappyPath(t *testing.T) {
	base, projPath, env := setupTestProject(t)

	// initTestGitRepo gives us an initial empty commit; treat that hash as the
	// "prior review's base" so the bundle's diff window has something to show.
	initTestGitRepo(t, base)
	firstHash := gitHead(t, base)
	writeAndCommit(t, base, "src/feature.go", "package main\n// added since last review\n", "add feature")

	// Make the env operate from inside the git repo.
	if err := env.OverrideWorkDir(base); err != nil {
		t.Fatalf("OverrideWorkDir: %v", err)
	}

	// Seed a prior review run carrying firstHash as its GitStartHash.
	db, err := calldb.Open(env.ProjectDBPath())
	if err != nil {
		t.Fatalf("Open calldb: %v", err)
	}
	now := time.Now()
	rowID, err := db.InsertCall(&calldb.Call{
		ProjectID: env.ProjectID(), Action: "review", Role: "supervisor",
		StartedAt: now.Add(-30 * time.Minute), GitStartHash: firstHash,
	})
	if err != nil {
		t.Fatalf("InsertCall: %v", err)
	}
	if err := db.UpdateCall(rowID, &calldb.CallResult{EndedAt: now, DurationMS: 60000}); err != nil {
		t.Fatalf("UpdateCall: %v", err)
	}
	db.Close()

	// Seed a per-role report, a review, and an execution_report at their v1
	// shared/ locations — the paths auto-roles context reads post-migration.
	mustWrite(t, filepath.Join(projPath, ".ateam", "shared", "report", "code.bugs", "code.bugs.md"), "# Bug findings\n- Finding 1\n")
	mustWrite(t, filepath.Join(projPath, ".ateam", "shared", "review", "review.md"), "# Review\n\nSelected: code.bugs:Finding-1\nDeferred: nothing\n")
	mustWrite(t, filepath.Join(projPath, ".ateam", "runtime", "999", "execution_report.md"), "# Execution\n\nApplied Finding-1\n")

	got, err := buildAutoRolesContext(env)
	if err != nil {
		t.Fatalf("buildAutoRolesContext: %v", err)
	}

	wantSubstrings := []string{
		"### Roles configured for this project",
		"### Per-role report freshness",
		"### Latest review",
		"### Latest code-cycle execution report",
		"### Git base for diff",
		firstHash[:7],
		fmt.Sprintf("from review agent_exec %d", rowID),
		"### Git log since base",
		"### Git diff stat since base",
		"add feature",         // from the second commit
		"Selected: code.bugs", // from review.md
		"Applied Finding-1",   // from execution_report.md
		"code.bugs",           // from per-role report freshness
	}
	for _, s := range wantSubstrings {
		if !strings.Contains(got, s) {
			t.Errorf("output missing %q\n---\n%s", s, got)
		}
	}
}

// TestBuildAutoRolesContextFallback verifies that with no prior review row in
// the DB, the bundle falls back gracefully. With a single-commit repo
// HEAD~N is unavailable and root == HEAD, so the planner must see the initial
// commit directly (not an empty range or git error placeholders).
func TestBuildAutoRolesContextFallback(t *testing.T) {
	base, _, env := setupTestProject(t)
	initTestGitRepo(t, base) // one commit — HEAD~5 unavailable, root == HEAD
	if err := env.OverrideWorkDir(base); err != nil {
		t.Fatalf("OverrideWorkDir: %v", err)
	}

	got, err := buildAutoRolesContext(env)
	if err != nil {
		t.Fatalf("buildAutoRolesContext: %v", err)
	}
	if !strings.Contains(got, "single-commit repo") {
		t.Errorf("expected single-commit fallback explanation; got:\n%s", got)
	}
	// The git sections must use the direct-HEAD variant, not the range variant.
	if !strings.Contains(got, "### Git log (initial commit") {
		t.Errorf("expected direct-HEAD log section header; got:\n%s", got)
	}
	if !strings.Contains(got, "### Git diff stat (initial commit") {
		t.Errorf("expected direct-HEAD diff section header; got:\n%s", got)
	}
	// The sections must not contain git error placeholders.
	for _, section := range []string{"### Git log (initial commit", "### Git diff stat (initial commit"} {
		idx := strings.Index(got, section)
		if idx < 0 {
			continue // already reported above
		}
		excerpt := got[idx:]
		if nextSection := strings.Index(excerpt[len(section):], "###"); nextSection > 0 {
			excerpt = excerpt[:len(section)+nextSection]
		}
		if strings.Contains(excerpt, "_(git") && strings.Contains(excerpt, "failed:") {
			t.Errorf("section %q contains a git error placeholder:\n%s", section, excerpt)
		}
	}
	// The log section must contain actual commit content (the "init" message from
	// initTestGitRepo), proving the single-commit is shown, not an empty range.
	if !strings.Contains(got, "init") {
		t.Errorf("expected initial commit message in log section; got:\n%s", got)
	}
}

// --- test helpers ---

func writeAndCommit(t *testing.T, dir, relPath, content, msg string) {
	t.Helper()
	mustWrite(t, filepath.Join(dir, relPath), content)
	for _, args := range [][]string{
		{"add", relPath},
		{"commit", "-m", msg},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

func gitHead(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}
