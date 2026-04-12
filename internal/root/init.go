// Package root provides project initialization and setup for the root command including directory scaffolding and configuration.
package root

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/config"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/runtime"
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

	if err := runtime.WriteOrgDefaults(orgDir, false); err != nil {
		return "", err
	}

	return orgDir, nil
}

// InitProject creates a new .ateam/ project directory at path.
// orgDir is the resolved .ateamorg/ directory used for duplicate-name checking.
// orgDir may be empty for org-less mode.
func InitProject(path, orgDir string, opts InitProjectOpts) (string, error) {
	projDir := filepath.Join(path, ProjectDirName)

	if _, err := os.Stat(projDir); err == nil {
		return "", fmt.Errorf("%s/ already exists at %s", ProjectDirName, path)
	}

	if orgDir != "" {
		if err := checkDuplicateProjectName(orgDir, opts.Name); err != nil {
			return "", err
		}
	}

	var relPath string
	if orgDir != "" {
		orgRoot := filepath.Dir(orgDir)
		rel, err := filepath.Rel(orgRoot, path)
		if err != nil {
			relPath = path
		} else {
			relPath = rel
		}
		if relPath != "." {
			if err := config.ValidateProjectPath(relPath); err != nil {
				return "", err
			}
		}
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

	// Create logs directories under .ateam/
	if err := createLogsDirs(projDir, roleIDs); err != nil {
		return "", err
	}

	// Write .gitignore for runtime artifacts
	if err := WriteProjectGitignore(projDir); err != nil {
		return "", err
	}

	cfg := config.DefaultConfig()
	cfg.Project.Name = opts.Name
	cfg.Project.KeychainKey = generateKeychainKey(opts.Name, projDir)
	cfg.Git.Repo = opts.GitRepo
	cfg.Git.RemoteOriginURL = opts.GitRemoteOrigin

	if len(opts.EnabledRoles) > 0 {
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
		cfg.Roles = roles
	}

	if err := config.Save(projDir, cfg); err != nil {
		return "", err
	}

	// Create the project database with the proper schema.
	db, err := calldb.Open(filepath.Join(projDir, "state.sqlite"))
	if err != nil {
		return "", fmt.Errorf("cannot create project database: %w", err)
	}
	db.Close()

	// Legacy: create state dirs under .ateamorg/projects/ if org exists
	if orgDir != "" && relPath != "." {
		projectID := config.PathToProjectID(relPath)
		if err := createStateDirs(orgDir, projectID, roleIDs); err != nil {
			return "", err
		}
	}

	return projDir, nil
}

// EnsureRoles creates missing role dirs under the project for the given roles.
// The project role dirs (history) are best-effort (may fail on read-only mounts);
// the logs dirs under .ateam/logs/ are required for logging.
func EnsureRoles(projectDir string, roleIDs []string) error {
	for _, roleID := range roleIDs {
		if err := os.MkdirAll(filepath.Join(projectDir, "roles", roleID, "history"), 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: cannot create project role directory for %s: %v\n", roleID, err)
		}
		if err := os.MkdirAll(filepath.Join(projectDir, "logs", "roles", roleID), 0755); err != nil {
			return fmt.Errorf("cannot create role logs directory: %w", err)
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

// createLogsDirs creates the logs directory structure under .ateam/.
func createLogsDirs(projDir string, roleIDs []string) error {
	for _, id := range roleIDs {
		if err := os.MkdirAll(filepath.Join(projDir, "logs", "roles", id), 0755); err != nil {
			return fmt.Errorf("cannot create role logs directory: %w", err)
		}
	}
	for _, sub := range []string{"supervisor", "run"} {
		if err := os.MkdirAll(filepath.Join(projDir, "logs", sub), 0755); err != nil {
			return fmt.Errorf("cannot create %s logs directory: %w", sub, err)
		}
	}
	return nil
}

// WriteProjectGitignore writes the .gitignore file inside .ateam/ to exclude
// runtime artifacts (state.sqlite and logs/).
func WriteProjectGitignore(projDir string) error {
	content := "state.sqlite\nstate.sqlite-wal\nstate.sqlite-shm\nlogs/\ncache/\nsecrets.env\n"
	return os.WriteFile(filepath.Join(projDir, ".gitignore"), []byte(content), 0644)
}

// generateKeychainKey creates a stable identifier for keychain lookups.
// Format: <name>-<first 6 hex of SHA-256(absPath)>.
func generateKeychainKey(name, absPath string) string {
	h := sha256.Sum256([]byte(absPath))
	return fmt.Sprintf("%s-%x", name, h[:3])
}

// checkDuplicateProjectName checks registered projects for a name collision.
func checkDuplicateProjectName(orgDir, name string) error {
	return WalkProjects(orgDir, func(p ProjectInfo) error {
		if p.Config.Project.Name == name {
			return fmt.Errorf("project %q already exists at %s", name, filepath.Dir(p.Dir))
		}
		return nil
	})
}
