package root

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ateam-poc/internal/config"
	"github.com/ateam-poc/internal/prompts"
)

// InitProjectOpts holds options for creating a new project.
type InitProjectOpts struct {
	Name            string
	Source          string
	GitRepo         string
	GitRemoteOrigin string
	EnabledAgents   []string
	AllAgents       []string
}

// InstallOrg creates a new .ateamorg/ directory at parentDir with default prompts
// and empty agent directories.
func InstallOrg(parentDir string) (string, error) {
	orgDir := filepath.Join(parentDir, OrgDirName)

	if _, err := os.Stat(orgDir); err == nil {
		return "", fmt.Errorf("%s/ already exists at %s", OrgDirName, parentDir)
	}

	allAgents := prompts.AllAgentIDs
	for _, id := range allAgents {
		dir := filepath.Join(orgDir, "agents", id)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return "", fmt.Errorf("cannot create agent directory %s: %w", dir, err)
		}
	}

	supervisorDir := filepath.Join(orgDir, "agents", "supervisor")
	if err := os.MkdirAll(supervisorDir, 0755); err != nil {
		return "", fmt.Errorf("cannot create supervisor directory: %w", err)
	}

	if err := prompts.WriteRootDefaults(orgDir, false); err != nil {
		return "", err
	}

	return orgDir, nil
}

// InitProject creates a new .ateam/ project directory at path.
// orgDir is the resolved .ateamorg/ directory used for duplicate-name checking.
func InitProject(path, orgDir string, opts InitProjectOpts) (string, error) {
	projDir := filepath.Join(path, ProjectDirName)

	if _, err := os.Stat(projDir); err == nil {
		return "", fmt.Errorf("%s/ already exists at %s", ProjectDirName, path)
	}

	if err := checkDuplicateProjectName(orgDir, opts.Name); err != nil {
		return "", err
	}

	agentIDs := opts.AllAgents
	if len(agentIDs) == 0 {
		agentIDs = prompts.AllAgentIDs
	}

	for _, id := range agentIDs {
		dir := filepath.Join(projDir, "agents", id, "history")
		if err := os.MkdirAll(dir, 0755); err != nil {
			return "", fmt.Errorf("cannot create agent directory %s: %w", dir, err)
		}
	}

	supervisorHistory := filepath.Join(projDir, "supervisor", "history")
	if err := os.MkdirAll(supervisorHistory, 0755); err != nil {
		return "", fmt.Errorf("cannot create supervisor directory: %w", err)
	}

	agents := make(map[string]string, len(agentIDs))
	enabledSet := make(map[string]bool, len(opts.EnabledAgents))
	for _, id := range opts.EnabledAgents {
		enabledSet[id] = true
	}
	for _, id := range agentIDs {
		if enabledSet[id] {
			agents[id] = "enabled"
		} else {
			agents[id] = "disabled"
		}
	}

	cfg := config.Config{
		Project: config.ProjectConfig{
			Name:   opts.Name,
			Source: opts.Source,
		},
		Git: config.GitConfig{
			Repo:            opts.GitRepo,
			RemoteOriginURL: opts.GitRemoteOrigin,
		},
		Report: config.ReportConfig{
			MaxParallel:               config.DefaultMaxParallel,
			AgentReportTimeoutMinutes: config.DefaultAgentReportTimeoutMinutes,
		},
		Agents: agents,
	}

	if err := config.Save(projDir, cfg); err != nil {
		return "", err
	}

	return projDir, nil
}

// EnsureAgents creates missing agent dirs under the project for the given agents.
func EnsureAgents(projectDir string, agentIDs []string) error {
	for _, agentID := range agentIDs {
		if err := os.MkdirAll(filepath.Join(projectDir, "agents", agentID, "history"), 0755); err != nil {
			return fmt.Errorf("cannot create project agent directory: %w", err)
		}
	}
	return nil
}

// checkDuplicateProjectName walks from orgDir's parent looking for any
// .ateam/config.toml with a matching project.name.
func checkDuplicateProjectName(orgDir, name string) error {
	start := filepath.Dir(orgDir)

	return filepath.WalkDir(start, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && d.Name() == OrgDirName {
			return filepath.SkipDir
		}
		if d.IsDir() && d.Name() == ProjectDirName {
			cfg, loadErr := config.Load(path)
			if loadErr == nil && cfg.Project.Name == name {
				return fmt.Errorf("project %q already exists at %s", name, filepath.Dir(path))
			}
			return filepath.SkipDir
		}
		return nil
	})
}
