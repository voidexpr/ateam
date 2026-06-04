package promptdata

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/gitutil"
)

func TestEnabledRoleIDsAllowlist(t *testing.T) {
	configRoles := map[string]string{
		"alpha":   "on",
		"beta":    "off",
		"gamma":   "enabled",
		"delta":   "disabled",
		"epsilon": "weird_value",
	}
	allKnown := []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta"}

	got := enabledRoleIDs(configRoles, allKnown)

	// Only roles explicitly listed as "on" or "enabled" are included.
	// alpha: "on" → included; gamma: "enabled" → included.
	// beta/delta: explicit off; epsilon: unrecognized status; zeta: unlisted —
	// all excluded.
	want := map[string]bool{"alpha": true, "gamma": true}
	if len(got) != len(want) {
		t.Fatalf("enabledRoleIDs returned %v, want keys %v", got, want)
	}
	for _, id := range got {
		if !want[id] {
			t.Errorf("enabledRoleIDs included unexpected role %q", id)
		}
	}
}

func TestEnabledRoleIDsNilConfig(t *testing.T) {
	allKnown := []string{"a", "b", "c"}
	got := enabledRoleIDs(nil, allKnown)
	if len(got) != len(allKnown) {
		t.Errorf("enabledRoleIDs(nil) = %v, want all %v", got, allKnown)
	}
}

// TestDotNamespacedRole exercises the IsValidRole / AllKnownRoleIDs /
// ResolveRoleList chain for role IDs containing dots (e.g. "code.small").
// Validity is sourced from configRoles since the v1 path scanner
// (scanV1Roles) doesn't run against bare temp dirs — it expects
// `<dir>/prompts/report/<id>.prompt.md`. The dotted-name handling is the
// same regardless of where the role is registered, so configRoles alone
// is sufficient to verify the round trip.
func TestDotNamespacedRole(t *testing.T) {
	base := t.TempDir()
	orgDir := filepath.Join(base, "org")
	projectDir := filepath.Join(base, "project")
	roleID := "code.small"

	configRoles := map[string]string{
		"code.small": "on",
		"security":   "on",
	}

	if !IsValidRole(roleID, configRoles, projectDir, orgDir) {
		t.Errorf("IsValidRole(%q): want true, got false", roleID)
	}

	all := AllKnownRoleIDs(configRoles, projectDir, orgDir)
	found := false
	for _, id := range all {
		if id == roleID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("AllKnownRoleIDs missing %q, got %v", roleID, all)
	}

	resolved, err := ResolveRoleList([]string{"code.small", "security"}, configRoles, projectDir, orgDir)
	if err != nil {
		t.Fatalf("ResolveRoleList: %v", err)
	}
	if len(resolved) != 2 || resolved[0] != "code.small" || resolved[1] != "security" {
		t.Errorf("ResolveRoleList = %v, want [code.small security]", resolved)
	}
}

func TestResolveRoleListAllExpansionUsesAllowlist(t *testing.T) {
	configRoles := map[string]string{
		"security":   "on",
		"automation": "off",
	}
	// "all" expands only to roles explicitly listed as "on" or "enabled".
	// Built-in roles not present in configRoles (e.g. "code.bugs") and roles
	// listed as "off" are both excluded.
	roles, err := ResolveRoleList([]string{"all"}, configRoles, "", "")
	if err != nil {
		t.Fatalf("ResolveRoleList: %v", err)
	}
	for _, r := range roles {
		if r != "security" {
			t.Errorf("unexpected role %q in 'all' expansion; only 'security' should be included", r)
		}
	}
	if len(roles) != 1 || roles[0] != "security" {
		t.Errorf("expected exactly [security] in 'all' expansion, got %v", roles)
	}
}

func TestFormatProjectInfo(t *testing.T) {
	t.Run("empty role returns empty", func(t *testing.T) {
		got := FormatProjectInfo(ProjectInfoParams{})
		if got != "" {
			t.Errorf("expected empty string for zero-value params, got %q", got)
		}
	})

	t.Run("basic fields use relative paths", func(t *testing.T) {
		p := ProjectInfoParams{
			OrgDir:      "/home/user/.ateamorg",
			ProjectDir:  "/projects/myapp/.ateam",
			ProjectName: "myapp",
			WorkDir:     "/projects/myapp",
			Role:        "role security",
		}
		got := FormatProjectInfo(p)

		for _, want := range []string{
			"# ATeam Project Context",
			"* project name: myapp",
			"* role: role security",
			"* working directory: .",
			"* reports and reviews: .ateam",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %q in output:\n%s", want, got)
			}
		}
		// No absolute paths for WorkDir or ProjectDir
		if strings.Contains(got, "/projects/myapp") {
			t.Errorf("should not contain absolute project path in output:\n%s", got)
		}
		// GitRepoDir not set → no scope warning
		if strings.Contains(got, "IMPORTANT") {
			t.Error("should not contain IMPORTANT when GitRepoDir is empty")
		}
	})

	t.Run("git repo dir differs uses relative path", func(t *testing.T) {
		p := ProjectInfoParams{
			OrgDir:      "/home/user/.ateamorg",
			ProjectDir:  "/projects/mono/apps/myapp/.ateam",
			ProjectName: "myapp",
			WorkDir:     "/projects/mono/apps/myapp",
			GitRepoDir:  "/projects/mono",
			Role:        "the supervisor",
		}
		got := FormatProjectInfo(p)
		if !strings.Contains(got, "**IMPORTANT**") {
			t.Errorf("missing IMPORTANT scope warning in output:\n%s", got)
		}
		if !strings.Contains(got, "Limit your findings to the working directory") {
			t.Errorf("missing scope instruction in output:\n%s", got)
		}
		// Should use relative path for git repo root, not absolute
		if !strings.Contains(got, "../..") {
			t.Errorf("expected relative git repo path (../..) in output:\n%s", got)
		}
		if strings.Contains(got, "/projects/mono/apps/myapp") {
			t.Errorf("should not contain absolute path in scope warning:\n%s", got)
		}
	})

	t.Run("git repo dir same as source dir", func(t *testing.T) {
		p := ProjectInfoParams{
			OrgDir:      "/home/user/.ateamorg",
			ProjectDir:  "/projects/myapp/.ateam",
			ProjectName: "myapp",
			WorkDir:     "/projects/myapp",
			GitRepoDir:  "/projects/myapp",
			Role:        "role testing",
		}
		got := FormatProjectInfo(p)
		if strings.Contains(got, "IMPORTANT") {
			t.Error("should not contain IMPORTANT when GitRepoDir == WorkDir")
		}
	})

	t.Run("with meta clean tree", func(t *testing.T) {
		p := ProjectInfoParams{
			OrgDir:      "/home/user/.ateamorg",
			ProjectDir:  "/projects/myapp/.ateam",
			ProjectName: "myapp",
			WorkDir:     "/projects/myapp",
			Role:        "role security",
			Meta: &gitutil.ProjectMeta{
				CommitHash:    "abcdef1234567890abcdef",
				CommitDate:    "2026-03-20_10-00-00",
				CommitMessage: "fix the widget",
			},
		}
		got := FormatProjectInfo(p)

		for _, want := range []string{
			"* timestamp:",
			"* last commit: abcdef123456 - 2026-03-20_10-00-00 - \"fix the widget\"",
			"* working tree: clean",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %q in output:\n%s", want, got)
			}
		}
		// Hash should be truncated to 12 chars
		if strings.Contains(got, "abcdef1234567890") {
			t.Error("commit hash should be truncated to 12 characters")
		}
	})

	t.Run("with meta uncommitted changes", func(t *testing.T) {
		p := ProjectInfoParams{
			OrgDir:      "/home/user/.ateamorg",
			ProjectDir:  "/projects/myapp/.ateam",
			ProjectName: "myapp",
			WorkDir:     "/projects/myapp",
			Role:        "role security",
			Meta: &gitutil.ProjectMeta{
				CommitHash:    "abc123",
				CommitDate:    "2026-03-20_10-00-00",
				CommitMessage: "initial",
				Uncommitted:   []string{"README.md", "main.go"},
			},
		}
		got := FormatProjectInfo(p)

		for _, want := range []string{
			"* uncommitted changes: 2 file(s)",
			"* `README.md`",
			"* `main.go`",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %q in output:\n%s", want, got)
			}
		}
		if strings.Contains(got, "working tree: clean") {
			t.Error("should not say 'clean' when there are uncommitted changes")
		}
		// Short hash should not be truncated
		if !strings.Contains(got, "abc123") {
			t.Error("short hash should be kept as-is")
		}
	})

	t.Run("quick orientation appended when non-empty", func(t *testing.T) {
		p := ProjectInfoParams{
			OrgDir:           "/home/user/.ateamorg",
			ProjectDir:       "/projects/myapp/.ateam",
			ProjectName:      "myapp",
			WorkDir:          "/projects/myapp",
			Role:             "role security",
			QuickOrientation: "## Quick orientation (auto-generated; do not re-derive)\n\nUniversal:\n* Top-level: cmd, internal, README.md\n",
		}
		got := FormatProjectInfo(p)
		if !strings.Contains(got, "## Quick orientation") {
			t.Errorf("missing quick-orientation block:\n%s", got)
		}
		// Block must appear AFTER the existing project-info bullets so the
		// stable preamble stays cache-friendly.
		projectIdx := strings.Index(got, "# ATeam Project Context")
		orientIdx := strings.Index(got, "## Quick orientation")
		if projectIdx < 0 || orientIdx < 0 || orientIdx < projectIdx {
			t.Errorf("quick orientation should come after the project-info header; project=%d orient=%d", projectIdx, orientIdx)
		}
	})

	t.Run("quick orientation omitted when empty", func(t *testing.T) {
		p := ProjectInfoParams{
			ProjectName: "myapp",
			WorkDir:     "/projects/myapp",
			Role:        "role security",
		}
		got := FormatProjectInfo(p)
		if strings.Contains(got, "Quick orientation") {
			t.Errorf("quick-orientation block should be absent when QuickOrientation is empty:\n%s", got)
		}
	})

	t.Run("quick orientation suppresses duplicated meta lines", func(t *testing.T) {
		// When orientation is on, the existing block must NOT also emit
		// `* last commit:` / `* working tree:` / `* uncommitted changes:` —
		// those move into the orientation block. Timestamp stays here because
		// it reflects the run start, not the commit time.
		p := ProjectInfoParams{
			ProjectName: "myapp",
			WorkDir:     "/projects/myapp",
			Role:        "role security",
			Meta: &gitutil.ProjectMeta{
				CommitHash:    "abc123def456",
				CommitDate:    "2026-05-18_10-00-00",
				CommitMessage: "subject",
				Uncommitted:   []string{" M src/foo.go", "?? new.go"},
			},
			QuickOrientation: "## Quick orientation (auto-generated; do not re-derive)\n\nUniversal:\n* Working tree: dirty (2 files)\n    M src/foo.go\n    ?? new.go\n* Last commit: abc123def456 (2026-05-18_10-00-00) — subject\n",
		}
		got := FormatProjectInfo(p)
		if strings.Contains(got, "* last commit:") {
			t.Errorf("`* last commit:` should be suppressed when orientation is on:\n%s", got)
		}
		if strings.Contains(got, "* working tree:") {
			t.Errorf("`* working tree:` should be suppressed when orientation is on:\n%s", got)
		}
		if strings.Contains(got, "* uncommitted changes:") {
			t.Errorf("`* uncommitted changes:` should be suppressed when orientation is on:\n%s", got)
		}
		if !strings.Contains(got, "* timestamp:") {
			t.Errorf("timestamp should still be emitted (reflects run time):\n%s", got)
		}
		if !strings.Contains(got, "## Quick orientation") {
			t.Errorf("orientation block missing:\n%s", got)
		}
	})
}

func TestParsePromptFrontmatter(t *testing.T) {
	cases := []struct {
		name     string
		content  string
		wantMeta RoleMetadata
		wantBody string
	}{
		{
			name:     "description only",
			content:  "---\ndescription: Hello world\n---\n# Body\n",
			wantMeta: RoleMetadata{Description: "Hello world"},
			wantBody: "# Body\n",
		},
		{
			name:     "deprecated and legacy flags",
			content:  "---\ndescription: Old role\ndeprecated: true\nlegacy: true\n---\nbody",
			wantMeta: RoleMetadata{Description: "Old role", Deprecated: true, Legacy: true},
			wantBody: "body",
		},
		{
			name:     "deprecated without legacy",
			content:  "---\ndescription: Still listed\ndeprecated: true\n---\nbody",
			wantMeta: RoleMetadata{Description: "Still listed", Deprecated: true},
			wantBody: "body",
		},
		{
			name:     "no frontmatter",
			content:  "# Just a body\n",
			wantMeta: RoleMetadata{},
			wantBody: "# Just a body\n",
		},
		{
			name:     "false flags are not set",
			content:  "---\ndescription: New\ndeprecated: false\nlegacy: false\n---\nbody",
			wantMeta: RoleMetadata{Description: "New"},
			wantBody: "body",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			meta, body := ParsePromptFrontmatter(tc.content)
			if meta != tc.wantMeta {
				t.Errorf("meta = %+v, want %+v", meta, tc.wantMeta)
			}
			if body != tc.wantBody {
				t.Errorf("body = %q, want %q", body, tc.wantBody)
			}
		})
	}
}

func TestRoleMetaLegacyFlag(t *testing.T) {
	// security is an embedded legacy role marked deprecated + legacy.
	meta := RoleMeta("security")
	if !meta.Legacy {
		t.Errorf("security should be marked legacy; meta = %+v", meta)
	}
	if !meta.Deprecated {
		t.Errorf("security should be marked deprecated; meta = %+v", meta)
	}
	// code.bugs is a dotted current role — neither flag should be set.
	meta = RoleMeta("code.bugs")
	if meta.Legacy || meta.Deprecated {
		t.Errorf("code.bugs should not be legacy/deprecated; meta = %+v", meta)
	}
}

func TestFormatProjectInfoShowsRelativeAteamPath(t *testing.T) {
	t.Run("ateam under cwd renders as .ateam", func(t *testing.T) {
		p := ProjectInfoParams{
			OrgDir:      "/home/user/.ateamorg",
			ProjectDir:  "/projects/myapp/.ateam",
			WorkDir:     "/projects/myapp",
			ProjectName: "myapp",
			Role:        "role security",
		}
		got := FormatProjectInfo(p)
		if !strings.Contains(got, "* reports and reviews: .ateam") {
			t.Errorf("expected '.ateam' rendering when ProjectDir is directly under WorkDir, got:\n%s", got)
		}
	})

	t.Run("ateam in sibling tree renders as ../path", func(t *testing.T) {
		p := ProjectInfoParams{
			OrgDir:      "/home/user/.ateamorg",
			ProjectDir:  "/projects/main/.ateam",
			WorkDir:     "/projects/worktree-feat",
			ProjectName: "myapp",
			Role:        "role security",
		}
		got := FormatProjectInfo(p)
		if !strings.Contains(got, "../main/.ateam") {
			t.Errorf("expected '../main/.ateam' relative rendering, got:\n%s", got)
		}
		// Must not lie by saying ".ateam" when the .ateam is elsewhere.
		if strings.Contains(got, "* reports and reviews: .ateam\n") {
			t.Errorf("must not claim '.ateam' when ProjectDir is not under WorkDir, got:\n%s", got)
		}
	})
}
