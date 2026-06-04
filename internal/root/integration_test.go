package root

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/config"
	"github.com/ateam/internal/promptdata"
)

// resolvedTempDir returns a t.TempDir() with symlinks resolved,
// so comparisons with paths from FindOrg/FindProject (which call
// filepath.EvalSymlinks) work correctly on macOS where /tmp -> /private/tmp.
func resolvedTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", dir, err)
	}
	return resolved
}

// TestIntegration_BasicProject simulates:
//
//	cd ~/projects/level1/myproj && ateam init
func TestIntegration_BasicProject(t *testing.T) {
	base := resolvedTempDir(t)

	orgDir, err := InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}

	projPath := filepath.Join(base, "level1", "myproj")
	if err := os.MkdirAll(projPath, 0755); err != nil {
		t.Fatal(err)
	}

	opts := InitProjectOpts{
		Name:            "level1/myproj",
		GitRepo:         ".",
		GitRemoteOrigin: "https://foobar/myproj.git",
		EnabledRoles:    promptdata.AllRoleIDs,
	}
	projDir, err := InitProject(projPath, orgDir, opts)
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}

	cfg, err := config.Load(projDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if cfg.Project.Name != "level1/myproj" {
		t.Errorf("name = %q, want %q", cfg.Project.Name, "level1/myproj")
	}
	if cfg.Git.Repo != "." {
		t.Errorf("git.repo = %q, want %q", cfg.Git.Repo, ".")
	}
	if cfg.Git.RemoteOriginURL != "https://foobar/myproj.git" {
		t.Errorf("git.remote = %q, want %q", cfg.Git.RemoteOriginURL, "https://foobar/myproj.git")
	}

	// Verify resolution: source = project path. GitRepoDir is no longer
	// derived from config.Git.Repo — it now comes from `git rev-parse` in
	// WorkDir at resolution time. The tmp fixture isn't a real git repo,
	// so GitRepoDir is "" here. Real-repo behavior is covered in gitutil tests.
	env := &ResolvedEnv{OrgDir: orgDir, ProjectDir: projDir}
	env.populateFromConfig(projDir, cfg)
	if env.SourceDir != projPath {
		t.Errorf("SourceDir = %q, want %q", env.SourceDir, projPath)
	}
	if env.GitRepoDir != "" {
		t.Errorf("GitRepoDir = %q, want \"\" (not a git repo)", env.GitRepoDir)
	}

	// Verify logs directories were created under .ateam/.
	for _, roleID := range promptdata.AllRoleIDs {
		logsDir := filepath.Join(projDir, "logs", "roles", roleID)
		if _, err := os.Stat(logsDir); err != nil {
			t.Errorf("logs dir missing for role %s: %v", roleID, err)
		}
	}
	// Verify .gitignore was created.
	gitignorePath := filepath.Join(projDir, ".gitignore")
	if data, err := os.ReadFile(gitignorePath); err != nil {
		t.Errorf(".gitignore missing: %v", err)
	} else {
		content := string(data)
		if !strings.Contains(content, "state.sqlite") {
			t.Error(".gitignore should contain state.sqlite")
		}
		if !strings.Contains(content, "logs/") {
			t.Error(".gitignore should contain logs/")
		}
	}

	// Verify ProjectDBPath and that the DB file was created during init.
	wantDBPath := filepath.Join(projDir, "state.sqlite")
	if env.ProjectDBPath() != wantDBPath {
		t.Errorf("ProjectDBPath() = %q, want %q", env.ProjectDBPath(), wantDBPath)
	}
	if _, err := os.Stat(wantDBPath); err != nil {
		t.Errorf("state.sqlite should exist after init: %v", err)
	}

	// FindOrg from project path should find the org.
	foundOrg, err := FindOrg(projPath)
	if err != nil {
		t.Fatalf("FindOrg from projPath: %v", err)
	}
	if foundOrg != orgDir {
		t.Errorf("FindOrg = %q, want %q", foundOrg, orgDir)
	}

	// FindProject from project path should find the project.
	foundProj, err := FindProject(projPath)
	if err != nil {
		t.Fatalf("FindProject from projPath: %v", err)
	}
	if foundProj != projDir {
		t.Errorf("FindProject = %q, want %q", foundProj, projDir)
	}
}

// TestIntegration_MonorepoSubdir simulates:
//
//	cd ~/projects/level1/myproj/subdir_abc && ateam init
func TestIntegration_MonorepoSubdir(t *testing.T) {
	base := resolvedTempDir(t)

	orgDir, err := InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}

	projPath := filepath.Join(base, "level1", "myproj", "subdir_abc")
	if err := os.MkdirAll(projPath, 0755); err != nil {
		t.Fatal(err)
	}

	opts := InitProjectOpts{
		Name:         "level1/myproj/subdir_abc",
		GitRepo:      "..",
		EnabledRoles: []string{"security"},
	}
	projDir, err := InitProject(projPath, orgDir, opts)
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}

	cfg, err := config.Load(projDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if cfg.Project.Name != "level1/myproj/subdir_abc" {
		t.Errorf("name = %q, want %q", cfg.Project.Name, "level1/myproj/subdir_abc")
	}
	if cfg.Git.Repo != ".." {
		t.Errorf("git.repo = %q, want %q", cfg.Git.Repo, "..")
	}

	// Verify resolution: source = subdir. GitRepoDir is now derived from
	// `git rev-parse` in WorkDir, not from config.Git.Repo. The tmp fixture
	// isn't a real git repo, so GitRepoDir is "" here.
	env := &ResolvedEnv{OrgDir: orgDir, ProjectDir: projDir}
	env.populateFromConfig(projDir, cfg)
	if env.SourceDir != projPath {
		t.Errorf("SourceDir = %q, want %q", env.SourceDir, projPath)
	}
	if env.GitRepoDir != "" {
		t.Errorf("GitRepoDir = %q, want \"\" (not a git repo)", env.GitRepoDir)
	}

	// Only "security" should be enabled.
	if cfg.Roles["security"] != config.RoleEnabled {
		t.Errorf("security role = %q, want %q", cfg.Roles["security"], config.RoleEnabled)
	}
	for id, status := range cfg.Roles {
		if id != "security" && status != config.RoleDisabled {
			t.Errorf("role %s = %q, want %q", id, status, config.RoleDisabled)
		}
	}

	// FindProject from a child directory should work.
	childDir := filepath.Join(projPath, "deep", "child")
	if err := os.MkdirAll(childDir, 0755); err != nil {
		t.Fatal(err)
	}
	foundProj, err := FindProject(childDir)
	if err != nil {
		t.Fatalf("FindProject from child: %v", err)
	}
	if foundProj != projDir {
		t.Errorf("FindProject = %q, want %q", foundProj, projDir)
	}
}

// TestIntegration_DuplicateProjectName verifies that creating two projects
// with the same name under the same org fails.
func TestIntegration_DuplicateProjectName(t *testing.T) {
	base := resolvedTempDir(t)

	orgDir, err := InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}

	proj1Path := filepath.Join(base, "proj1")
	if err := os.MkdirAll(proj1Path, 0755); err != nil {
		t.Fatal(err)
	}
	opts1 := InitProjectOpts{
		Name:         "duplicate-name",
		EnabledRoles: promptdata.AllRoleIDs,
	}
	if _, err := InitProject(proj1Path, orgDir, opts1); err != nil {
		t.Fatalf("first InitProject: %v", err)
	}

	proj2Path := filepath.Join(base, "proj2")
	if err := os.MkdirAll(proj2Path, 0755); err != nil {
		t.Fatal(err)
	}
	opts2 := InitProjectOpts{
		Name:         "duplicate-name",
		EnabledRoles: promptdata.AllRoleIDs,
	}
	_, err = InitProject(proj2Path, orgDir, opts2)
	if err == nil {
		t.Fatal("expected error for duplicate project name, got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestIntegration_MultipleProjects creates three independent projects and
// verifies each is discoverable from its own directory.
func TestIntegration_MultipleProjects(t *testing.T) {
	base := resolvedTempDir(t)

	orgDir, err := InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}

	names := []string{"frontend", "backend", "shared"}
	projDirs := make(map[string]string, len(names))

	for _, name := range names {
		p := filepath.Join(base, name)
		if err := os.MkdirAll(p, 0755); err != nil {
			t.Fatal(err)
		}
		opts := InitProjectOpts{
			Name:         name,
			EnabledRoles: promptdata.AllRoleIDs,
		}
		projDir, err := InitProject(p, orgDir, opts)
		if err != nil {
			t.Fatalf("InitProject(%s): %v", name, err)
		}
		projDirs[name] = projDir
	}

	for _, name := range names {
		p := filepath.Dir(projDirs[name]) // the parent dir containing .ateam
		foundProj, err := FindProject(p)
		if err != nil {
			t.Fatalf("FindProject for %s: %v", name, err)
		}
		if foundProj != projDirs[name] {
			t.Errorf("FindProject(%s) = %q, want %q", name, foundProj, projDirs[name])
		}

		cfg, err := config.Load(foundProj)
		if err != nil {
			t.Fatalf("config.Load for %s: %v", name, err)
		}
		if cfg.Project.Name != name {
			t.Errorf("project %s name = %q, want %q", name, cfg.Project.Name, name)
		}
	}
}

// TestIntegration_RelPathHelper verifies the RelPath helper on ResolvedEnv.
func TestIntegration_RelPathHelper(t *testing.T) {
	base := resolvedTempDir(t)

	orgDir, err := InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}

	projPath := filepath.Join(base, "services", "api")
	if err := os.MkdirAll(projPath, 0755); err != nil {
		t.Fatal(err)
	}

	opts := InitProjectOpts{
		Name:         "services/api",
		GitRepo:      ".",
		EnabledRoles: promptdata.AllRoleIDs,
	}
	projDir, err := InitProject(projPath, orgDir, opts)
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}

	cfg, _ := config.Load(projDir)
	env := &ResolvedEnv{OrgDir: orgDir, ProjectDir: projDir}
	env.populateFromConfig(projDir, cfg)

	if env.OrgRoot() != base {
		t.Errorf("OrgRoot() = %q, want %q", env.OrgRoot(), base)
	}
	if got := env.RelPath(projPath); got != "services/api" {
		t.Errorf("RelPath(projPath) = %q, want %q", got, "services/api")
	}
	if got := env.RelPath(env.SourceDir); got != "services/api" {
		t.Errorf("RelPath(SourceDir) = %q, want %q", got, "services/api")
	}
	if got := env.RelPath(""); got != "" {
		t.Errorf("RelPath(\"\") = %q, want %q", got, "")
	}
}

// TestIntegration_StatePathMethods verifies LogsDir, RuntimeDir, ProjectDBPath.
func TestIntegration_StatePathMethods(t *testing.T) {
	base := resolvedTempDir(t)

	orgDir, err := InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}

	projPath := filepath.Join(base, "myproj")
	if err := os.MkdirAll(projPath, 0755); err != nil {
		t.Fatal(err)
	}

	opts := InitProjectOpts{
		Name:         "myproj",
		EnabledRoles: promptdata.AllRoleIDs,
	}
	projDir, err := InitProject(projPath, orgDir, opts)
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}

	cfg, _ := config.Load(projDir)
	env := &ResolvedEnv{OrgDir: orgDir, ProjectDir: projDir}
	env.populateFromConfig(projDir, cfg)

	if got := env.LogsDir(42); got != filepath.Join(projDir, "logs", "42") {
		t.Errorf("LogsDir(42) = %q, want %q", got, filepath.Join(projDir, "logs", "42"))
	}
	if got := env.RuntimeDir(42); got != filepath.Join(projDir, "runtime", "42") {
		t.Errorf("RuntimeDir(42) = %q, want %q", got, filepath.Join(projDir, "runtime", "42"))
	}
	if got := env.ProjectDBPath(); got != filepath.Join(projDir, "state.sqlite") {
		t.Errorf("ProjectDBPath = %q, want %q", got, filepath.Join(projDir, "state.sqlite"))
	}
}

// TestIntegration_NestedProjectPaths verifies that a nested project path
// produces correct log paths under .ateam/.
func TestIntegration_NestedProjectPaths(t *testing.T) {
	base := resolvedTempDir(t)

	orgDir, err := InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}

	projPath := filepath.Join(base, "services", "api", "v2")
	if err := os.MkdirAll(projPath, 0755); err != nil {
		t.Fatal(err)
	}

	opts := InitProjectOpts{
		Name:         "services/api/v2",
		EnabledRoles: []string{"security"},
	}
	projDir, err := InitProject(projPath, orgDir, opts)
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}

	cfg, _ := config.Load(projDir)
	env := &ResolvedEnv{OrgDir: orgDir, ProjectDir: projDir}
	env.populateFromConfig(projDir, cfg)

	// Verify the per-exec_id logs path resolves under .ateam/logs/.
	wantLogsDir := filepath.Join(projDir, "logs", "1")
	if got := env.LogsDir(1); got != wantLogsDir {
		t.Errorf("LogsDir(1) = %q, want %q", got, wantLogsDir)
	}

	// Verify ProjectID still works for org context
	wantProjectID := config.PathToProjectID("services/api/v2")
	if wantProjectID != "services_api_v2" {
		t.Errorf("project ID = %q, want %q", wantProjectID, "services_api_v2")
	}
}

// TestIntegration_ResolveFromStateDir verifies that resolveProjectFromStateDir
// can find the project when cwd is inside .ateamorg/projects/<id>/.
func TestIntegration_ResolveFromStateDir(t *testing.T) {
	base := resolvedTempDir(t)

	orgDir, err := InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}

	cases := []struct {
		name    string
		relPath string // project relative path from orgRoot
	}{
		{"simple", "myproj"},
		{"nested", "services/api/v2"},
		{"underscores", "my_project"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			projPath := filepath.Join(base, filepath.FromSlash(tc.relPath))
			if err := os.MkdirAll(projPath, 0755); err != nil {
				t.Fatal(err)
			}

			opts := InitProjectOpts{
				Name:         tc.relPath,
				EnabledRoles: []string{"security"},
			}
			projDir, err := InitProject(projPath, orgDir, opts)
			if err != nil {
				t.Fatalf("InitProject: %v", err)
			}

			// Compute state dir and verify resolution from it.
			projectID := config.PathToProjectID(tc.relPath)
			stateDir := filepath.Join(orgDir, "projects", projectID)

			got, err := resolveProjectFromStateDir(orgDir, stateDir)
			if err != nil {
				t.Fatalf("resolveProjectFromStateDir(%q): %v", stateDir, err)
			}
			if got != projDir {
				t.Errorf("got %q, want %q", got, projDir)
			}

			// Also resolve from a subdirectory of the state dir.
			subDir := filepath.Join(stateDir, "roles", "security", "logs")
			if err := os.MkdirAll(subDir, 0755); err != nil {
				t.Fatal(err)
			}
			got, err = resolveProjectFromStateDir(orgDir, subDir)
			if err != nil {
				t.Fatalf("resolveProjectFromStateDir(%q): %v", subDir, err)
			}
			if got != projDir {
				t.Errorf("from subdir: got %q, want %q", got, projDir)
			}
		})
	}
}

// TestIntegration_WalkProjectsDiscovery verifies that WalkProjects discovers
// all registered projects via .ateamorg/projects/.
func TestIntegration_WalkProjectsDiscovery(t *testing.T) {
	base := resolvedTempDir(t)

	orgDir, err := InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}

	names := []string{"frontend", "backend", "shared"}
	for _, name := range names {
		p := filepath.Join(base, name)
		if err := os.MkdirAll(p, 0755); err != nil {
			t.Fatal(err)
		}
		opts := InitProjectOpts{
			Name:         name,
			EnabledRoles: promptdata.AllRoleIDs,
		}
		if _, err := InitProject(p, orgDir, opts); err != nil {
			t.Fatalf("InitProject(%s): %v", name, err)
		}
	}

	found := make(map[string]bool)
	err = WalkProjects(orgDir, func(p ProjectInfo) error {
		found[p.Config.Project.Name] = true
		return nil
	})
	if err != nil {
		t.Fatalf("WalkProjects: %v", err)
	}

	for _, name := range names {
		if !found[name] {
			t.Errorf("WalkProjects did not discover project %q", name)
		}
	}
	if len(found) != len(names) {
		t.Errorf("WalkProjects found %d projects, want %d", len(found), len(names))
	}
}
