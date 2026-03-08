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

// FindOrg walks up from cwd looking for a .ateamorg directory.
func FindOrg(cwd string) (string, error) {
	if dir, ok := findInPath(cwd, OrgDirName); ok {
		return dir, nil
	}

	dir := filepath.Clean(cwd)
	for {
		candidate := filepath.Join(dir, OrgDirName)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return realPath(candidate), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", fmt.Errorf("no %s/ found (run 'ateam install' first)", OrgDirName)
}

// FindProject walks up from cwd looking for a .ateam directory.
func FindProject(cwd string) (string, error) {
	if dir, ok := findInPath(cwd, ProjectDirName); ok {
		return dir, nil
	}

	dir := filepath.Clean(cwd)
	for {
		candidate := filepath.Join(dir, ProjectDirName)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return realPath(candidate), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", fmt.Errorf("no %s/ found (not inside an ateam project)", ProjectDirName)
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
		OrgDir:      orgDir,
		ProjectDir:  projectDir,
		ProjectName: cfg.Project.Name,
		Config:      cfg,
	}

	if cfg.Project.Source != "" {
		env.SourceDir = resolveRelPath(projectDir, cfg.Project.Source)
	}

	if cfg.Git.Repo != "" && env.SourceDir != "" {
		env.GitRepoDir = resolveRelPath(env.SourceDir, cfg.Git.Repo)
	}

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

	env.Config = cfg
	env.ProjectName = cfg.Project.Name

	if cfg.Project.Source != "" {
		env.SourceDir = resolveRelPath(projectDir, cfg.Project.Source)
	}

	if cfg.Git.Repo != "" && env.SourceDir != "" {
		env.GitRepoDir = resolveRelPath(env.SourceDir, cfg.Git.Repo)
	}

	return env, nil
}

// resolveOrgByName treats override as a path and looks for .ateamorg child there.
func resolveOrgByName(override string) (string, error) {
	candidate := filepath.Join(override, OrgDirName)
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return realPath(candidate), nil
	}
	return "", fmt.Errorf("no %s/ found under %s", OrgDirName, override)
}

// resolveProjectByName walks from orgDir's parent looking for a .ateam/config.toml
// where project.name matches the given name.
func resolveProjectByName(orgDir, name string) (string, error) {
	start := filepath.Dir(orgDir)

	var found string
	err := filepath.WalkDir(start, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && d.Name() == OrgDirName {
			return filepath.SkipDir
		}
		if d.IsDir() && d.Name() == ProjectDirName {
			configPath := filepath.Join(path, "config.toml")
			cfg, loadErr := config.Load(filepath.Dir(configPath))
			if loadErr == nil && cfg.Project.Name == name {
				found = path
				return filepath.SkipAll
			}
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("error searching for project %q: %w", name, err)
	}
	if found == "" {
		return "", fmt.Errorf("project %q not found under %s", name, start)
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

func mustHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		panic(fmt.Sprintf("cannot determine home directory: %v", err))
	}
	return home
}
