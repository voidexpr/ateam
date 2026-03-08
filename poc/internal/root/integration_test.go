package root

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam-poc/internal/config"
	"github.com/ateam-poc/internal/prompts"
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
		Source:          ".",
		GitRepo:         ".",
		GitRemoteOrigin: "https://foobar/myproj.git",
		EnabledAgents:   prompts.AllAgentIDs,
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
	if cfg.Project.Source != "." {
		t.Errorf("source = %q, want %q", cfg.Project.Source, ".")
	}
	if cfg.Git.Repo != "." {
		t.Errorf("git.repo = %q, want %q", cfg.Git.Repo, ".")
	}
	if cfg.Git.RemoteOriginURL != "https://foobar/myproj.git" {
		t.Errorf("git.remote = %q, want %q", cfg.Git.RemoteOriginURL, "https://foobar/myproj.git")
	}

	// Verify resolution: source and git should resolve to the project path.
	env := &ResolvedEnv{OrgDir: orgDir, ProjectDir: projDir}
	env.populateFromConfig(projDir, cfg)
	if env.SourceDir != projPath {
		t.Errorf("SourceDir = %q, want %q", env.SourceDir, projPath)
	}
	if env.GitRepoDir != projPath {
		t.Errorf("GitRepoDir = %q, want %q", env.GitRepoDir, projPath)
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
		Name:          "level1/myproj/subdir_abc",
		Source:        ".",
		GitRepo:       "..",
		EnabledAgents: []string{"security"},
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
	if cfg.Project.Source != "." {
		t.Errorf("source = %q, want %q", cfg.Project.Source, ".")
	}
	if cfg.Git.Repo != ".." {
		t.Errorf("git.repo = %q, want %q", cfg.Git.Repo, "..")
	}

	// Verify resolution: source = subdir, git repo = parent dir.
	env := &ResolvedEnv{OrgDir: orgDir, ProjectDir: projDir}
	env.populateFromConfig(projDir, cfg)
	if env.SourceDir != projPath {
		t.Errorf("SourceDir = %q, want %q", env.SourceDir, projPath)
	}
	wantGit := filepath.Join(base, "level1", "myproj")
	if env.GitRepoDir != wantGit {
		t.Errorf("GitRepoDir = %q, want %q", env.GitRepoDir, wantGit)
	}

	// Only "security" should be enabled.
	if cfg.Agents["security"] != "enabled" {
		t.Errorf("security agent = %q, want %q", cfg.Agents["security"], "enabled")
	}
	for id, status := range cfg.Agents {
		if id != "security" && status != "disabled" {
			t.Errorf("agent %s = %q, want %q", id, status, "disabled")
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

// TestIntegration_ExternalProject simulates:
//
//	cd ~/projects/ateam_projects && ateam init --name myproj --source ~/projects/level1/myproj
func TestIntegration_ExternalProject(t *testing.T) {
	base := resolvedTempDir(t)

	orgDir, err := InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}

	externalSource := filepath.Join(base, "level1", "myproj")
	if err := os.MkdirAll(externalSource, 0755); err != nil {
		t.Fatal(err)
	}

	ateamProjectsPath := filepath.Join(base, "ateam_projects")
	if err := os.MkdirAll(ateamProjectsPath, 0755); err != nil {
		t.Fatal(err)
	}

	// Source relative from ateam_projects to level1/myproj
	relSource := "../level1/myproj"

	opts := InitProjectOpts{
		Name:          "myproj",
		Source:        relSource,
		EnabledAgents: prompts.AllAgentIDs,
	}
	projDir, err := InitProject(ateamProjectsPath, orgDir, opts)
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}

	cfg, err := config.Load(projDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if cfg.Project.Name != "myproj" {
		t.Errorf("name = %q, want %q", cfg.Project.Name, "myproj")
	}
	if cfg.Project.Source != relSource {
		t.Errorf("source = %q, want %q", cfg.Project.Source, relSource)
	}

	// Verify resolution: source should resolve to the external path.
	env := &ResolvedEnv{OrgDir: orgDir, ProjectDir: projDir}
	env.populateFromConfig(projDir, cfg)
	if env.SourceDir != externalSource {
		t.Errorf("SourceDir = %q, want %q", env.SourceDir, externalSource)
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
		Name:          "duplicate-name",
		Source:        ".",
		EnabledAgents: prompts.AllAgentIDs,
	}
	if _, err := InitProject(proj1Path, orgDir, opts1); err != nil {
		t.Fatalf("first InitProject: %v", err)
	}

	proj2Path := filepath.Join(base, "proj2")
	if err := os.MkdirAll(proj2Path, 0755); err != nil {
		t.Fatal(err)
	}
	opts2 := InitProjectOpts{
		Name:          "duplicate-name",
		Source:        ".",
		EnabledAgents: prompts.AllAgentIDs,
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
			Name:          name,
			Source:        ".",
			EnabledAgents: prompts.AllAgentIDs,
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

// TestIntegration_3LevelPromptFallback tests the prompt cascade:
// org defaults -> org override -> project override.
func TestIntegration_3LevelPromptFallback(t *testing.T) {
	if len(prompts.AllAgentIDs) == 0 {
		t.Skip("no embedded agents found")
	}

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
		Name:          "myproj",
		Source:        ".",
		EnabledAgents: prompts.AllAgentIDs,
	}
	projDir, err := InitProject(projPath, orgDir, opts)
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}

	agentID := "security"
	sourceDir := projPath

	// Level 1: default content is written by InstallOrg.
	// The defaults file exists at orgDir/defaults/agents/security/report_prompt.md.
	defaultFile := filepath.Join(orgDir, "defaults", "agents", agentID, "report_prompt.md")
	if err := os.WriteFile(defaultFile, []byte("default content"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := prompts.AssembleAgentPrompt(orgDir, projDir, agentID, sourceDir, "", nil)
	if err != nil {
		t.Fatalf("AssembleAgentPrompt (defaults): %v", err)
	}
	if !strings.Contains(result, "default content") {
		t.Errorf("expected 'default content' in result, got:\n%s", result)
	}

	// Level 2: org override at orgDir/agents/security/report_prompt.md.
	orgOverrideFile := filepath.Join(orgDir, "agents", agentID, "report_prompt.md")
	if err := os.MkdirAll(filepath.Dir(orgOverrideFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(orgOverrideFile, []byte("org override"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err = prompts.AssembleAgentPrompt(orgDir, projDir, agentID, sourceDir, "", nil)
	if err != nil {
		t.Fatalf("AssembleAgentPrompt (org override): %v", err)
	}
	if !strings.Contains(result, "org override") {
		t.Errorf("expected 'org override' in result, got:\n%s", result)
	}
	if strings.Contains(result, "default content") {
		t.Error("org override should take precedence over default content")
	}

	// Level 3: project override at projDir/agents/security/report_prompt.md.
	projectOverrideFile := filepath.Join(projDir, "agents", agentID, "report_prompt.md")
	if err := os.MkdirAll(filepath.Dir(projectOverrideFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectOverrideFile, []byte("project override"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err = prompts.AssembleAgentPrompt(orgDir, projDir, agentID, sourceDir, "", nil)
	if err != nil {
		t.Fatalf("AssembleAgentPrompt (project override): %v", err)
	}
	if !strings.Contains(result, "project override") {
		t.Errorf("expected 'project override' in result, got:\n%s", result)
	}
	if strings.Contains(result, "org override") {
		t.Error("project override should take precedence over org override")
	}
}
