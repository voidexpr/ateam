package root

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ateam-poc/internal/config"
	"github.com/ateam-poc/internal/gitutil"
	"github.com/ateam-poc/internal/prompts"
)

const (
	OrgDirName     = ".ateamorg"
	ProjectDirName = ".ateam"
)

// ResolvedEnv holds all resolved paths for the org + project environment.
type ResolvedEnv struct {
	OrgDir      string         // absolute path to .ateamorg/
	ProjectDir  string         // absolute path to .ateam/
	ProjectName string         // from config.toml
	SourceDir   string         // absolute path to project root (parent of .ateam/)
	GitRepoDir  string         // resolved from config git.repo
	StateDir    string         // .ateamorg/projects/<project-id>/
	Config      *config.Config
}

func (e *ResolvedEnv) RoleReportPath(roleID string) string {
	return filepath.Join(e.ProjectDir, "roles", roleID, prompts.ReportFile)
}

func (e *ResolvedEnv) RoleHistoryDir(roleID string) string {
	return filepath.Join(e.ProjectDir, "roles", roleID, "history")
}

func (e *ResolvedEnv) ReviewPath() string {
	return filepath.Join(e.ProjectDir, "supervisor", "review.md")
}

func (e *ResolvedEnv) ReviewHistoryDir() string {
	return filepath.Join(e.ProjectDir, "supervisor", "history")
}

func (e *ResolvedEnv) RunnerLogPath() string {
	if e.StateDir != "" {
		return filepath.Join(e.StateDir, "runner.log")
	}
	return filepath.Join(e.ProjectDir, "logs", "runner.log")
}

func (e *ResolvedEnv) RoleLogsDir(roleID string) string {
	return filepath.Join(e.StateDir, "roles", roleID, "logs")
}

func (e *ResolvedEnv) SupervisorLogsDir() string {
	return filepath.Join(e.StateDir, "supervisor", "logs")
}

func (e *ResolvedEnv) RoleWorkspacesDir(roleID string) string {
	return filepath.Join(e.StateDir, "roles", roleID, "workspaces")
}

// NewProjectInfoParams builds a ProjectInfoParams from the resolved environment.
func (e *ResolvedEnv) NewProjectInfoParams(role string) prompts.ProjectInfoParams {
	meta, _ := gitutil.GetProjectMeta(e.SourceDir)
	return prompts.ProjectInfoParams{
		OrgDir:      e.OrgDir,
		ProjectDir:  e.ProjectDir,
		ProjectName: e.ProjectName,
		SourceDir:   e.SourceDir,
		GitRepoDir:  e.GitRepoDir,
		Role:        role,
		Meta:        meta,
	}
}

// ProjectID returns the project identifier derived from the source directory path.
func (e *ResolvedEnv) ProjectID() string {
	if e.SourceDir == "" || e.OrgDir == "" {
		return ""
	}
	return config.PathToProjectID(e.RelPath(e.SourceDir))
}

// OrgRoot returns the parent directory of .ateamorg.
func (e *ResolvedEnv) OrgRoot() string {
	return filepath.Dir(e.OrgDir)
}

// RelPath returns absPath relative to the org root.
// Returns absPath as-is if the computation fails or absPath is empty.
func (e *ResolvedEnv) RelPath(absPath string) string {
	if absPath == "" {
		return ""
	}
	rel, err := filepath.Rel(e.OrgRoot(), absPath)
	if err != nil {
		return absPath
	}
	return rel
}

func (e *ResolvedEnv) populateFromConfig(projectDir string, cfg *config.Config) {
	e.Config = cfg
	e.ProjectName = cfg.Project.Name
	e.SourceDir = filepath.Dir(projectDir) // project root = parent of .ateam/
	if cfg.Git.Repo != "" {
		e.GitRepoDir = resolvePath(e.SourceDir, cfg.Git.Repo)
	}
	relPath := e.RelPath(e.SourceDir)
	e.StateDir = filepath.Join(e.OrgDir, "projects", config.PathToProjectID(relPath))
}

// FindOrg walks up from cwd looking for a .ateamorg directory.
func FindOrg(cwd string) (string, error) {
	return findDirUp(cwd, OrgDirName, "run 'ateam install' first")
}

// FindProject walks up from cwd looking for a .ateam directory.
func FindProject(cwd string) (string, error) {
	return findDirUp(cwd, ProjectDirName, "not inside an ateam project")
}

// findDirUp walks up from cwd looking for a directory named target.
// If cwd is already inside the target directory, returns it directly.
func findDirUp(cwd, target, errHint string) (string, error) {
	if dir, ok := findInPath(cwd, target); ok {
		return dir, nil
	}

	dir := filepath.Clean(cwd)
	for {
		candidate := filepath.Join(dir, target)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return realPath(candidate), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", fmt.Errorf("no %s/ found (%s)", target, errHint)
}

// findInPath checks if cwd is inside a directory named target.
// Returns the absolute path up to and including the target component.
func findInPath(cwd, target string) (string, bool) {
	parts := strings.Split(filepath.Clean(cwd), string(filepath.Separator))
	for i, part := range parts {
		if part == target {
			root := string(filepath.Separator) + filepath.Join(parts[:i+1]...)
			return root, true
		}
	}
	return "", false
}

// Resolve discovers org and project directories and loads config.
func Resolve(orgOverride, projectOverride string) (*ResolvedEnv, error) {
	cwd := realPath(mustGetwd())

	var orgDir string
	var err error
	if orgOverride != "" {
		orgDir, err = resolveOrgByName(orgOverride)
	} else {
		orgDir, err = FindOrg(cwd)
	}
	if err != nil {
		return nil, err
	}

	var projectDir string
	if projectOverride != "" {
		projectDir, err = resolveProjectByName(orgDir, projectOverride)
	} else {
		projectDir, err = resolveProjectFromStateDir(orgDir, cwd)
		if err != nil {
			projectDir, err = FindProject(cwd)
		}
	}
	if err != nil {
		return nil, err
	}

	cfg, err := config.Load(projectDir)
	if err != nil {
		return nil, err
	}

	env := &ResolvedEnv{
		OrgDir:     orgDir,
		ProjectDir: projectDir,
	}
	env.populateFromConfig(projectDir, cfg)

	return env, nil
}

// Lookup discovers org and project without creating anything.
// Returns partial ResolvedEnv if project is not found.
func Lookup() (*ResolvedEnv, error) {
	cwd := realPath(mustGetwd())

	orgDir, err := FindOrg(cwd)
	if err != nil {
		return nil, err
	}

	env := &ResolvedEnv{
		OrgDir: orgDir,
	}

	projectDir, err := resolveProjectFromStateDir(orgDir, cwd)
	if err != nil {
		projectDir, err = FindProject(cwd)
	}
	if err != nil {
		return env, nil
	}

	env.ProjectDir = projectDir

	cfg, err := config.Load(projectDir)
	if err != nil {
		return env, nil
	}

	env.populateFromConfig(projectDir, cfg)

	return env, nil
}

// ProjectInfo holds metadata about a discovered project.
type ProjectInfo struct {
	Dir    string
	Config *config.Config
}

// WalkProjects enumerates registered projects from .ateamorg/projects/.
// Each subdirectory is a project ID that maps back to a project path.
// The callback receives each discovered project. Return filepath.SkipAll to stop early.
func WalkProjects(orgDir string, fn func(ProjectInfo) error) error {
	projectsDir := filepath.Join(orgDir, "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("cannot read projects directory: %w", err)
	}

	orgRoot := filepath.Dir(orgDir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		relPath := config.ProjectIDToPath(e.Name())
		projDir := filepath.Join(orgRoot, relPath, ProjectDirName)
		cfg, loadErr := config.Load(projDir)
		if loadErr != nil {
			continue
		}
		if err := fn(ProjectInfo{Dir: projDir, Config: cfg}); err != nil {
			if err == filepath.SkipAll {
				return nil
			}
			return err
		}
	}
	return nil
}

// resolveProjectFromStateDir checks if cwd is inside .ateamorg/projects/<id>/
// and resolves the project directory by reversing the project ID to a path.
func resolveProjectFromStateDir(orgDir, cwd string) (string, error) {
	projectsDir := filepath.Join(orgDir, "projects")
	prefix := projectsDir + string(filepath.Separator)
	if !strings.HasPrefix(cwd+string(filepath.Separator), prefix) {
		return "", fmt.Errorf("not inside a state directory")
	}

	// Extract the project ID: first path component after "projects/"
	rest := cwd[len(projectsDir):]
	rest = strings.TrimPrefix(rest, string(filepath.Separator))
	if rest == "" {
		return "", fmt.Errorf("not inside a specific project state directory")
	}
	projectID := strings.SplitN(rest, string(filepath.Separator), 2)[0]

	relPath := config.ProjectIDToPath(projectID)
	orgRoot := filepath.Dir(orgDir)
	candidate := filepath.Join(orgRoot, relPath, ProjectDirName)

	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return realPath(candidate), nil
	}
	return "", fmt.Errorf("project directory not found for state %q", projectID)
}

// resolveOrgByName treats override as a path and looks for .ateamorg child there.
func resolveOrgByName(override string) (string, error) {
	candidate := filepath.Join(override, OrgDirName)
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return realPath(candidate), nil
	}
	return "", fmt.Errorf("no %s/ found under %s", OrgDirName, override)
}

// resolveProjectByName searches registered projects for one with the given name.
func resolveProjectByName(orgDir, name string) (string, error) {
	var found string
	err := WalkProjects(orgDir, func(p ProjectInfo) error {
		if p.Config.Project.Name == name {
			found = p.Dir
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil && err != filepath.SkipAll {
		return "", fmt.Errorf("error searching for project %q: %w", name, err)
	}
	if found == "" {
		return "", fmt.Errorf("project %q not found under %s", name, filepath.Dir(orgDir))
	}
	return realPath(found), nil
}

// resolvePath resolves rel relative to base.
// If rel is absolute, it is returned as-is.
func resolvePath(base, rel string) string {
	if filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Join(base, rel)
}

func realPath(p string) string {
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return filepath.Clean(p)
	}
	return r
}

func mustGetwd() string {
	cwd, err := os.Getwd()
	if err != nil {
		panic(fmt.Sprintf("cannot get working directory: %v", err))
	}
	return cwd
}
