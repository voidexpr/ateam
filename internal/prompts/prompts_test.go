package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/gitutil"
)

func TestReadWith3LevelFallback(t *testing.T) {
	base := t.TempDir()

	projectPath := filepath.Join(base, "project", "report_prompt.md")
	orgPath := filepath.Join(base, "org", "report_prompt.md")
	defaultPath := filepath.Join(base, "defaults", "report_prompt.md")

	_ = os.MkdirAll(filepath.Dir(projectPath), 0755)
	_ = os.MkdirAll(filepath.Dir(orgPath), 0755)
	_ = os.MkdirAll(filepath.Dir(defaultPath), 0755)

	// Only default exists
	_ = os.WriteFile(defaultPath, []byte("default"), 0644)
	got, err := readWith3LevelFallback(projectPath, orgPath, defaultPath, "", "test")
	if err != nil {
		t.Fatalf("default only: %v", err)
	}
	if got != "default" {
		t.Errorf("default only: got %q, want %q", got, "default")
	}

	// Org override exists
	_ = os.WriteFile(orgPath, []byte("org"), 0644)
	got, _ = readWith3LevelFallback(projectPath, orgPath, defaultPath, "", "test")
	if got != "org" {
		t.Errorf("org override: got %q, want %q", got, "org")
	}

	// Project override exists
	_ = os.WriteFile(projectPath, []byte("project"), 0644)
	got, _ = readWith3LevelFallback(projectPath, orgPath, defaultPath, "", "test")
	if got != "project" {
		t.Errorf("project override: got %q, want %q", got, "project")
	}

	// embeddedPath fallback: when none of the three filesystem paths exist
	// but a real embedded resource is referenced, the embedded content is
	// returned. Use a path known to exist in defaults/embed.go.
	missing := filepath.Join(base, "missing.md")
	embedded := "prompts/code_verify.prompt.md"
	got, err = readWith3LevelFallback(missing, missing, missing, embedded, "test")
	if err != nil {
		t.Fatalf("embedded fallback: %v", err)
	}
	if got == "" {
		t.Error("embedded fallback returned empty content")
	}
}

// setupMinimalRole creates the minimum structure for AssembleRolePrompt to work:
// a role prompt file at defaults level.
func setupMinimalRole(t *testing.T, orgDir, projectDir, roleID string) {
	t.Helper()
	roleDir := filepath.Join(orgDir, "defaults", "roles", roleID)
	_ = os.MkdirAll(roleDir, 0755)
	_ = os.WriteFile(filepath.Join(roleDir, ReportPromptFile), []byte("role prompt"), 0644)

	roleProjectDir := filepath.Join(projectDir, "roles", roleID)
	_ = os.MkdirAll(roleProjectDir, 0755)
}

func TestAssembleRolePromptIncludesPreviousReport(t *testing.T) {
	base := t.TempDir()
	orgDir := filepath.Join(base, "org")
	projectDir := filepath.Join(base, "project")
	roleID := "security"

	setupMinimalRole(t, orgDir, projectDir, roleID)

	reportPath := filepath.Join(projectDir, "roles", roleID, ReportFile)
	_ = os.WriteFile(reportPath, []byte("previous findings here"), 0644)

	result, err := AssembleRolePrompt(orgDir, projectDir, roleID, base, "", ProjectInfoParams{}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "# Previous Report") {
		t.Error("expected '# Previous Report' section in prompt")
	}
	if !strings.Contains(result, "It might be outdated but it will give you some context") {
		t.Error("expected context instructions in Previous Report header")
	}
	if !strings.Contains(result, "previous findings here") {
		t.Errorf("expected previous report content in prompt, got:\n%s", result)
	}
}

func TestAssembleRolePromptSkipPreviousReport(t *testing.T) {
	base := t.TempDir()
	orgDir := filepath.Join(base, "org")
	projectDir := filepath.Join(base, "project")
	roleID := "security"

	setupMinimalRole(t, orgDir, projectDir, roleID)

	reportPath := filepath.Join(projectDir, "roles", roleID, ReportFile)
	_ = os.WriteFile(reportPath, []byte("previous findings here"), 0644)

	result, err := AssembleRolePrompt(orgDir, projectDir, roleID, base, "", ProjectInfoParams{}, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Match the section header, not the backtick-quoted reference that
	// may appear in the base prompt's merging instructions.
	if strings.Contains(result, "# Previous Report\n") {
		t.Error("previous report section should be excluded when skipPreviousReport=true")
	}
	if strings.Contains(result, "previous findings here") {
		t.Error("previous report content should be excluded when skipPreviousReport=true")
	}
	if strings.Contains(result, "# Prior Report Status") {
		t.Error("prior-report-status notice should be excluded when skipPreviousReport=true")
	}
}

func TestAssembleRolePromptNoPreviousReportFile(t *testing.T) {
	base := t.TempDir()
	orgDir := filepath.Join(base, "org")
	projectDir := filepath.Join(base, "project")
	roleID := "security"

	setupMinimalRole(t, orgDir, projectDir, roleID)

	// No report.md exists — should succeed with a "Prior Report Status" notice
	// instead of a "# Previous Report" section, so the agent knows the absence
	// is intentional and doesn't snoop disk for one.
	result, err := AssembleRolePrompt(orgDir, projectDir, roleID, base, "", ProjectInfoParams{}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "# Previous Report\n") {
		t.Error("should not contain '# Previous Report' section when no report file exists")
	}
	if !strings.Contains(result, "# Prior Report Status") {
		t.Errorf("expected '# Prior Report Status' notice when no prior report, got:\n%s", result)
	}
	if !strings.Contains(result, "No prior report exists for this role") {
		t.Error("expected explicit absence notice in Prior Report Status block")
	}
}

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

func TestDotNamespacedRole(t *testing.T) {
	base := t.TempDir()
	orgDir := filepath.Join(base, "org")
	projectDir := filepath.Join(base, "project")
	roleID := "code.small"

	// Filesystem discovery: dot in directory name works with flat single-level layout.
	roleDir := filepath.Join(projectDir, "roles", roleID)
	if err := os.MkdirAll(roleDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(roleDir, ReportPromptFile), []byte("dotted role prompt"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

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

	// Prompt assembly: AssembleRolePrompt finds the dotted role on disk.
	result, err := AssembleRolePrompt(orgDir, projectDir, roleID, base, "", ProjectInfoParams{}, true)
	if err != nil {
		t.Fatalf("AssembleRolePrompt(%q): %v", roleID, err)
	}
	if !strings.Contains(result, "dotted role prompt") {
		t.Errorf("expected role prompt content in assembled prompt, got:\n%s", result)
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

func TestReadWith3LevelFallbackNoneExist(t *testing.T) {
	base := t.TempDir()
	_, err := readWith3LevelFallback(
		filepath.Join(base, "a"),
		filepath.Join(base, "b"),
		filepath.Join(base, "c"),
		"",
		"test",
	)
	if err == nil {
		t.Fatal("expected error when no files exist")
	}
}

func TestResolveValueLiteral(t *testing.T) {
	got, err := ResolveValue("hello world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "hello world" {
		t.Errorf("got %q, want %q", got, "hello world")
	}
}

func TestResolveValueFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p.md")
	if err := os.WriteFile(path, []byte("from file"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveValue("@" + path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "from file" {
		t.Errorf("got %q, want %q", got, "from file")
	}
}

func TestAssembleCodeVerifyPrompt(t *testing.T) {
	base := t.TempDir()
	orgDir := filepath.Join(base, "org")
	projectDir := filepath.Join(base, "project")

	// Place stub at org-defaults/supervisor level so the 3-level fallback finds it.
	supervisorDir := filepath.Join(orgDir, "defaults", "supervisor")
	if err := os.MkdirAll(supervisorDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	stubBody := "verify the code changes carefully"
	if err := os.WriteFile(filepath.Join(supervisorDir, CodeVerifyPromptFile), []byte(stubBody), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	pinfo := ProjectInfoParams{
		ProjectName: "myapp",
		Role:        "the supervisor",
	}

	t.Run("without extra prompt", func(t *testing.T) {
		result, err := AssembleCodeVerifyPrompt(orgDir, projectDir, pinfo, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result, "# ATeam Project Context") {
			t.Error("missing project-info header")
		}
		if !strings.Contains(result, stubBody) {
			t.Errorf("missing prompt body %q in:\n%s", stubBody, result)
		}
		if strings.Contains(result, "# Additional Instructions") {
			t.Error("unexpected Additional Instructions section when extraPrompt is empty")
		}
	})

	t.Run("with extra prompt appended last", func(t *testing.T) {
		extra := "run the full test suite first"
		result, err := AssembleCodeVerifyPrompt(orgDir, projectDir, pinfo, extra)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result, "# ATeam Project Context") {
			t.Error("missing project-info header")
		}
		if !strings.Contains(result, stubBody) {
			t.Errorf("missing prompt body %q", stubBody)
		}
		if !strings.Contains(result, "# Additional Instructions") {
			t.Error("missing Additional Instructions section")
		}
		if !strings.Contains(result, extra) {
			t.Errorf("missing extra prompt content %q", extra)
		}
		// Project info must appear before the prompt body, which must appear before extra.
		headerIdx := strings.Index(result, "# ATeam Project Context")
		bodyIdx := strings.Index(result, stubBody)
		extraIdx := strings.Index(result, "# Additional Instructions")
		if headerIdx >= bodyIdx || bodyIdx >= extraIdx {
			t.Errorf("sections out of order: header=%d body=%d extra=%d", headerIdx, bodyIdx, extraIdx)
		}
	})
}

func TestAssembleCodeManagementPrompt(t *testing.T) {
	base := t.TempDir()
	orgDir := filepath.Join(base, "org")
	projectDir := filepath.Join(base, "project")
	sourceDir := filepath.Join(base, "src")

	supervisorDir := filepath.Join(orgDir, "defaults", "supervisor")
	if err := os.MkdirAll(supervisorDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	stubBody := "manage the code changes for {{SOURCE_DIR}}"
	if err := os.WriteFile(filepath.Join(supervisorDir, CodeManagementPromptFile), []byte(stubBody), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	pinfo := ProjectInfoParams{
		ProjectName: "myapp",
		Role:        "the supervisor",
	}
	reviewContent := "task list from supervisor review"

	t.Run("without extra prompt", func(t *testing.T) {
		result, err := AssembleCodeManagementPrompt(orgDir, projectDir, sourceDir, pinfo, reviewContent, "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == "" {
			t.Fatal("expected non-empty result")
		}
		if !strings.Contains(result, "# ATeam Project Context") {
			t.Error("missing project-info header")
		}
		// {{SOURCE_DIR}} should be substituted to "."
		if !strings.Contains(result, "manage the code changes for .") {
			t.Errorf("expected {{SOURCE_DIR}} substitution in body, got:\n%s", result)
		}
		if !strings.Contains(result, "# Review") {
			t.Error("missing Review section")
		}
		if !strings.Contains(result, reviewContent) {
			t.Errorf("missing review content %q", reviewContent)
		}
		if strings.Contains(result, "# Additional Instructions") {
			t.Error("unexpected Additional Instructions section when extraPrompt is empty")
		}
	})

	t.Run("with custom prompt overrides fallback", func(t *testing.T) {
		custom := "custom management prompt for {{SOURCE_DIR}}"
		result, err := AssembleCodeManagementPrompt(orgDir, projectDir, sourceDir, pinfo, reviewContent, custom, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result, "custom management prompt for .") {
			t.Errorf("expected custom prompt with substitution, got:\n%s", result)
		}
		if strings.Contains(result, stubBody) || strings.Contains(result, "manage the code changes for .") {
			t.Errorf("custom prompt should replace stub body, got:\n%s", result)
		}
	})

	t.Run("with extra prompt appended last", func(t *testing.T) {
		extra := "follow extra rules"
		result, err := AssembleCodeManagementPrompt(orgDir, projectDir, sourceDir, pinfo, reviewContent, "", extra)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result, "# Additional Instructions") {
			t.Error("missing Additional Instructions section")
		}
		if !strings.Contains(result, extra) {
			t.Errorf("missing extra prompt content %q", extra)
		}
		// Section order: project info → body → review → extra
		headerIdx := strings.Index(result, "# ATeam Project Context")
		bodyIdx := strings.Index(result, "manage the code changes for .")
		reviewIdx := strings.Index(result, "# Review")
		extraIdx := strings.Index(result, "# Additional Instructions")
		if headerIdx >= bodyIdx || bodyIdx >= reviewIdx || reviewIdx >= extraIdx {
			t.Errorf("sections out of order: header=%d body=%d review=%d extra=%d",
				headerIdx, bodyIdx, reviewIdx, extraIdx)
		}
	})
}

func TestAssembleAutoRolesPrompt(t *testing.T) {
	base := t.TempDir()
	orgDir := filepath.Join(base, "org")
	projectDir := filepath.Join(base, "project")

	supervisorDir := filepath.Join(orgDir, "defaults", "supervisor")
	if err := os.MkdirAll(supervisorDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	stubBody := "recommend which roles to run, ending with " + AutoRolesMarker
	if err := os.WriteFile(filepath.Join(supervisorDir, ReportAutoRolesPromptFile), []byte(stubBody), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	pinfo := ProjectInfoParams{
		ProjectName: "myapp",
		Role:        "the supervisor",
	}

	result, err := AssembleAutoRolesPrompt(orgDir, projectDir, pinfo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	if !strings.Contains(result, "# ATeam Project Context") {
		t.Error("missing project-info header")
	}
	if !strings.Contains(result, stubBody) {
		t.Errorf("missing prompt body %q in:\n%s", stubBody, result)
	}
	// Project info must appear before the prompt body.
	headerIdx := strings.Index(result, "# ATeam Project Context")
	bodyIdx := strings.Index(result, stubBody)
	if headerIdx < 0 || bodyIdx < 0 || headerIdx >= bodyIdx {
		t.Errorf("sections out of order: header=%d body=%d", headerIdx, bodyIdx)
	}
}

func TestResolveValueStdin(t *testing.T) {
	for _, sentinel := range []string{"-", "@-"} {
		t.Run(sentinel, func(t *testing.T) {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			origStdin := os.Stdin
			os.Stdin = r
			t.Cleanup(func() { os.Stdin = origStdin })

			go func() {
				_, _ = w.Write([]byte("from stdin"))
				_ = w.Close()
			}()

			got, err := ResolveValue(sentinel)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != "from stdin" {
				t.Errorf("got %q, want %q", got, "from stdin")
			}
		})
	}
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
