package root

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ateam/defaults"
	"github.com/ateam/internal/config"
	"github.com/ateam/internal/gitutil"
	"github.com/ateam/internal/projectinfo"
	"github.com/ateam/internal/promptdata"
	"github.com/ateam/internal/prompts/assembler"
	"github.com/ateam/internal/runtime"
)

const (
	// DirName is the per-project and per-user ateam directory.
	DirName = runtime.DirName
	// ConfigFile is the configuration file inside every DirName layer.
	ConfigFile = runtime.ConfigFile

	legacyOrgDirName = ".ateamorg"
	legacyConfigFile = "config.toml"
)

// ResolvedEnv holds all resolved paths for the current invocation.
//
// Three base dirs drive everything:
//   - HomeDir:    the user's .ateam/ (~/.ateam or $ATEAM_HOME): lowest
//     user-controlled config layer, and user state under state/
//   - ProjectDir: the project's .ateam/ (state, config, prompts, artifacts);
//     empty in scratch mode
//   - WorkDir:    the agent's cwd (what code it reads/edits)
//
// Layers lists every config directory consulted, lowest precedence first:
// HomeDir, the .ateam/ of each ancestor of the project (outermost first),
// then ProjectDir. GitRepoDir is derived from WorkDir at resolution time.
type ResolvedEnv struct {
	HomeDir     string
	Layers      []string
	ProjectDir  string
	ProjectName string          // project { name } of the project layer; the project's id
	KeychainKey string          // from the registry: stable id for project-scoped keychain secrets
	SourceDir   string          // absolute path to project root (parent of .ateam/) — informational
	WorkDir     string          // absolute path to the agent's working directory
	GitRepoDir  string          // `git rev-parse --show-toplevel` from WorkDir; "" if not a repo
	Config      *config.Config  // merged settings of every layer
	Runtime     *runtime.Config // the loaded configuration (agents, profiles, settings, provenance)

	// projectMeta caches the `git log -1` + `git status --porcelain` result so
	// multi-role commands fork git at most once. nil = uncached; a sentinel
	// with empty CommitHash = "we tried, got nothing" (don't retry).
	projectMeta *gitutil.ProjectMeta

	// quickOrientation caches the pre-rendered Markdown produced by
	// projectinfo.Info.Markdown(). nil = not yet evaluated; an empty string
	// pointer = "we tried, got nothing". Populated lazily by
	// NewProjectInfoParams; see plans/Feature_TokenReduction.md (Phase 0.5).
	quickOrientation *string

	// assemblerOverride is test-only — see SetAssemblerOverride.
	assemblerOverride *assembler.MultiAnchorAssembler
}

func (e *ResolvedEnv) SupervisorDir() string {
	return filepath.Join(e.ProjectDir, "supervisor")
}

// RoleReportPath returns the path of the role's report file (shared/report/<role>.md).
func (e *ResolvedEnv) RoleReportPath(roleID string) string {
	return filepath.Join(e.SharedDir(), "report", roleID+".md")
}

// ReviewPath returns the path of the supervisor review file.
func (e *ResolvedEnv) ReviewPath() string {
	return filepath.Join(e.SharedDir(), "review.md")
}

// VerifyPath returns the path of the supervisor verification file.
func (e *ResolvedEnv) VerifyPath() string {
	return filepath.Join(e.SharedDir(), "verify.md")
}

// AutoSetupPath returns the path of the supervisor's auto-setup overview file.
func (e *ResolvedEnv) AutoSetupPath() string {
	return filepath.Join(e.SharedDir(), "auto_setup.md")
}

// HasProject reports whether this env resolved an actual ateam project
// (a .ateam/ whose ateam.hcl names a project), as opposed to scratch mode.
func (e *ResolvedEnv) HasProject() bool {
	return e != nil && e.ProjectDir != "" && e.Config != nil
}

// ConfigLayers returns the config-only layers (every layer but the
// project), lowest precedence first: HomeDir, then the ancestors.
func (e *ResolvedEnv) ConfigLayers() []string {
	var out []string
	for _, l := range e.Layers {
		if l != e.ProjectDir {
			out = append(out, l)
		}
	}
	return out
}

// HomeStateDir returns <HomeDir>/state: scratch-mode state, registry,
// caches and agent config dirs.
func (e *ResolvedEnv) HomeStateDir() string {
	if e.HomeDir == "" {
		return ""
	}
	return filepath.Join(e.HomeDir, runtime.StateSubdir)
}

// StateDir returns the directory that owns state.sqlite, logs/, and runtime/
// for the current invocation: the project's .ateam/ inside a project, else
// <HomeDir>/state/ (scratch mode, so `ateam exec` / `ateam parallel` from
// arbitrary cwds still record into a stable, user-owned location).
func (e *ResolvedEnv) StateDir() string {
	if e.ProjectDir != "" {
		return e.ProjectDir
	}
	return e.HomeStateDir()
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

// ProjectDBPath returns the path to the state.sqlite database, under StateDir.
func (e *ResolvedEnv) ProjectDBPath() string {
	return filepath.Join(e.StateDir(), "state.sqlite")
}

// SharedDir returns the cross-agent artifact directory: .ateam/shared/.
// Promoted role reports and supervisor outputs land as flat files under this
// path (shared/report/<role>.md, shared/review.md, shared/verify.md,
// shared/auto_setup.md). Code sessions are the exception — they keep a
// per-session subdir at shared/code/<exec_id>/ because they produce many
// files per run. Callers are responsible for creating subdirs they need.
func (e *ResolvedEnv) SharedDir() string {
	return filepath.Join(e.ProjectDir, "shared")
}

// Assembler returns a prompt Assembler with the project → layers → embedded
// anchor chain. New on each call (cheap — just an FS handle per anchor);
// callers should cache if they need multiple lookups within one operation.
//
// Tests can install an in-memory assembler via SetAssemblerOverride
// (avoids needing on-disk fixtures for the embedded-defaults FS).
func (e *ResolvedEnv) Assembler() *assembler.MultiAnchorAssembler {
	if e.assemblerOverride != nil {
		return e.assemblerOverride
	}
	return assembler.New(assembler.BuildAnchors(e.ProjectDir, e.ConfigLayers(), defaults.FS))
}

// SetAssemblerOverride installs a test-only assembler that bypasses the
// anchor chain. Production code never calls this; tests use it to feed an
// fstest.MapFS-backed assembler.
func (e *ResolvedEnv) SetAssemblerOverride(a *assembler.MultiAnchorAssembler) {
	e.assemblerOverride = a
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
		// roles.* — project-wide role-set state. The factory-curated
		// args.* namespace is seeded empty here so verb factories can
		// mutate it without re-allocating (BaseVars is a value but the
		// inner maps are references).
		Args:  map[string]string{},
		Roles: e.rolesEnabledMap(),
		Ateam: map[string]string{
			"own_readme":        defaults.SelfDocs["README"],
			"own_commands":      defaults.SelfDocs["COMMANDS"],
			"own_config":        defaults.SelfDocs["CONFIG"],
			"own_isolation":     defaults.SelfDocs["ISOLATION"],
			"own_roles":         defaults.SelfDocs["ROLES"],
			"auto_roles_marker": promptdata.AutoRolesMarker,
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
func (e *ResolvedEnv) NewProjectInfoParams(role, action string) promptdata.ProjectInfoParams {
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
	return promptdata.ProjectInfoParams{
		HomeDir:          e.HomeDir,
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

// ProjectID returns the project's identifier in the state db: its name.
// Empty in scratch mode.
func (e *ResolvedEnv) ProjectID() string {
	return e.ProjectName
}

// rolesEnabledMap seeds the {{roles.*}} namespace. Currently exposes
// `roles.enabled` — the comma-separated list of enabled role IDs from
// the project config (e.g. "code.bugs,security,test.gaps"). Empty
// string in scratch / unconfigured mode so prompts that reference
// {{roles.enabled}} render cleanly there too.
func (e *ResolvedEnv) rolesEnabledMap() map[string]string {
	out := map[string]string{"enabled": ""}
	if e == nil || e.Config == nil || len(e.Config.Roles) == 0 {
		return out
	}
	ids := make([]string, 0, len(e.Config.Roles))
	for id, status := range e.Config.Roles {
		if config.IsRoleEnabled(status) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	out["enabled"] = strings.Join(ids, ",")
	return out
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

// Resolve discovers the user's home layer and the project around the
// current directory and loads the configuration.
//
//   - homeOverride: path of the per-user .ateam/ to use instead of
//     $ATEAM_HOME / ~/.ateam (any directory; it need not be named .ateam)
//   - projectOverride: a project root, its .ateam/, or any path inside the
//     project; discovery walks up from there instead of from cwd
//
// Scratch mode (no project found) returns a project-less env with the
// home layer only; callers that strictly require a project check
// env.ProjectDir. A project override that finds no project is an error.
func Resolve(homeOverride, projectOverride string) (*ResolvedEnv, error) {
	start := realPath(mustGetwd())
	if projectOverride != "" {
		abs, err := filepath.Abs(projectOverride)
		if err != nil {
			return nil, fmt.Errorf("--project %q: cannot resolve absolute path: %w", projectOverride, err)
		}
		start = realPath(abs)
	}
	env, err := resolveFrom(homeOverride, start)
	if err != nil {
		return nil, err
	}
	if projectOverride != "" && env.ProjectDir == "" {
		return nil, fmt.Errorf("--project %q: no %s/ with a project block found at or above this path", projectOverride, DirName)
	}
	if err := env.resolveWorkDir(""); err != nil {
		return nil, err
	}
	return env, nil
}

// Lookup is Resolve for commands where a project is optional. Kept as a
// separate name so call sites read as intended.
func Lookup(homeOverride, projectOverride string) (*ResolvedEnv, error) {
	return Resolve(homeOverride, projectOverride)
}

// LookupFrom discovers the project from the given starting path (instead
// of cwd) with the default home layer. WorkDir is seeded from start.
func LookupFrom(start string) (*ResolvedEnv, error) {
	env, err := resolveFrom("", realPath(start))
	if err != nil {
		return nil, err
	}
	if err := env.resolveWorkDir(start); err != nil {
		return nil, err
	}
	return env, nil
}

func resolveFrom(homeOverride, start string) (*ResolvedEnv, error) {
	home, err := resolveHome(homeOverride)
	if err != nil {
		return nil, err
	}
	projectDir, ancestors, err := discover(start, home)
	if err != nil {
		return nil, err
	}

	env := &ResolvedEnv{HomeDir: home, ProjectDir: projectDir}
	env.Layers = append([]string{home}, ancestors...)
	if projectDir != "" {
		env.Layers = append(env.Layers, projectDir)
	}
	rt, err := runtime.Load(env.Layers)
	if err != nil {
		return nil, err
	}
	env.Runtime = rt
	env.Config = &rt.Settings

	if projectDir != "" {
		env.ProjectName = rt.Settings.Project.Name
		env.SourceDir = filepath.Dir(projectDir)
		if entry, ok := LookupRegistry(home, env.ProjectName); ok {
			env.KeychainKey = entry.KeychainKey
		}
		// Best-effort: keep .ateam/.gitignore in sync with required entries so
		// projects created by an older binary self-heal. Never block ateam
		// on a gitignore update.
		if err := EnsureProjectGitignore(projectDir); err != nil {
			fmt.Fprintf(os.Stderr, "ateam: warning: cannot update %s/.gitignore: %v\n", DirName, err)
		}
	}
	return env, nil
}

// resolveHome returns the absolute per-user ateam directory.
func resolveHome(override string) (string, error) {
	if override != "" {
		abs, err := filepath.Abs(override)
		if err != nil {
			return "", fmt.Errorf("--home %q: cannot resolve absolute path: %w", override, err)
		}
		return realPath(abs), nil
	}
	home := runtime.HomeDir()
	if home == "" {
		return "", fmt.Errorf("cannot determine the user's ateam directory: set %s or HOME", runtime.HomeEnv)
	}
	return realPath(home), nil
}

// discover walks up from start collecting every .ateam/ directory other
// than home. The nearest one whose ateam.hcl names a project is the
// project; every other one is a config layer, returned outermost first.
// A legacy layout (.ateamorg/, or a config.toml inside .ateam/) is an
// error that explains the move.
func discover(start, home string) (projectDir string, ancestors []string, err error) {
	// A ~/.ateamorg from a previous version is only an error when the home
	// layer is the default one (an explicit ATEAM_HOME / --home means the
	// user already chose a location, and tests and containers do that).
	// The walk below applies the same exemption when it passes through $HOME.
	defaultHome := os.Getenv(runtime.HomeEnv) == "" && home == realPath(runtime.HomeDir())
	userHome := ""
	if h, herr := os.UserHomeDir(); herr == nil && h != "" {
		userHome = realPath(h)
	}
	legacyOrgAt := func(dir string) bool {
		_, statErr := os.Stat(filepath.Join(dir, legacyOrgDirName))
		return statErr == nil
	}

	dir := filepath.Clean(start)
	seen := map[string]bool{}
	for {
		candidate := ""
		if filepath.Base(dir) == DirName {
			candidate = dir
		} else if info, statErr := os.Stat(filepath.Join(dir, DirName)); statErr == nil && info.IsDir() {
			candidate = filepath.Join(dir, DirName)
		}
		if (defaultHome || dir != userHome) && legacyOrgAt(dir) {
			return "", nil, legacyLayoutError(filepath.Join(dir, legacyOrgDirName))
		}
		if candidate != "" {
			candidate = realPath(candidate)
			if candidate != home && !seen[candidate] {
				seen[candidate] = true
				if _, statErr := os.Stat(filepath.Join(candidate, legacyConfigFile)); statErr == nil {
					return "", nil, legacyLayoutError(filepath.Join(candidate, legacyConfigFile))
				}
				if projectDir == "" && runtime.ProjectName(candidate) != "" {
					projectDir = candidate
				} else {
					ancestors = append([]string{candidate}, ancestors...)
				}
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if defaultHome && userHome != "" && legacyOrgAt(userHome) {
		return "", nil, legacyLayoutError(filepath.Join(userHome, legacyOrgDirName))
	}
	return projectDir, ancestors, nil
}

func legacyLayoutError(path string) error {
	return fmt.Errorf("legacy layout found at %s.\n"+
		"This version uses one .ateam/ directory per project and ~/.ateam for user-wide config,\n"+
		"with a single ateam.hcl replacing config.toml and runtime.hcl. To migrate:\n"+
		"  1. move ~/.ateamorg/runtime.hcl to ~/.ateam/ateam.hcl and ~/.ateamorg/prompts to ~/.ateam/prompts\n"+
		"  2. in each project, replace .ateam/config.toml with project/role/supervisor blocks in .ateam/ateam.hcl\n"+
		"     (ateam init --name NAME in a copy shows the shape) and rename runtime.hcl to ateam.hcl\n"+
		"  3. delete the .ateamorg/ directory and config.toml files\n"+
		"scripts/migrate-ateamorg.sh in the ateam repository does these steps. See CONFIG.md for the layout", path)
}

// ResolveStreamPath resolves a relative stream_file path recorded in the
// state db to an absolute path under stateDir. Absolute paths pass through.
func ResolveStreamPath(stateDir, sf string) string {
	if sf == "" || filepath.IsAbs(sf) || stateDir == "" {
		return sf
	}
	return filepath.Join(stateDir, sf)
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
