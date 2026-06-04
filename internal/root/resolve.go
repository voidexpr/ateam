package root

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ateam/defaults"
	"github.com/ateam/internal/config"
	"github.com/ateam/internal/gitutil"
	"github.com/ateam/internal/migrate"
	"github.com/ateam/internal/projectinfo"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/prompts/assembler"
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

	// quickOrientation caches the pre-rendered Markdown produced by
	// projectinfo.Info.Markdown(). nil = not yet evaluated; an empty string
	// pointer = "we tried, got nothing". Populated lazily by
	// NewProjectInfoParams; see plans/Feature_TokenReduction.md (Phase 0.5).
	quickOrientation *string
}

func (e *ResolvedEnv) SupervisorDir() string {
	return filepath.Join(e.ProjectDir, "supervisor")
}

// RoleReportPath returns the canonical v1 path for the role's report file
// (shared/report/<role>.md). Auto-migration (default-on) collapses the
// legacy roles/<role>/report.md and the pre-flat shared/report/<role>/<role>.md
// before this is consulted; ATEAM_NO_MIGRATE=1 users must place files at
// the v1 path themselves.
func (e *ResolvedEnv) RoleReportPath(roleID string) string {
	return filepath.Join(e.SharedDir(), "report", roleID+".md")
}

// ReviewPath returns the canonical v1 path for the supervisor review file.
// Auto-migration handles the legacy supervisor/review.md and the pre-flat
// shared/review/review.md locations.
func (e *ResolvedEnv) ReviewPath() string {
	return filepath.Join(e.SharedDir(), "review.md")
}

// VerifyPath returns the canonical v1 path for the supervisor verification
// file. Auto-migration handles the legacy supervisor/verify.md and the
// pre-flat shared/verify/verify.md locations.
func (e *ResolvedEnv) VerifyPath() string {
	return filepath.Join(e.SharedDir(), "verify.md")
}

// AutoSetupPath returns the canonical v1 path for the supervisor's
// auto-setup overview file. Auto-migration handles the legacy
// setup_overview.md and the pre-flat shared/auto_setup/auto_setup.md
// locations.
func (e *ResolvedEnv) AutoSetupPath() string {
	return filepath.Join(e.SharedDir(), "auto_setup.md")
}

// HasProject reports whether this env resolved an actual ateam project
// (config-bearing .ateam/ dir), as opposed to scratch mode where only
// an org is available. Centralizes the `ProjectDir != "" && Config != nil`
// predicate cmds were duplicating: a non-empty ProjectDir without a
// loaded Config means the project is broken or never initialized, which
// callers must treat as "not a project."
func (e *ResolvedEnv) HasProject() bool {
	return e != nil && e.ProjectDir != "" && e.Config != nil
}

// StateDir returns the directory that owns state.sqlite, logs/, and runtime/
// for the current invocation. Inside an ateam project this is ProjectDir
// (.ateam/). Outside one — scratch mode — this is OrgDir (.ateamorg/), so
// `ateam exec` / `ateam parallel` from arbitrary cwds still record into a
// stable, user-owned location instead of littering throwaway dirs.
// Returns "" only when neither is set (treat as a hard error at the caller).
func (e *ResolvedEnv) StateDir() string {
	if e.ProjectDir != "" {
		return e.ProjectDir
	}
	return e.OrgDir
}

// LogsDir returns the per-exec_id forensic log directory. Holds agent.jsonl,
// stderr.out, settings.json, prompt.md, cmd.md.
func (e *ResolvedEnv) LogsDir(execID int64) string {
	return filepath.Join(e.StateDir(), "logs", strconv.FormatInt(execID, 10))
}

// RuntimeDir returns the per-exec_id agent-writable output directory. Files
// the agent writes here are cloned to canonical destinations on success.
func (e *ResolvedEnv) RuntimeDir(execID int64) string {
	return filepath.Join(e.StateDir(), "runtime", strconv.FormatInt(execID, 10))
}

// ProjectDBPath returns the path to the state.sqlite database. Anchored at
// StateDir so scratch-mode invocations resolve to <OrgDir>/state.sqlite.
func (e *ResolvedEnv) ProjectDBPath() string {
	return filepath.Join(e.StateDir(), "state.sqlite")
}

// SharedDir returns the v1 cross-agent artifact directory: .ateam/shared/.
// Promoted role reports and supervisor outputs land as flat files under this
// path (shared/report/<role>.md, shared/review.md, shared/verify.md,
// shared/auto_setup.md). Code sessions are the exception — they keep a
// per-session subdir at shared/code/<exec_id>/ because they produce many
// files per run. Callers are responsible for creating subdirs they need.
func (e *ResolvedEnv) SharedDir() string {
	return filepath.Join(e.ProjectDir, "shared")
}

// Assembler returns a v1 prompt Assembler with the standard project →
// org → embedded anchor chain. New on each call (cheap — just an FS handle
// per anchor); callers should cache if they need multiple lookups within
// one operation.
func (e *ResolvedEnv) Assembler() *assembler.Assembler {
	return assembler.New(assembler.BuildAnchors(e.ProjectDir, e.OrgDir, defaults.FS))
}

// BuildAssemblerVars produces the per-namespace variable map the new
// template engine resolves against. `promptPath` is the full v1 path
// (e.g. "report/security" or "review"); the trailing segment becomes
// {{prompt.name}}, the leading segment becomes {{prompt.action}}.
//
// roleLabel is the human-friendly identifier used in the project info
// block — typically "role security" for roles or "the supervisor" for
// singletons. action is the OutputKind verb (report/code/review/...).
// They're separate from promptPath because the project info block has
// historically formatted them differently from the file-system path.
//
// Empty values are populated with sensible defaults so a prompt that
// references {{exec.id}} on a preview call (which has no exec ID yet)
// just renders the empty string instead of erroring.
func (e *ResolvedEnv) BuildAssemblerVars(promptPath, roleLabel, action string) assembler.MapVars {
	parts := strings.Split(promptPath, "/")
	promptName := promptPath
	promptAction := action
	if len(parts) > 1 {
		promptName = parts[len(parts)-1]
		if promptAction == "" {
			promptAction = parts[0]
		}
	}
	vars := assembler.MapVars{
		Prompt: map[string]string{
			"name":   promptName,
			"path":   promptPath,
			"action": promptAction,
		},
		Project: map[string]string{
			"name":      e.ProjectName,
			"root":      e.SourceDir,
			"full_path": e.SourceDir,
			"dir":       filepath.Base(e.SourceDir),
		},
		Exec: map[string]string{
			// AgentExecutor-deferred keys: each renders to itself (the
			// dotted-form placeholder) so the assembled prompt still
			// carries `{{exec.id}}` etc. for runner.TemplateVars.Replacer
			// to fill at exec time (see internal/runner/template.go).
			// Resolving them to "" here would consume the placeholder
			// before the runner can populate it, leaving the agent with
			// a blank value. The engine writes substitution values
			// verbatim and advances past the closing brace (no re-scan),
			// so substituting the directive with the same string is
			// safe — it doesn't loop. On a preview call (no runner) the
			// placeholder shape mirrors the source, which honestly
			// signals "resolved at run time."
			"id":                   "{{exec.id}}",
			"batch":                "{{exec.batch}}",
			"timestamp":            "{{exec.timestamp}}",
			"profile":              "{{exec.profile}}",
			"agent":                "{{exec.agent}}",
			"model":                "{{exec.model}}",
			"effort":               "{{exec.effort}}",
			"max_budget_usd":       "{{exec.max_budget_usd}}",
			"max_budget_usd_batch": "{{exec.max_budget_usd_batch}}",
			"subrun_args":          "{{exec.subrun_args}}",
			"output_dir":           "{{exec.output_dir}}",
			"output_file":          "{{exec.output_file}}",
			"prompt_file":          "{{exec.prompt_file}}",
			// Assembly-time keys: filled by the caller before Assemble. Empty
			// default lets the engine render `{{exec.debug_context}}` to ""
			// for prompts that don't use it instead of hard-erroring.
			"debug_context":              "",
			"auto_roles_commands_output": "",
		},
		Container: map[string]string{
			"type": "{{container.type}}",
			"name": "{{container.name}}",
		},
		// Role-set computations. `reports` is currently produced by the
		// assemble helpers (formatReportsBlock in cmd/review_assemble.go) and appended after
		// assembly, not consumed via {{role.reports}}. Seed it empty so the
		// engine doesn't error on user prompts that still reference the
		// legacy `{{ROLE_REPORTS}}` (compat shim routes it through here).
		Role: map[string]string{
			"reports": "",
		},
		Ateam: map[string]string{
			"own_readme":        defaults.SelfDocs["README"],
			"own_commands":      defaults.SelfDocs["COMMANDS"],
			"own_config":        defaults.SelfDocs["CONFIG"],
			"own_isolation":     defaults.SelfDocs["ISOLATION"],
			"own_roles":         defaults.SelfDocs["ROLES"],
			"auto_roles_marker": prompts.AutoRolesMarker,
		},
		EnvLookup: os.LookupEnv,
	}
	// project.info used to live here as a static var; it's now produced by
	// {{dynamic.project_info}} (registered on the bundle's Dynamics map)
	// so the role/action context can shape the output at resolve time.

	// {{git.*}} surfaces repo facts to prompts. Queried in WorkDir for
	// alignment with project.info (same agent-cwd convention; see
	// NewProjectInfoParams). For non-git work-dirs every helper returns ""
	// (or "false" for Dirty), so prompts render cleanly outside a repo.
	repo := gitutil.TopLevel(e.WorkDir)
	if repo != "" {
		repo = filepath.Base(repo)
	}
	vars.Git = map[string]string{
		"repo":       repo,
		"branch":     gitutil.CurrentBranch(e.WorkDir),
		"commit":     gitutil.HeadHash(e.WorkDir),
		"head_short": gitutil.HeadShort(e.WorkDir),
		"dirty":      gitutil.Dirty(e.WorkDir),
	}

	return vars
}

// NewProjectInfoParams builds a ProjectInfoParams from the resolved environment.
// Git metadata is queried in WorkDir (the agent's cwd) so the recorded HEAD
// and uncommitted-files list reflect the code the agent will actually operate
// on, not the parent of .ateam/. See also runner.go's HeadHash note.
//
// The metadata is cached on the env after the first call: multi-role commands
// (e.g. `ateam report`) build pinfo once per role and would otherwise fork
// `git log` + `git status` N times for unchanged repo state.
//
// The pre-rendered Markdown from projectinfo.Info.Markdown() is always
// attached to QuickOrientation (Phase 0.5 — see plans/Feature_TokenReduction.md).
// Collection failures degrade gracefully to an empty string.
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
	if e.quickOrientation == nil {
		e.quickOrientation = collectQuickOrientation(e.WorkDir, meta)
	}
	return prompts.ProjectInfoParams{
		OrgDir:           e.OrgDir,
		ProjectDir:       e.ProjectDir,
		ProjectName:      e.ProjectName,
		WorkDir:          e.WorkDir,
		GitRepoDir:       e.GitRepoDir,
		Role:             role,
		Action:           action,
		Meta:             meta,
		QuickOrientation: *e.quickOrientation,
	}
}

// collectQuickOrientation renders the project-info "Quick orientation" block,
// reusing the supplied ProjectMeta to avoid forking git log/status a second
// time. Returns a pointer to an empty string when collection fails, so callers
// can use a nil cache pointer to mean "not yet evaluated".
func collectQuickOrientation(workDir string, meta *gitutil.ProjectMeta) *string {
	empty := ""
	info, err := projectinfo.CollectWithMeta(workDir, meta)
	if err != nil || info == nil {
		return &empty
	}
	md := info.Markdown()
	return &md
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
	// Resolve symlinks so comparisons against env.ProjectDir / env.SourceDir
	// (both realPath'd at discovery time) use the same canonical form.
	// Without this, a symlinked cwd inside the project is misclassified as
	// "outside the project tree" by the cwd-in-project check.
	target = realPath(target)
	if e.WorkDir == target {
		return nil
	}
	e.WorkDir = target
	e.GitRepoDir = gitutil.TopLevel(target)
	e.projectMeta = nil      // stale: WorkDir changed
	e.quickOrientation = nil // stale: WorkDir changed
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
		projectDir, _ = discoverProject(orgDir, cwd)
	}

	// Scratch mode: org resolved but no .ateam/ above cwd. Return a
	// project-less env. Callers that strictly require a project
	// (report/code/review/verify/…) still check env.ProjectDir.
	if projectDir == "" {
		if orgDir == "" {
			return nil, fmt.Errorf("no .ateamorg/ or .ateam/ found")
		}
		env := &ResolvedEnv{OrgDir: orgDir}
		if err := env.resolveWorkDir(""); err != nil {
			return nil, err
		}
		return env, nil
	}

	if err := applyV1LayoutMigration(projectDir, orgDir); err != nil {
		return nil, err
	}
	// Best-effort: keep .ateam/.gitignore in sync with required entries so
	// projects created by an older binary self-heal (notably for `runtime/`,
	// added after the original gitignore template). Failure here is non-fatal
	// — never block ateam on a gitignore update.
	if err := EnsureProjectGitignore(projectDir); err != nil {
		fmt.Fprintf(os.Stderr, "ateam: warning: cannot update .ateam/.gitignore: %v\n", err)
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

// applyV1LayoutMigration upgrades pre-v1 .ateam / .ateamorg layouts in place.
// Idempotent: a stat-only check skips quickly when nothing to do. On the
// first material change to a directory, a one-line notice is written to
// stderr. Migration failures stop further work; re-running picks up where
// the failed run left off.
//
// Default-on as of the cmd/* rewires that read from shared/ paths. Set
// ATEAM_NO_MIGRATE=1 to suppress (one-off recovery, debugging an old
// layout, or tests that build legacy fixtures).
func applyV1LayoutMigration(projectDir, orgDir string) error {
	if os.Getenv("ATEAM_NO_MIGRATE") == "1" {
		return nil
	}
	for _, dir := range []string{projectDir, orgDir} {
		if dir == "" {
			continue
		}
		res, err := migrate.V1Layout(dir)
		if err != nil {
			return fmt.Errorf("migrate %s: %w", dir, err)
		}
		if res.Changed() {
			fmt.Fprintf(os.Stderr, "ateam: migrated %s to v1 prompts layout (%d moves, %d cleanups)\n",
				dir, len(res.Moved), len(res.RemovedDirs))
		}
		// Warnings are surfaced even when nothing moved — a conflict-only pass
		// (target already exists, a directory blocks a file target, …) records
		// a warning but leaves Changed() false, and the user still needs to
		// reconcile by hand.
		for _, w := range res.Warnings {
			fmt.Fprintf(os.Stderr, "ateam: migration warning: %s\n", w)
		}
	}
	return nil
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

	// Migration and config-load failures are real errors, not the "project not
	// found" case handled above — surface them (matching Resolve) instead of
	// returning a config-less env with a nil error, which would leave callers
	// like cmd/init.go printing a blank project section with no failure shown.
	if err := applyV1LayoutMigration(projectDir, orgDir); err != nil {
		return env, err
	}
	if err := EnsureProjectGitignore(projectDir); err != nil {
		fmt.Fprintf(os.Stderr, "ateam: warning: cannot update .ateam/.gitignore: %v\n", err)
	}

	cfg, err := config.Load(projectDir)
	if err != nil {
		return env, err
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

// resolveOrgPath accepts a path to .ateamorg/ itself, a parent directory
// containing .ateamorg/, or any descendant of one — discovery walks up
// from the given path, matching flag-less discovery semantics.
func resolveOrgPath(path string) (string, error) {
	return resolveSpecialDir(path, OrgDirName, "--org")
}

// resolveProjectPath accepts a path to .ateam/ itself, a project root
// containing .ateam/, or any subdirectory of one — discovery walks up
// so `ateam --project . ...` from `defaults/roles` finds the project,
// just like running with no flag would.
func resolveProjectPath(path string) (string, error) {
	return resolveSpecialDir(path, ProjectDirName, "--project")
}

func resolveSpecialDir(path, target, flag string) (string, error) {
	if filepath.Base(path) == target {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return realPath(path), nil
		}
	}
	// Absolutise before walking — findDirUp stops at the relative "." root
	// otherwise, breaking `--project .` from a subdir of the project.
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("%s %q: cannot resolve absolute path: %w", flag, path, err)
	}
	if found, err := findDirUp(abs, target, ""); err == nil {
		return found, nil
	}
	return "", fmt.Errorf("%s %q: no %s/ found at or above this path", flag, path, target)
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
