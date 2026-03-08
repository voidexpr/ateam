package root

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ateam-poc/internal/config"
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
	SourceDir   string         // resolved from config project.source
	GitRepoDir  string         // resolved from config git.repo
	Config      *config.Config
}

func (e *ResolvedEnv) AgentReportPath(agentID, reportType string) string {
	return filepath.Join(e.ProjectDir, "agents", agentID, reportType+"_report.md")
}

func (e *ResolvedEnv) AgentHistoryDir(agentID string) string {
	return filepath.Join(e.ProjectDir, "agents", agentID, "history")
}

func (e *ResolvedEnv) ReviewPath() string {
	return filepath.Join(e.ProjectDir, "supervisor", "review.md")
}

func (e *ResolvedEnv) ReviewHistoryDir() string {
	return filepath.Join(e.ProjectDir, "supervisor", "history")
}

func (e *ResolvedEnv) populateFromConfig(projectDir string, cfg *config.Config) {
	e.Config = cfg
	e.ProjectName = cfg.Project.Name
	if cfg.Project.Source != "" {
		e.SourceDir = resolveRelPath(projectDir, cfg.Project.Source)
	}
	if cfg.Git.Repo != "" && e.SourceDir != "" {
		e.GitRepoDir = resolveRelPath(e.SourceDir, cfg.Git.Repo)
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
		projectDir, err = FindProject(cwd)
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

	projectDir, err := FindProject(cwd)
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

// WalkProjects walks from orgDir's parent looking for .ateam/config.toml files.
// The callback receives each discovered project. Return filepath.SkipAll to stop early.
func WalkProjects(orgDir string, fn func(ProjectInfo) error) error {
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
			if loadErr != nil {
				return filepath.SkipDir
			}
			if err := fn(ProjectInfo{Dir: path, Config: cfg}); err != nil {
				return err
			}
			return filepath.SkipDir
		}
		return nil
	})
}

// resolveOrgByName treats override as a path and looks for .ateamorg child there.
func resolveOrgByName(override string) (string, error) {
	candidate := filepath.Join(override, OrgDirName)
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return realPath(candidate), nil
	}
	return "", fmt.Errorf("no %s/ found under %s", OrgDirName, override)
}

// resolveProjectByName walks from orgDir's parent looking for a project with matching name.
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

// resolveRelPath resolves rel relative to base's parent directory.
// If rel is absolute, it is returned as-is.
func resolveRelPath(base, rel string) string {
	if filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Join(filepath.Dir(base), rel)
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
