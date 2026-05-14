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
	embedded := "supervisor/code_verify_prompt.md"
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
	if strings.Contains(result, "# Previous Report") {
		t.Error("previous report should be excluded when skipPreviousReport=true")
	}
	if strings.Contains(result, "previous findings here") {
		t.Error("previous report content should be excluded when skipPreviousReport=true")
	}
}

func TestAssembleRolePromptNoPreviousReportFile(t *testing.T) {
	base := t.TempDir()
	orgDir := filepath.Join(base, "org")
	projectDir := filepath.Join(base, "project")
	roleID := "security"

	setupMinimalRole(t, orgDir, projectDir, roleID)

	// No report.md exists — should succeed without "Previous Report"
	result, err := AssembleRolePrompt(orgDir, projectDir, roleID, base, "", ProjectInfoParams{}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "# Previous Report") {
		t.Error("should not contain Previous Report when no report file exists")
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

	// alpha: "on" → included; beta: "off" → excluded; gamma: "enabled" → included;
	// delta: "disabled" → excluded; epsilon: "weird_value" → excluded (allowlist);
	// zeta: not in configRoles → included (enabled by default).
	want := map[string]bool{"alpha": true, "gamma": true, "zeta": true}
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

// TestDottedRoleSelectsNewReportBase verifies that dotted-prefix role IDs
// (code.bugs, test.gaps, etc.) pick up new_report_base_prompt.md, while
// legacy non-dotted role IDs continue using report_base_prompt.md.
// TODO: fix this before v1 — once the new base is merged into the canonical
// file, this test can be deleted.
func TestDottedRoleSelectsNewReportBase(t *testing.T) {
	// Both bases live as embedded defaults; the test relies on the embedded
	// fallback (no on-disk base) so the selection branch is exercised.
	base := t.TempDir()
	orgDir := filepath.Join(base, "org")
	projectDir := filepath.Join(base, "project")

	setupMinimalRole(t, orgDir, projectDir, "code.bugs")
	setupMinimalRole(t, orgDir, projectDir, "security")

	dotted, err := AssembleRolePrompt(orgDir, projectDir, "code.bugs", base, "", ProjectInfoParams{}, true)
	if err != nil {
		t.Fatalf("AssembleRolePrompt(code.bugs): %v", err)
	}
	if !strings.Contains(dotted, "Project maturity") {
		t.Errorf("dotted role should pick new base containing 'Project maturity'; got:\n%s", dotted)
	}
	if !strings.Contains(dotted, "Run analytical tools when your role's findings ARE the tool's output") {
		t.Error("dotted role should pick new base containing analytical-tools bullet")
	}

	legacy, err := AssembleRolePrompt(orgDir, projectDir, "security", base, "", ProjectInfoParams{}, true)
	if err != nil {
		t.Fatalf("AssembleRolePrompt(security): %v", err)
	}
	if strings.Contains(legacy, "Project maturity") {
		t.Error("non-dotted role should use legacy base WITHOUT 'Project maturity' section")
	}
	if strings.Contains(legacy, "Run analytical tools when your role's findings ARE the tool's output") {
		t.Error("non-dotted role should use legacy base WITHOUT analytical-tools bullet")
	}
}

func TestResolveRoleListAllExpansionUsesAllowlist(t *testing.T) {
	configRoles := map[string]string{
		"security":   "on",
		"automation": "off",
	}
	// "all" should expand only to roles with status "on" or "enabled", plus
	// embedded roles not listed in configRoles (which default to enabled).
	roles, err := ResolveRoleList([]string{"all"}, configRoles, "", "")
	if err != nil {
		t.Fatalf("ResolveRoleList: %v", err)
	}
	for _, r := range roles {
		if r == "automation" {
			t.Errorf("'automation' (status 'off') should not appear in 'all' expansion")
		}
	}
	found := false
	for _, r := range roles {
		if r == "security" {
			found = true
		}
	}
	if !found {
		t.Errorf("'security' (status 'on') should appear in 'all' expansion")
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
			SourceDir:   "/projects/myapp",
			Role:        "role security",
		}
		got := FormatProjectInfo(p)

		for _, want := range []string{
			"# ATeam Project Context",
			"* project name: myapp",
			"* role: role security",
			"* project directory: . (working directory)",
			"* reports and reviews: .ateam",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %q in output:\n%s", want, got)
			}
		}
		// No absolute paths for SourceDir or ProjectDir
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
			SourceDir:   "/projects/mono/apps/myapp",
			GitRepoDir:  "/projects/mono",
			Role:        "the supervisor",
		}
		got := FormatProjectInfo(p)
		if !strings.Contains(got, "**IMPORTANT**") {
			t.Errorf("missing IMPORTANT scope warning in output:\n%s", got)
		}
		if !strings.Contains(got, "Limit your findings to the project directory") {
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
			SourceDir:   "/projects/myapp",
			GitRepoDir:  "/projects/myapp",
			Role:        "role testing",
		}
		got := FormatProjectInfo(p)
		if strings.Contains(got, "IMPORTANT") {
			t.Error("should not contain IMPORTANT when GitRepoDir == SourceDir")
		}
	})

	t.Run("with meta clean tree", func(t *testing.T) {
		p := ProjectInfoParams{
			OrgDir:      "/home/user/.ateamorg",
			ProjectDir:  "/projects/myapp/.ateam",
			ProjectName: "myapp",
			SourceDir:   "/projects/myapp",
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
			SourceDir:   "/projects/myapp",
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
