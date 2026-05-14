package root

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ateam/internal/config"
	"github.com/ateam/internal/gitutil"
	"github.com/ateam/internal/prompts"
)

const (
	OrgDirName     = ".ateamorg"
	ProjectDirName = ".ateam"
)

// ResolvedEnv holds all resolved paths for the org + project environment.
//
// Three orthogonal base dirs drive everything:
//   - OrgDir:    where the .ateamorg/ lives (org config + project registry)
//   - ProjectDir: the .ateam/ directory (state, config, prompts, artifacts)
//   - WorkDir:   the agent's cwd (what code it reads/edits)
//
// SourceDir (parent of .ateam/) is kept for legacy callers but should be
// treated as informational; WorkDir is what the agent actually runs in.
// GitRepoDir is derived from WorkDir at resolution time (never from config).
type ResolvedEnv struct {
	OrgDir      string // absolute path to .ateamorg/ (empty in org-less mode)
	ProjectDir  string // absolute path to .ateam/
	ProjectName string // from config.toml
	SourceDir   string // absolute path to project root (parent of .ateam/) — informational
	WorkDir     string // absolute path to the agent's working directory
	GitRepoDir  string // `git rev-parse --show-toplevel` from WorkDir; "" if not a repo
	Config      *config.Config

	// projectMeta caches the `git log -1` + `git status --porcelain` result so
	// multi-role commands fork git at most once. nil = uncached; a sentinel
	// with empty CommitHash = "we tried, got nothing" (don't retry).
	projectMeta *gitutil.ProjectMeta
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

func (e *ResolvedEnv) VerifyPath() string {
	return filepath.Join(e.SupervisorDir(), "verify.md")
}

// LogsDir returns the per-exec_id forensic log directory. Holds stream.jsonl,
// stderr.out, settings.json, prompt.md, cmd.md.
func (e *ResolvedEnv) LogsDir(execID int64) string {
	return filepath.Join(e.ProjectDir, "logs", strconv.FormatInt(execID, 10))
}

// RuntimeDir returns the per-exec_id agent-writable output directory. Files
// the agent writes here are cloned to canonical destinations on success.
func (e *ResolvedEnv) RuntimeDir(execID int64) string {
	return filepath.Join(e.ProjectDir, "runtime", strconv.FormatInt(execID, 10))
}

// ProjectDBPath returns the path to the per-project state database.
func (e *ResolvedEnv) ProjectDBPath() string {
	return filepath.Join(e.ProjectDir, "state.sqlite")
}

// NewProjectInfoParams builds a ProjectInfoParams from the resolved environment.
// Git metadata is queried in WorkDir (the agent's cwd) so the recorded HEAD
// and uncommitted-files list reflect the code the agent will actually operate
// on, not the parent of .ateam/. See also runner.go's HeadHash note.
//
// The metadata is cached on the env after the first call: multi-role commands
// (e.g. `ateam report`) build pinfo once per role and would otherwise fork
// `git log` + `git status` N times for unchanged repo state.
func (e *ResolvedEnv) NewProjectInfoParams(role, action string) prompts.ProjectInfoParams {
	if e.projectMeta == nil {
		e.projectMeta, _ = gitutil.GetProjectMeta(e.WorkDir)
		if e.projectMeta == nil {
			// Mark "we tried, got nothing" so we don't retry every call.
			e.projectMeta = &gitutil.ProjectMeta{}
		}
	}
	var meta *gitutil.ProjectMeta
	if e.projectMeta.CommitHash != "" {
		meta = e.projectMeta
	}
	return prompts.ProjectInfoParams{
		OrgDir:      e.OrgDir,
		ProjectDir:  e.ProjectDir,
		ProjectName: e.ProjectName,
		WorkDir:     e.WorkDir,
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
	// note: GitRepoDir is NOT derived from config.Git.Repo here. It is set later
	// from WorkDir via gitutil.TopLevel — git is properly a function of "where
	// the agent operates," not "where state lives." config.Git.Repo is kept in
	// config.toml for informational/forensic use but never read at runtime.
}

// resolveWorkDir populates WorkDir and GitRepoDir from an explicit override or
// the current working directory. Empty override → defaults to os.Getwd().
// GitRepoDir is derived from WorkDir via gitutil.TopLevel; "" if WorkDir is
// not inside a git repo or the git CLI is unavailable. Skips the git subprocess
// when WorkDir is unchanged from a prior call.
func (e *ResolvedEnv) resolveWorkDir(workDirOverride string) error {
	target := mustGetwd()
	if workDirOverride != "" {
		abs, err := filepath.Abs(workDirOverride)
		if err != nil {
			return fmt.Errorf("cannot resolve --work-dir: %w", err)
		}
		target = abs
	}
	if e.WorkDir == target {
		return nil
	}
	e.WorkDir = target
	e.GitRepoDir = gitutil.TopLevel(target)
	e.projectMeta = nil // stale: WorkDir changed
	return nil
}

// OverrideWorkDir resets WorkDir to the given path and re-derives GitRepoDir.
// Used by cmd packages after parsing the persistent --work-dir flag.
func (e *ResolvedEnv) OverrideWorkDir(workDirOverride string) error {
	return e.resolveWorkDir(workDirOverride)
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
//
// The overrides are location overrides (not name lookups):
//   - orgOverride: path to .ateamorg/ itself or its parent
//   - projectOverride: path to .ateam/ itself or its parent (project root)
//
// Either may be set independently; with --project alone, the org is
// auto-discovered by walking up from the project root.
func Resolve(orgOverride, projectOverride string) (*ResolvedEnv, error) {
	cwd := realPath(mustGetwd())

	var projectDir, orgDir string
	var err error

	if projectOverride != "" {
		projectDir, err = resolveProjectPath(projectOverride)
		if err != nil {
			return nil, err
		}
	}

	if orgOverride != "" {
		orgDir, err = resolveOrgPath(orgOverride)
		if err != nil {
			return nil, err
		}
	}

	if orgDir == "" {
		if projectDir != "" {
			orgDir, _ = FindOrg(filepath.Dir(projectDir))
		} else {
			orgDir, _ = FindOrg(cwd)
		}
	}

	if projectDir == "" {
		projectDir, err = discoverProject(orgDir, cwd)
		if err != nil {
			return nil, err
		}
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
	if err := env.resolveWorkDir(""); err != nil {
		return nil, err
	}

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
	// Seed WorkDir from `start`, not the process cwd — callers like
	// eval --dirs pass an explicit base/candidate path and would
	// otherwise attach the wrong execution directory.
	_ = env.resolveWorkDir(cwd)

	projectDir, err := discoverProject(orgDir, cwd)
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
// When orgOverride or projectOverride are non-empty, delegates to Resolve for explicit resolution.
func Lookup(orgOverride, projectOverride string) (*ResolvedEnv, error) {
	if orgOverride != "" || projectOverride != "" {
		return Resolve(orgOverride, projectOverride)
	}
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

// discoverProject finds a project directory: when orgDir is set, prefer the
// state-dir mapping (cwd inside .ateamorg/projects/<id>/), then fall back to
// walking up from cwd. Without an org, just walks up from cwd.
func discoverProject(orgDir, cwd string) (string, error) {
	if orgDir != "" {
		if p, err := resolveProjectFromStateDir(orgDir, cwd); err == nil {
			return p, nil
		}
	}
	return FindProject(cwd)
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

// resolveOrgPath accepts either a path to .ateamorg/ itself or a parent
// directory containing .ateamorg/ as a child.
func resolveOrgPath(path string) (string, error) {
	return resolveSpecialDir(path, OrgDirName, "--org")
}

// resolveProjectPath accepts either a path to .ateam/ itself or a parent
// directory (project root) containing .ateam/ as a child.
func resolveProjectPath(path string) (string, error) {
	return resolveSpecialDir(path, ProjectDirName, "--project")
}

func resolveSpecialDir(path, target, flag string) (string, error) {
	if filepath.Base(path) == target {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return realPath(path), nil
		}
	}
	candidate := filepath.Join(path, target)
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return realPath(candidate), nil
	}
	return "", fmt.Errorf("%s %q: no %s/ found at or under this path", flag, path, target)
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

// IsLegacyStreamFile reports whether streamPath uses the pre-exec_id layout
// (`<TS>_<ACTION>_stream.jsonl` next to its sibling _exec.md / _stderr.log /
// _settings.json files). Callers branch on this to decide whether to walk a
// shared parent directory (new layout) or strip the suffix and append per-kind
// suffixes (legacy layout).
func IsLegacyStreamFile(streamPath string) bool {
	return strings.HasSuffix(streamPath, "_stream.jsonl")
}

// HistoryTimestampSkew is the ±window FindHistoryFileWithSkew searches for
// archived files whose write timestamp may have drifted from the run's
// started_at by a second or two.
var HistoryTimestampSkew = []time.Duration{
	0,
	time.Second, -time.Second,
	2 * time.Second, -2 * time.Second,
	5 * time.Second, -5 * time.Second,
}

// FindHistoryFileWithSkew looks for `<formattedTS>.<suffix>` in dir, trying
// each offset in HistoryTimestampSkew. Returns the absolute path of the first
// match, or "" if nothing is found.
func FindHistoryFileWithSkew(dir string, ts time.Time, suffix string) string {
	for _, off := range HistoryTimestampSkew {
		candidate := filepath.Join(dir, ts.Add(off).Format(historyTimestampLayout)+"."+suffix)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

// historyTimestampLayout matches the format used by archivePrompt /
// prepareOutputFile in the legacy layout. Kept as a package-private constant
// to avoid pulling internal/display into root.
const historyTimestampLayout = "2006-01-02_15-04-05"

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
