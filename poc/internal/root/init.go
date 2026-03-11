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
	GitRepo         string
	GitRemoteOrigin string
	EnabledRoles    []string
	AllRoles        []string
}

// InstallOrg creates a new .ateamorg/ directory at parentDir with default prompts
// and empty role directories.
func InstallOrg(parentDir string) (string, error) {
	orgDir := filepath.Join(parentDir, OrgDirName)

	if _, err := os.Stat(orgDir); err == nil {
		return "", fmt.Errorf("%s/ already exists at %s", OrgDirName, parentDir)
	}

	allRoles := prompts.AllRoleIDs
	for _, id := range allRoles {
		dir := filepath.Join(orgDir, "roles", id)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return "", fmt.Errorf("cannot create role directory %s: %w", dir, err)
		}
	}

	supervisorDir := filepath.Join(orgDir, "roles", "supervisor")
	if err := os.MkdirAll(supervisorDir, 0755); err != nil {
		return "", fmt.Errorf("cannot create supervisor directory: %w", err)
	}

	if err := prompts.WriteOrgDefaults(orgDir, false); err != nil {
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

	orgRoot := filepath.Dir(orgDir)
	relPath, err := filepath.Rel(orgRoot, path)
	if err != nil {
		relPath = path
	}
	if err := config.ValidateProjectPath(relPath); err != nil {
		return "", err
	}

	roleIDs := opts.AllRoles
	if len(roleIDs) == 0 {
		roleIDs = prompts.AllRoleIDs
	}

	for _, id := range roleIDs {
		dir := filepath.Join(projDir, "roles", id, "history")
		if err := os.MkdirAll(dir, 0755); err != nil {
			return "", fmt.Errorf("cannot create role directory %s: %w", dir, err)
		}
	}

	supervisorHistory := filepath.Join(projDir, "supervisor", "history")
	if err := os.MkdirAll(supervisorHistory, 0755); err != nil {
		return "", fmt.Errorf("cannot create supervisor directory: %w", err)
	}

	roles := make(map[string]string, len(roleIDs))
	enabledSet := make(map[string]bool, len(opts.EnabledRoles))
	for _, id := range opts.EnabledRoles {
		enabledSet[id] = true
	}
	for _, id := range roleIDs {
		if enabledSet[id] {
			roles[id] = config.RoleEnabled
		} else {
			roles[id] = config.RoleDisabled
		}
	}

	cfg := config.Config{
		Project: config.ProjectConfig{
			Name: opts.Name,
		},
		Git: config.GitConfig{
			Repo:            opts.GitRepo,
			RemoteOriginURL: opts.GitRemoteOrigin,
		},
		Report: config.ReportConfig{
			MaxParallel:          config.DefaultMaxParallel,
			ReportTimeoutMinutes: config.DefaultReportTimeoutMinutes,
		},
		Roles: roles,
	}

	if err := config.Save(projDir, cfg); err != nil {
		return "", err
	}

	projectID := config.PathToProjectID(relPath)

	if err := createStateDirs(orgDir, projectID, roleIDs); err != nil {
		return "", err
	}

	return projDir, nil
}

// EnsureRoles creates missing role dirs under the project and state dir for the given roles.
func EnsureRoles(projectDir, stateDir string, roleIDs []string) error {
	for _, roleID := range roleIDs {
		if err := os.MkdirAll(filepath.Join(projectDir, "roles", roleID, "history"), 0755); err != nil {
			return fmt.Errorf("cannot create project role directory: %w", err)
		}
		if stateDir != "" {
			if err := os.MkdirAll(filepath.Join(stateDir, "roles", roleID, "logs"), 0755); err != nil {
				return fmt.Errorf("cannot create role state directory: %w", err)
			}
		}
	}
	return nil
}

func createStateDirs(orgDir, projectID string, roleIDs []string) error {
	stateBase := filepath.Join(orgDir, "projects", projectID)
	for _, id := range roleIDs {
		if err := os.MkdirAll(filepath.Join(stateBase, "roles", id, "logs"), 0755); err != nil {
			return fmt.Errorf("cannot create role state directory: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Join(stateBase, "supervisor", "logs"), 0755); err != nil {
		return fmt.Errorf("cannot create supervisor state directory: %w", err)
	}
	return nil
}

// checkDuplicateProjectName walks from orgDir's parent looking for any
// .ateam/config.toml with a matching project.name.
func checkDuplicateProjectName(orgDir, name string) error {
	return WalkProjects(orgDir, func(p ProjectInfo) error {
		if p.Config.Project.Name == name {
			return fmt.Errorf("project %q already exists at %s", name, filepath.Dir(p.Dir))
		}
		return nil
	})
}
