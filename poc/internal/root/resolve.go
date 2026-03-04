package root

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ateam-poc/internal/config"
)

// ResolvedProject holds all resolved paths for a project.
type ResolvedProject struct {
	ProjectRelPath string         // e.g. "code/myapp"
	SourceDir      string         // absolute path to source git directory
	AteamRoot      string         // path to .ateam/
	ProjectDir     string         // .ateam/projects/<RelPath>/
	Config         *config.Config
}

// AgentReportPath returns the path to an agent's report file.
func (p *ResolvedProject) AgentReportPath(agentID, reportType string) string {
	return filepath.Join(p.ProjectDir, "agents", agentID, reportType+"_report.md")
}

// AgentHistoryDir returns the history directory for an agent.
func (p *ResolvedProject) AgentHistoryDir(agentID string) string {
	return filepath.Join(p.ProjectDir, "agents", agentID, "history")
}

// ReviewPath returns the path to the supervisor review file.
func (p *ResolvedProject) ReviewPath() string {
	return filepath.Join(p.ProjectDir, "supervisor", "review.md")
}

// ReviewHistoryDir returns the history directory for supervisor reviews.
func (p *ResolvedProject) ReviewHistoryDir() string {
	return filepath.Join(p.ProjectDir, "supervisor", "history")
}

// realPath resolves symlinks and returns an absolute path.
func realPath(p string) string {
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return filepath.Clean(p)
	}
	return r
}

// Resolve discovers the .ateam root and project, auto-creating as needed.
// agentHint is used when auto-initializing a project to pre-create agent dirs.
func Resolve(agentHint []string) (*ResolvedProject, error) {
	cwd := realPath(mustGetwd())

	if ateamRoot, ok := isInsideAteam(cwd); ok {
		return resolveInside(cwd, ateamRoot)
	}

	ateamRoot, err := findAteamRoot(cwd)
	if err != nil {
		return nil, err
	}
	if ateamRoot == "" {
		home := realPath(mustHomeDir())

		gitRoot, gitErr := findSourceGit(cwd)
		if gitErr != nil {
			return nil, gitErr
		}
		rel, relErr := filepath.Rel(home, gitRoot)
		if relErr != nil || strings.HasPrefix(rel, "..") {
			return nil, fmt.Errorf("no .ateam/ found and git root %s is not under $HOME\nRun 'ateam install <parent-dir>' where <parent-dir> is a common ancestor of your projects", gitRoot)
		}

		ateamRoot, err = Install(home)
		if err != nil {
			return nil, fmt.Errorf("cannot create .ateam: %w", err)
		}
		fmt.Printf("Created %s with default prompts\n", ateamRoot)

		// Pass gitRoot to avoid a second git subprocess
		return resolveOutsideWithSourceGit(ateamRoot, gitRoot, agentHint)
	}

	return resolveOutside(cwd, ateamRoot, agentHint)
}

// isInsideAteam checks if cwd is inside a .ateam directory.
func isInsideAteam(cwd string) (string, bool) {
	parts := strings.Split(filepath.Clean(cwd), string(filepath.Separator))
	for i, part := range parts {
		if part == ".ateam" {
			root := string(filepath.Separator) + filepath.Join(parts[:i+1]...)
			return root, true
		}
	}
	return "", false
}

// findAteamRoot walks up from dir looking for a .ateam/ child directory.
// Stops at $HOME (inclusive). Returns "" if not found.
// dir must already be resolved via realPath.
func findAteamRoot(dir string) (string, error) {
	home := realPath(mustHomeDir())

	for {
		candidate := filepath.Join(dir, ".ateam")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return realPath(candidate), nil
		}

		if dir == "/" || dir == "." {
			break
		}
		if dir == home {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", nil
}

// findSourceGit runs git rev-parse --show-toplevel to find the git root.
func findSourceGit(dir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("not a git repository (run from a git project or use 'ateam install')")
	}
	return realPath(strings.TrimSpace(string(out))), nil
}

// resolveOutside handles the case where we're in a git project outside .ateam.
func resolveOutside(cwd, ateamRoot string, agentHint []string) (*ResolvedProject, error) {
	gitRoot, err := findSourceGit(cwd)
	if err != nil {
		return nil, err
	}
	return resolveOutsideWithSourceGit(ateamRoot, gitRoot, agentHint)
}

// resolveOutsideWithSourceGit is the core resolution when we already know the git root.
func resolveOutsideWithSourceGit(ateamRoot, gitRoot string, agentHint []string) (*ResolvedProject, error) {
	ateamParent := filepath.Dir(ateamRoot)
	relPath, err := filepath.Rel(ateamParent, gitRoot)
	if err != nil || strings.HasPrefix(relPath, "..") {
		return nil, fmt.Errorf("git root %s is not under .ateam parent %s", gitRoot, ateamParent)
	}

	projectDir := filepath.Join(ateamRoot, "projects", relPath)
	configPath := filepath.Join(projectDir, "config.toml")

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		projectDir, err = AutoInitProject(ateamRoot, gitRoot, relPath, agentHint)
		if err != nil {
			return nil, fmt.Errorf("cannot auto-init project: %w", err)
		}
		fmt.Printf("Auto-initialized project: %s\n", relPath)
	}

	cfg, err := config.Load(projectDir)
	if err != nil {
		return nil, err
	}

	return &ResolvedProject{
		ProjectRelPath: relPath,
		SourceDir:      gitRoot,
		AteamRoot:      ateamRoot,
		ProjectDir:     projectDir,
		Config:         cfg,
	}, nil
}

// resolveInside handles the case where we're inside the .ateam directory tree.
func resolveInside(cwd, ateamRoot string) (*ResolvedProject, error) {
	projectsDir := filepath.Join(ateamRoot, "projects")

	rel, err := filepath.Rel(projectsDir, cwd)
	if err != nil || strings.HasPrefix(rel, "..") {
		return nil, fmt.Errorf("inside .ateam but not under projects/ — cd into a project or run from a git checkout")
	}

	dir := cwd
	for {
		if _, err := os.Stat(filepath.Join(dir, "config.toml")); err == nil {
			break
		}
		if dir == projectsDir || dir == ateamRoot {
			return nil, fmt.Errorf("no config.toml found under .ateam/projects/ — run 'ateam init' from a git project first")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, fmt.Errorf("no config.toml found under .ateam/projects/")
		}
		dir = parent
	}

	relPath, err := filepath.Rel(projectsDir, dir)
	if err != nil {
		return nil, err
	}

	cfg, err := config.Load(dir)
	if err != nil {
		return nil, err
	}

	return &ResolvedProject{
		ProjectRelPath: relPath,
		SourceDir:      resolveSourceDir(ateamRoot, cfg.Project.SourceDir),
		AteamRoot:      ateamRoot,
		ProjectDir:     dir,
		Config:         cfg,
	}, nil
}

// EnvInfo holds read-only environment information (no auto-creation).
type EnvInfo struct {
	AteamRoot      string
	SourceGit        string
	ProjectRelPath string
	ProjectDir     string
	Agents         []string
}

// Lookup discovers the .ateam root and project without creating anything.
func Lookup() (*EnvInfo, error) {
	cwd := realPath(mustGetwd())

	ateamRoot := ""
	if root, ok := isInsideAteam(cwd); ok {
		ateamRoot = root
	} else {
		found, err := findAteamRoot(cwd)
		if err != nil {
			return nil, err
		}
		ateamRoot = found
	}
	if ateamRoot == "" {
		return nil, fmt.Errorf("no .ateam/ found (run 'ateam install' first)")
	}

	gitRoot, _ := findSourceGit(cwd)
	if gitRoot == "" {
		return &EnvInfo{AteamRoot: ateamRoot}, nil
	}

	info := &EnvInfo{
		AteamRoot: ateamRoot,
		SourceGit:   gitRoot,
	}

	ateamParent := filepath.Dir(ateamRoot)
	relPath, err := filepath.Rel(ateamParent, gitRoot)
	if err != nil || strings.HasPrefix(relPath, "..") {
		return info, nil
	}

	projectDir := filepath.Join(ateamRoot, "projects", relPath)
	configPath := filepath.Join(projectDir, "config.toml")
	if _, err := os.Stat(configPath); err != nil {
		return info, nil
	}

	info.ProjectRelPath = relPath
	info.ProjectDir = projectDir

	cfg, err := config.Load(projectDir)
	if err != nil {
		return info, nil
	}
	info.Agents = cfg.Agents.Enabled

	return info, nil
}

// resolveSourceDir makes a source_dir from config absolute.
// If it's already absolute, return as-is. Otherwise resolve relative to .ateam's parent.
func resolveSourceDir(ateamRoot, sourceDir string) string {
	if filepath.IsAbs(sourceDir) {
		return sourceDir
	}
	return filepath.Join(filepath.Dir(ateamRoot), sourceDir)
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
