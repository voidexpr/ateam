package root

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ateam/internal/config"
	"github.com/ateam/internal/gitutil"
	"github.com/ateam/internal/prompts"
)

const (
	OrgDirName     = ".ateamorg"
	ProjectDirName = ".ateam"
)

// ResolvedEnv holds all resolved paths for the org + project environment.
type ResolvedEnv struct {
	OrgDir      string // absolute path to .ateamorg/ (empty in org-less mode)
	ProjectDir  string // absolute path to .ateam/
	ProjectName string // from config.toml
	SourceDir   string // absolute path to project root (parent of .ateam/)
	GitRepoDir  string // resolved from config git.repo
	Config      *config.Config
}

func (e *ResolvedEnv) RoleDir(roleID string) string {
	return filepath.Join(e.ProjectDir, "roles", roleID)
}

func (e *ResolvedEnv) SupervisorDir() string {
	return filepath.Join(e.ProjectDir, "supervisor")
}

func (e *ResolvedEnv) RoleReportPath(roleID string) string {
	return filepath.Join(e.RoleDir(roleID), prompts.ReportFile)
}

func (e *ResolvedEnv) RoleHistoryDir(roleID string) string {
	return filepath.Join(e.RoleDir(roleID), "history")
}

func (e *ResolvedEnv) ReviewPath() string {
	return filepath.Join(e.SupervisorDir(), "review.md")
}

func (e *ResolvedEnv) ReviewHistoryDir() string {
	return filepath.Join(e.SupervisorDir(), "history")
}

func (e *ResolvedEnv) RunnerLogPath() string {
	return filepath.Join(e.ProjectDir, "logs", "runner.log")
}

func (e *ResolvedEnv) RoleLogsDir(roleID string) string {
	return filepath.Join(e.ProjectDir, "logs", "roles", roleID)
}

func (e *ResolvedEnv) SupervisorLogsDir() string {
	return filepath.Join(e.ProjectDir, "logs", "supervisor")
}

func (e *ResolvedEnv) RoleWorkspacesDir(roleID string) string {
	return filepath.Join(e.ProjectDir, "logs", "roles", roleID, "workspaces")
}

// ProjectDBPath returns the path to the per-project state database.
func (e *ResolvedEnv) ProjectDBPath() string {
	return filepath.Join(e.ProjectDir, "state.sqlite")
}

// NewProjectInfoParams builds a ProjectInfoParams from the resolved environment.
func (e *ResolvedEnv) NewProjectInfoParams(role, action string) prompts.ProjectInfoParams {
	meta, _ := gitutil.GetProjectMeta(e.SourceDir)
	return prompts.ProjectInfoParams{
		OrgDir:      e.OrgDir,
		ProjectDir:  e.ProjectDir,
		ProjectName: e.ProjectName,
		SourceDir:   e.SourceDir,
		GitRepoDir:  e.GitRepoDir,
		Role:        role,
		Action:      action,
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
// Returns "" in org-less mode.
func (e *ResolvedEnv) OrgRoot() string {
	if e.OrgDir == "" {
		return ""
	}
	return filepath.Dir(e.OrgDir)
}

// RelPath returns absPath relative to the org root.
// Returns absPath as-is if the computation fails, absPath is empty, or org-less mode.
func (e *ResolvedEnv) RelPath(absPath string) string {
	if absPath == "" || e.OrgDir == "" {
		return absPath
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
// If no .ateamorg/ is found but .ateam/ exists, operates in org-less mode.
func Resolve(orgOverride, projectOverride string) (*ResolvedEnv, error) {
	cwd := realPath(mustGetwd())

	var orgDir string
	var err error
	if orgOverride != "" {
		orgDir, err = resolveOrgByName(orgOverride)
		if err != nil {
			return nil, err
		}
	} else {
		orgDir, _ = FindOrg(cwd)
	}

	var projectDir string
	if projectOverride != "" {
		if orgDir == "" {
			return nil, fmt.Errorf("--project requires an org context (.ateamorg/)")
		}
		projectDir, err = resolveProjectByName(orgDir, projectOverride)
	} else {
		if orgDir != "" {
			projectDir, err = resolveProjectFromStateDir(orgDir, cwd)
			if err != nil {
				projectDir, err = FindProject(cwd)
			}
		} else {
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

// LookupFrom discovers org and project from the given starting path without creating anything.
// Returns partial ResolvedEnv if project is not found.
// Works in org-less mode: if no .ateamorg/ but .ateam/ exists, OrgDir is "".
func LookupFrom(start string) (*ResolvedEnv, error) {
	cwd := realPath(start)

	orgDir, _ := FindOrg(cwd)

	env := &ResolvedEnv{
		OrgDir: orgDir,
	}

	var projectDir string
	var err error
	if orgDir != "" {
		projectDir, err = resolveProjectFromStateDir(orgDir, cwd)
		if err != nil {
			projectDir, err = FindProject(cwd)
		}
	} else {
		projectDir, err = FindProject(cwd)
	}
	if err != nil {
		if orgDir != "" {
			return env, nil
		}
		return nil, fmt.Errorf("no .ateamorg/ or .ateam/ found")
	}

	env.ProjectDir = projectDir

	cfg, err := config.Load(projectDir)
	if err != nil {
		return env, nil
	}

	env.populateFromConfig(projectDir, cfg)

	return env, nil
}

// Lookup discovers org and project from the current working directory without creating anything.
func Lookup() (*ResolvedEnv, error) {
	return LookupFrom(mustGetwd())
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

// ResolveStreamPath resolves a relative stream_file path to absolute.
// New layout: relative to projectDir. Legacy: relative to orgDir (paths starting with "projects/").
func ResolveStreamPath(projectDir, orgDir, sf string) string {
	if sf == "" || filepath.IsAbs(sf) {
		return sf
	}
	if strings.HasPrefix(sf, "projects/") && orgDir != "" {
		return filepath.Join(orgDir, sf)
	}
	if projectDir != "" {
		return filepath.Join(projectDir, sf)
	}
	if orgDir != "" {
		return filepath.Join(orgDir, sf)
	}
	return sf
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
