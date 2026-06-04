// Package root provides project initialization and setup for the root command including directory scaffolding and configuration.
package root

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/config"
	"github.com/ateam/internal/promptdata"
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

	allRoles := promptdata.AllRoleIDs
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

	if err := promptdata.WriteOrgDefaults(orgDir, false); err != nil {
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
		roleIDs = promptdata.AllRoleIDs
	}

	supervisorHistory := filepath.Join(projDir, "supervisor", "history")
	if err := os.MkdirAll(supervisorHistory, 0755); err != nil {
		return "", fmt.Errorf("cannot create supervisor directory: %w", err)
	}

	for _, id := range roleIDs {
		if err := os.MkdirAll(filepath.Join(projDir, "logs", "roles", id), 0755); err != nil {
			return "", fmt.Errorf("cannot create role logs directory: %w", err)
		}
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

// EnsureRoles creates the per-role logs directories the runner needs to stream
// stdout/stderr into. The history dirs under roles/<id>/history are not
// pre-created — they are written to on demand by the legacy log migration
// path (and don't exist for fresh runs at all).
func EnsureRoles(projectDir string, roleIDs []string) error {
	for _, roleID := range roleIDs {
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

// projectGitignoreEntries lists the entries ateam guarantees in .ateam/.gitignore.
// EnsureProjectGitignore appends any that are missing so projects created by an
// older binary self-heal on the next ateam command. Order matches the original
// `ateam init` output so a fresh project still produces a deterministic file.
var projectGitignoreEntries = []string{
	"state.sqlite",
	"state.sqlite-wal",
	"state.sqlite-shm",
	"logs/",
	"runtime/",
	"cache/",
	"secrets.env",
}

// WriteProjectGitignore writes the .gitignore file inside .ateam/ to exclude
// runtime artifacts. Used by `ateam init` to create the file from scratch;
// EnsureProjectGitignore handles the in-place upgrade case for existing
// projects.
func WriteProjectGitignore(projDir string) error {
	content := strings.Join(projectGitignoreEntries, "\n") + "\n"
	return os.WriteFile(filepath.Join(projDir, ".gitignore"), []byte(content), 0644)
}

// EnsureProjectGitignore appends any required entries that aren't already in
// .ateam/.gitignore (added in entry-list order), preserving user-added lines.
// Quiet no-op when every required entry is already present. Creates the file
// when it's missing entirely.
func EnsureProjectGitignore(projDir string) error {
	path := filepath.Join(projDir, ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return WriteProjectGitignore(projDir)
		}
		return err
	}
	present := make(map[string]bool)
	for _, line := range strings.Split(string(data), "\n") {
		present[strings.TrimSpace(line)] = true
	}
	var missing []string
	for _, entry := range projectGitignoreEntries {
		if !present[entry] {
			missing = append(missing, entry)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	// Ensure a trailing newline before our additions so we don't join the
	// last user line with the first required entry.
	out := string(data)
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	out += strings.Join(missing, "\n") + "\n"
	return os.WriteFile(path, []byte(out), 0644)
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
