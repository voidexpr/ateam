// Package root provides project discovery, initialization and the resolved
// environment every command runs against.
package root

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/config"
	"github.com/ateam/internal/promptdata"
	"github.com/ateam/internal/runtime"
)

// InitProjectOpts holds options for creating a new project.
type InitProjectOpts struct {
	Name         string   // defaults to the directory name
	EnabledRoles []string // roles to enable; nil keeps the built-in defaults
	AllRoles     []string // roles listed in the generated file; defaults to promptdata.AllRoleIDs
}

// EnsureHome creates the user's ateam directory with a starter ateam.hcl
// and the state/ subdirectory. Idempotent: existing files are kept.
func EnsureHome(homeDir string) error {
	if homeDir == "" {
		return fmt.Errorf("cannot determine the user's ateam directory: set %s or HOME", runtime.HomeEnv)
	}
	for _, dir := range []string{homeDir, filepath.Join(homeDir, runtime.StateSubdir), filepath.Join(homeDir, "prompts")} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("cannot create %s: %w", dir, err)
		}
	}
	path := filepath.Join(homeDir, ConfigFile)
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return os.WriteFile(path, []byte(homeConfigTemplate), 0644)
}

const homeConfigTemplate = `// User-wide ateam configuration. Layers, lowest precedence first:
// built-in defaults -> this file -> each ancestor .ateam/ateam.hcl of a
// project -> the project's .ateam/ateam.hcl. A block here overrides only
// the attributes it sets. See CONFIG.md; 'ateam cat' prints the built-in
// defaults and 'ateam env' shows what every profile resolves to.
//
// Typical overrides:
//
//   agent "claude" {
//     model      = "claude-sonnet-5-5"
//     config_dir = "~/.ateam/state/agents/claude"   // or: ateam setup
//   }
//
//   project {
//     default_profile = "default"
//   }
`

// InitProject creates a new .ateam/ project directory at path: ateam.hcl
// with the project block and role statuses, .gitignore, state.sqlite, and
// a registry entry under the user's ateam directory.
func InitProject(path, homeDir string, opts InitProjectOpts) (string, error) {
	projDir := filepath.Join(path, DirName)
	if _, err := os.Stat(projDir); err == nil {
		return "", fmt.Errorf("%s/ already exists at %s", DirName, path)
	}

	name := opts.Name
	if name == "" {
		name = filepath.Base(path)
	}
	if err := config.ValidateProjectName(name); err != nil {
		return "", err
	}
	if err := EnsureHome(homeDir); err != nil {
		return "", err
	}
	if existing, ok := LookupRegistry(homeDir, name); ok && realPath(existing.Path) != realPath(path) {
		if _, err := os.Stat(filepath.Join(existing.Path, DirName)); err == nil {
			return "", fmt.Errorf("project %q already exists at %s (pick another --name)", name, existing.Path)
		}
	}

	roleIDs := opts.AllRoles
	if len(roleIDs) == 0 {
		roleIDs = promptdata.AllRoleIDs
	}
	if err := os.MkdirAll(filepath.Join(projDir, "logs"), 0755); err != nil {
		return "", fmt.Errorf("cannot create %s: %w", projDir, err)
	}
	if err := WriteProjectGitignore(projDir); err != nil {
		return "", err
	}
	if err := EnsureRoles(projDir, roleIDs); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(projDir, ConfigFile), []byte(projectConfigText(name, roleIDs, opts.EnabledRoles)), 0644); err != nil {
		return "", fmt.Errorf("cannot write %s: %w", ConfigFile, err)
	}

	// Create the project database with the proper schema.
	db, err := calldb.Open(filepath.Join(projDir, "state.sqlite"))
	if err != nil {
		return "", fmt.Errorf("cannot create project database: %w", err)
	}
	db.Close()

	if err := Register(homeDir, RegistryEntry{Name: name, Path: path, KeychainKey: NewKeychainKey(name, projDir)}); err != nil {
		return "", err
	}
	return projDir, nil
}

// projectConfigText renders the initial ateam.hcl of a project: the
// project block, then every role with its status (the built-in default,
// or on/off from enabled when given) so the list is there to edit.
func projectConfigText(name string, roleIDs, enabled []string) string {
	defaults := runtime.DefaultSettings().Roles
	enabledSet := make(map[string]bool, len(enabled))
	for _, id := range enabled {
		enabledSet[id] = true
	}
	ids := append([]string(nil), roleIDs...)
	sort.Strings(ids)

	var b strings.Builder
	b.WriteString("// ateam project configuration. Layers, lowest precedence first: built-in\n")
	b.WriteString("// defaults -> ~/.ateam/ateam.hcl -> ancestor .ateam/ateam.hcl files -> this\n")
	b.WriteString("// file. A block here overrides only the attributes it sets. 'ateam env'\n")
	b.WriteString("// shows what every profile resolves to; see CONFIG.md.\n\n")
	fmt.Fprintf(&b, "project {\n  name = %s\n", strconv.Quote(name))
	b.WriteString("  // default_profile = \"default\"   // e.g. \"cheap\", \"docker\"; see 'ateam env'\n")
	b.WriteString("}\n\n")
	b.WriteString("// Roles 'ateam report' runs. Add profile = \"...\" to run a role elsewhere.\n")
	for _, id := range ids {
		status := defaults[id]
		if status == "" {
			status = config.RoleDisabled
		}
		if len(enabled) > 0 {
			status = config.RoleDisabled
			if enabledSet[id] {
				status = config.RoleEnabled
			}
		}
		fmt.Fprintf(&b, "role %-28s { status = %q }\n", strconv.Quote(id), status)
	}
	b.WriteString("\n// supervisor { review_profile = \"\", code_profile = \"\", code_supervisor_profile = \"\" }\n")
	b.WriteString("// sandbox_extra { allow_write = [], allow_read = [], allow_domains = [] }\n")
	return b.String()
}

// EnsureRoles creates the per-role logs directories the runner needs to stream
// stdout/stderr into.
func EnsureRoles(projectDir string, roleIDs []string) error {
	for _, roleID := range roleIDs {
		if err := os.MkdirAll(filepath.Join(projectDir, "logs", "roles", roleID), 0755); err != nil {
			return fmt.Errorf("cannot create role logs directory: %w", err)
		}
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
	"agents/",
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

// NewKeychainKey creates a stable identifier for project-scoped keychain
// lookups. Format: <name>-<first 6 hex of SHA-256(absPath)>.
func NewKeychainKey(name, absPath string) string {
	h := sha256.Sum256([]byte(absPath))
	return fmt.Sprintf("%s-%x", name, h[:3])
}
