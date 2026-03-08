package root

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ateam-poc/internal/config"
	"github.com/ateam-poc/internal/prompts"
)

func TestInstallOrg(t *testing.T) {
	tmp := t.TempDir()
	orgDir, err := InstallOrg(tmp)
	if err != nil {
		t.Fatalf("InstallOrg failed: %v", err)
	}

	wantOrg := filepath.Join(tmp, OrgDirName)
	if orgDir != wantOrg {
		t.Errorf("orgDir = %q, want %q", orgDir, wantOrg)
	}

	// Verify agent dirs exist for all known agents.
	for _, id := range prompts.AllAgentIDs {
		dir := filepath.Join(orgDir, "agents", id)
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Errorf("expected agent dir %s to exist", dir)
		}
	}

	// Verify supervisor dir exists.
	supervisorDir := filepath.Join(orgDir, "agents", "supervisor")
	if info, err := os.Stat(supervisorDir); err != nil || !info.IsDir() {
		t.Errorf("expected supervisor dir %s to exist", supervisorDir)
	}

	// Verify at least one default prompt file exists.
	if len(prompts.AllAgentIDs) > 0 {
		promptFile := filepath.Join(orgDir, "defaults", "agents", prompts.AllAgentIDs[0], "report_prompt.md")
		if _, err := os.Stat(promptFile); err != nil {
			t.Errorf("expected prompt file %s to exist: %v", promptFile, err)
		}
	}

	// Verify supervisor review prompt exists.
	reviewPrompt := filepath.Join(orgDir, "defaults", "supervisor", "review_prompt.md")
	if _, err := os.Stat(reviewPrompt); err != nil {
		t.Errorf("expected supervisor prompt %s to exist: %v", reviewPrompt, err)
	}
}

func TestInstallOrgAlreadyExists(t *testing.T) {
	tmp := t.TempDir()
	orgDir := filepath.Join(tmp, OrgDirName)
	if err := os.MkdirAll(orgDir, 0755); err != nil {
		t.Fatal(err)
	}

	_, err := InstallOrg(tmp)
	if err == nil {
		t.Fatal("expected error when org dir already exists, got nil")
	}
}

func TestInitProject(t *testing.T) {
	tmp := t.TempDir()
	orgDir, err := InstallOrg(tmp)
	if err != nil {
		t.Fatalf("InstallOrg failed: %v", err)
	}

	projectPath := filepath.Join(tmp, "myproject")
	if err := os.MkdirAll(projectPath, 0755); err != nil {
		t.Fatal(err)
	}

	enabled := []string{}
	if len(prompts.AllAgentIDs) >= 2 {
		enabled = prompts.AllAgentIDs[:2]
	} else if len(prompts.AllAgentIDs) >= 1 {
		enabled = prompts.AllAgentIDs[:1]
	}

	opts := InitProjectOpts{
		Name:            "test-project",
		Source:          "../myproject",
		GitRepo:         ".",
		GitRemoteOrigin: "git@github.com:example/repo.git",
		EnabledAgents:   enabled,
	}

	projDir, err := InitProject(projectPath, orgDir, opts)
	if err != nil {
		t.Fatalf("InitProject failed: %v", err)
	}

	wantProj := filepath.Join(projectPath, ProjectDirName)
	if projDir != wantProj {
		t.Errorf("projDir = %q, want %q", projDir, wantProj)
	}

	// Verify agent history dirs.
	for _, id := range prompts.AllAgentIDs {
		histDir := filepath.Join(projDir, "agents", id, "history")
		if info, err := os.Stat(histDir); err != nil || !info.IsDir() {
			t.Errorf("expected agent history dir %s to exist", histDir)
		}
	}

	// Verify supervisor history dir.
	supHist := filepath.Join(projDir, "supervisor", "history")
	if info, err := os.Stat(supHist); err != nil || !info.IsDir() {
		t.Errorf("expected supervisor history dir %s to exist", supHist)
	}

	// Load config and verify fields.
	cfg, err := config.Load(projDir)
	if err != nil {
		t.Fatalf("config.Load failed: %v", err)
	}

	if cfg.Project.Name != "test-project" {
		t.Errorf("project name = %q, want %q", cfg.Project.Name, "test-project")
	}
	if cfg.Project.Source != "../myproject" {
		t.Errorf("project source = %q, want %q", cfg.Project.Source, "../myproject")
	}
	if cfg.Git.Repo != "." {
		t.Errorf("git repo = %q, want %q", cfg.Git.Repo, ".")
	}
	if cfg.Git.RemoteOriginURL != "git@github.com:example/repo.git" {
		t.Errorf("git remote = %q, want %q", cfg.Git.RemoteOriginURL, "git@github.com:example/repo.git")
	}
	if cfg.Report.MaxParallel != config.DefaultMaxParallel {
		t.Errorf("max_parallel = %d, want %d", cfg.Report.MaxParallel, config.DefaultMaxParallel)
	}
	if cfg.Report.AgentReportTimeoutMinutes != config.DefaultAgentReportTimeoutMinutes {
		t.Errorf("timeout = %d, want %d", cfg.Report.AgentReportTimeoutMinutes, config.DefaultAgentReportTimeoutMinutes)
	}

	// Verify enabled/disabled agent states.
	for _, id := range enabled {
		if cfg.Agents[id] != "enabled" {
			t.Errorf("agent %s = %q, want %q", id, cfg.Agents[id], "enabled")
		}
	}
	enabledSet := make(map[string]bool, len(enabled))
	for _, id := range enabled {
		enabledSet[id] = true
	}
	for _, id := range prompts.AllAgentIDs {
		if !enabledSet[id] && cfg.Agents[id] != "disabled" {
			t.Errorf("agent %s = %q, want %q", id, cfg.Agents[id], "disabled")
		}
	}
}

func TestInitProjectAlreadyExists(t *testing.T) {
	tmp := t.TempDir()
	projectPath := filepath.Join(tmp, "myproject")
	projDir := filepath.Join(projectPath, ProjectDirName)
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatal(err)
	}

	_, err := InitProject(projectPath, filepath.Join(tmp, OrgDirName), InitProjectOpts{Name: "p"})
	if err == nil {
		t.Fatal("expected error when project dir already exists, got nil")
	}
}

func TestInitProjectDuplicateName(t *testing.T) {
	tmp := t.TempDir()
	orgDir, err := InstallOrg(tmp)
	if err != nil {
		t.Fatalf("InstallOrg failed: %v", err)
	}

	// Create first project.
	proj1 := filepath.Join(tmp, "proj1")
	if err := os.MkdirAll(proj1, 0755); err != nil {
		t.Fatal(err)
	}
	opts := InitProjectOpts{
		Name:          "shared-name",
		Source:        ".",
		EnabledAgents: prompts.AllAgentIDs,
	}
	if _, err := InitProject(proj1, orgDir, opts); err != nil {
		t.Fatalf("first InitProject failed: %v", err)
	}

	// Second project with same name should fail.
	proj2 := filepath.Join(tmp, "proj2")
	if err := os.MkdirAll(proj2, 0755); err != nil {
		t.Fatal(err)
	}
	opts2 := InitProjectOpts{
		Name:          "shared-name",
		Source:        ".",
		EnabledAgents: prompts.AllAgentIDs,
	}
	_, err = InitProject(proj2, orgDir, opts2)
	if err == nil {
		t.Fatal("expected error for duplicate project name, got nil")
	}
}
