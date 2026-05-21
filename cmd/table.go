package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/container"
	"github.com/ateam/internal/display"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/ateam/internal/runtime"
	"github.com/ateam/internal/secret"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

func cmdContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// errNoReview is the canonical error for commands that need the supervisor
// review file before they can proceed.
func errNoReview(reviewPath string) error {
	return fmt.Errorf("no review found at %s; run 'ateam review' first", reviewPath)
}

// ExitError is returned by commands that need to exit with a specific non-zero code.
type ExitError struct {
	Code int
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("exit status %d", e.Code)
}

func newTable() *tabwriter.Writer {
	return tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
}

func relPath(cwd, path string) string {
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}
	rel, err := filepath.Rel(cwd, path)
	if err != nil {
		return path
	}
	return rel
}

func printDone(r runner.RunSummary) {
	costSuffix := ""
	if c := display.FmtCost(r.Cost); c != "" {
		costSuffix = ", " + c
	}
	fmt.Printf("Done (%s%s)\n\n", display.FormatDuration(r.Duration), costSuffix)
}

// openProjectDB opens the per-project state.sqlite in .ateam/, creating it
// if it doesn't exist. Returns an error if the project has no ProjectDir.
//
// On first open after upgrading to the logs/<exec_id>/ layout this also runs
// MigrateLogsLayout — sentinel-guarded, so subsequent calls are no-ops.
func openProjectDB(env *root.ResolvedEnv) (*calldb.CallDB, error) {
	if env.ProjectDir == "" {
		return nil, fmt.Errorf("no project context — run 'ateam init' first")
	}
	dbPath := env.ProjectDBPath()
	db, err := calldb.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("cannot open project database %s: %w", dbPath, err)
	}
	if err := root.MigrateLogsLayout(env, db); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: log layout migration: %v\n", err)
	}
	return db, nil
}

// requireProjectDB opens an existing per-project state.sqlite.
// Returns an error if the database does not exist.
//
// Like openProjectDB, this also runs MigrateLogsLayout so read-only commands
// (ateam ps, cat, inspect, resume, tail, cost) trigger the migration when
// they touch the DB.
func requireProjectDB(env *root.ResolvedEnv) (*calldb.CallDB, error) {
	if env.ProjectDir == "" {
		return nil, fmt.Errorf("no project context — run 'ateam init' first")
	}
	dbPath := env.ProjectDBPath()
	db, err := calldb.OpenIfExists(dbPath)
	if err != nil {
		return nil, fmt.Errorf("cannot open project database %s: %w", dbPath, err)
	}
	if db == nil {
		return nil, fmt.Errorf("project database not found at %s — run a command like 'ateam exec' or 'ateam report' first", dbPath)
	}
	if err := root.MigrateLogsLayout(env, db); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: log layout migration: %v\n", err)
	}
	return db, nil
}

// newRunner creates a Runner using the resolved profile from runtime.hcl.
// roleID is optional — used for role-specific Dockerfile resolution.
func newRunner(env *root.ResolvedEnv, profileName, roleID string, dockerAutoSetup bool) (*runner.Runner, error) {
	rtCfg, err := runtime.Load(env.ProjectDir, env.OrgDir)
	if err != nil {
		return nil, fmt.Errorf("cannot load runtime.hcl: %w", err)
	}

	prof, ac, cc, err := rtCfg.ResolveProfile(profileName)
	if err != nil {
		// If the profile name matches a known agent, treat it as an agent
		// shorthand (like --agent). This allows config.toml [profiles.roles]
		// to reference agent names directly: critical_code_reviewer = "codex"
		if _, ok := rtCfg.Agents[profileName]; ok {
			return newRunnerFromAgent(env, profileName)
		}
		return nil, err
	}

	// Validate secrets: require credentials for container runs and inside containers
	// (where agents can't use interactive login). On host without containers,
	// agents handle their own auth (macOS Keychain, interactive login).
	// IsolateCredentials always runs to strip competing alternatives from the
	// agent env — this is safe even when no secrets are configured.
	resolver := secretResolver(env, secret.DefaultBackend())
	if (cc != nil && cc.Type != "none") || runner.IsInContainer() {
		if err := secret.ValidateSecrets(ac, resolver); err != nil {
			return nil, err
		}
	}
	logIsolationResults(os.Stderr, secret.IsolateCredentials(ac, resolver))

	r := runnerFromAgentConfig(env, ac)
	r.Profile = profileName
	r.ProjectID = env.ProjectID()
	r.ExtraArgs = append(r.ExtraArgs, prof.AgentExtraArgs...)
	if err := preflightContainerSupportsWorkDir(cc, env); err != nil {
		return nil, err
	}
	ct, err := buildContainer(cc, prof, env.WorkDir, env.ProjectDir, env.OrgDir, env.GitRepoDir, roleID, dockerAutoSetup)
	if err != nil {
		return nil, err
	}
	// Merge per-project container extras from config.toml [container-extra]
	if ct != nil && env.Config != nil {
		ce := env.Config.ContainerExtra
		ct.ApplyContainerExtra(ce.ExtraArgs, ce.ForwardEnv, ce.Env)
	}
	// Apply agent env (credential isolation + agent-configured env) to the
	// container so that ForwardEnv respects IsolateCredentials decisions.
	if ct != nil {
		ct.ApplyAgentEnv(ac.Env)
	}
	r.Container = ct
	if cc != nil && cc.Type != "none" {
		r.ContainerType = cc.Type
		r.ContainerName = ct.GetContainerName()
	} else {
		r.ContainerType = "none"
	}
	return r, nil
}

// applyContainerNameOverride replaces the container name on a runner's container
// if a --container-name flag was provided. Only effective for container types that support it.
func applyContainerNameOverride(r *runner.Runner, name string) {
	if name == "" || r.Container == nil {
		return
	}
	if r.Container.SetContainerName(name) {
		r.ContainerName = name
	} else {
		fmt.Fprintf(os.Stderr, "Warning: --container-name has no effect for container type %q\n", r.ContainerType)
	}
}

// applyContainerName applies the --container-name CLI flag override, then
// resolves {{CONTAINER_NAME}} from the secret store if the flag was not set.
// Also sets ContainerNameSource for dry-run display.
func applyContainerName(r *runner.Runner, env *root.ResolvedEnv, cliFlag string) error {
	applyContainerNameOverride(r, cliFlag)

	if cliFlag != "" {
		r.ContainerNameSource = runner.ContainerNameSourceCLI
		return nil
	}
	if !strings.Contains(r.ContainerName, "{{CONTAINER_NAME}}") {
		r.ContainerNameSource = runner.ContainerNameSourceConfig
		return nil
	}
	resolver := secretResolver(env, secret.DefaultBackend())
	result := resolver.Resolve("CONTAINER_NAME")
	if result.Found {
		r.ContainerName = result.Value
		if r.Container != nil {
			r.Container.SetContainerName(result.Value)
		}
		if result.Source == "env" {
			r.ContainerNameSource = runner.ContainerNameSourceEnv
		} else {
			r.ContainerNameSource = runner.ContainerNameSourceSecret
		}
		return nil
	}
	return fmt.Errorf("container name not set — use --container-name, ateam secret CONTAINER_NAME=<name> --scope project, or set docker_container in runtime.hcl")
}

// newRunnerFromAgent creates a Runner using a named agent directly (no profile).
func newRunnerFromAgent(env *root.ResolvedEnv, agentName string) (*runner.Runner, error) {
	rtCfg, err := runtime.Load(env.ProjectDir, env.OrgDir)
	if err != nil {
		return nil, fmt.Errorf("cannot load runtime.hcl: %w", err)
	}

	ac, ok := rtCfg.Agents[agentName]
	if !ok {
		return nil, fmt.Errorf("unknown agent %q", agentName)
	}

	// No container — skip hard validation (agent handles its own auth on host).
	// IsolateCredentials still runs to strip competing env vars.
	resolver := secretResolver(env, secret.DefaultBackend())
	if runner.IsInContainer() {
		if err := secret.ValidateSecrets(&ac, resolver); err != nil {
			return nil, err
		}
	}
	logIsolationResults(os.Stderr, secret.IsolateCredentials(&ac, resolver))

	r := runnerFromAgentConfig(env, &ac)
	r.Profile = "a:" + agentName
	r.ProjectID = env.ProjectID()
	r.ContainerType = "none"
	return r, nil
}

func minimalRunnerFromAgentConfig(orgDir string, ac *runtime.AgentConfig) *runner.Runner {
	return &runner.Runner{
		Agent:  buildAgent(ac),
		OrgDir: orgDir,
		Sandbox: runner.SandboxConfig{
			Settings:        ac.Sandbox,
			RWPaths:         ac.RWPaths,
			ROPaths:         ac.ROPaths,
			Denied:          ac.DeniedPaths,
			InsideContainer: ac.SandboxInsideContainer,
		},
		ConfigDir:            ac.ConfigDir,
		ArgsInsideContainer:  ac.ArgsInsideContainer,
		ArgsOutsideContainer: ac.ArgsOutsideContainer,
	}
}

// preflightContainerSupportsWorkDir rejects container profiles when WorkDir
// is outside the project tree (remote-project / worktree mode). Container
// support for that case needs (a) mounting env.ProjectDir at a fixed path
// regardless of host layout and (b) translating template-substituted host
// paths in the rendered prompt — neither lands cleanly in this refactor.
// Host mode handles remote WorkDir fine.
func preflightContainerSupportsWorkDir(cc *runtime.ContainerConfig, env *root.ResolvedEnv) error {
	if cc == nil || cc.Type == "" || cc.Type == "none" {
		return nil
	}
	if env.ProjectDir == "" || env.WorkDir == "" {
		return nil
	}
	if pathInside(env.WorkDir, env.SourceDir) {
		return nil
	}
	return fmt.Errorf("container profile %q does not yet support running with --work-dir outside the project tree (work-dir %q, project root %q). Use a container=none profile, or run from inside the project", cc.Type, env.WorkDir, env.SourceDir)
}

func runnerFromAgentConfig(env *root.ResolvedEnv, ac *runtime.AgentConfig) *runner.Runner {
	// Sandbox grants and Runner.SourceDir follow WorkDir (where the agent
	// actually runs) — not the parent of .ateam/. This ensures --work-dir
	// is honored end-to-end: rw to the worktree, ro to the wider git repo.
	extraWriteDirs := gitWriteDirs(env.WorkDir)
	// Always grant write access to the project's .ateam directory. In typical
	// project-local mode this is redundant (it's inside WorkDir), but in
	// remote-project / worktree mode the agent still writes its runtime
	// output and per-exec runtime/<id>/ files under env.ProjectDir even
	// though that path is outside WorkDir.
	if env.ProjectDir != "" {
		extraWriteDirs = append(extraWriteDirs, env.ProjectDir)
	}
	r := &runner.Runner{
		Agent:       buildAgent(ac),
		ProjectDir:  env.ProjectDir,
		OrgDir:      env.OrgDir,
		SourceDir:   env.WorkDir,
		ProjectName: env.ProjectName,
		Sandbox: runner.SandboxConfig{
			Settings:        ac.Sandbox,
			RWPaths:         ac.RWPaths,
			ROPaths:         ac.ROPaths,
			Denied:          ac.DeniedPaths,
			ExtraWriteDirs:  extraWriteDirs,
			InsideContainer: ac.SandboxInsideContainer,
		},
		ConfigDir:            ac.ConfigDir,
		ArgsInsideContainer:  ac.ArgsInsideContainer,
		ArgsOutsideContainer: ac.ArgsOutsideContainer,
	}
	if env.Config != nil {
		r.Sandbox.ExtraWrite = env.Config.SandboxExtra.AllowWrite
		r.Sandbox.ExtraRead = env.Config.SandboxExtra.AllowRead
		r.Sandbox.ExtraDomains = env.Config.SandboxExtra.AllowDomains
		r.Sandbox.ExtraExcludedCmd = env.Config.SandboxExtra.UnsandboxedCommands
	}
	// Grant read access to the entire git repo when WorkDir is nested within it
	// (monorepo subdir or external worktree of a wider repo).
	if env.GitRepoDir != "" && env.GitRepoDir != env.WorkDir {
		r.Sandbox.ExtraRead = append(r.Sandbox.ExtraRead, env.GitRepoDir)
	}
	return r
}

// newRunnerDefault creates a Runner using the default profile.
func newRunnerDefault(env *root.ResolvedEnv) (*runner.Runner, error) {
	profileName := env.Config.ResolveProfile("", "")
	return newRunner(env, profileName, "", false)
}

// resolveRunnerMinimal builds a Runner without project context (just org dir).
// Docker containers are not supported without project context.
func resolveRunnerMinimal(orgDir, profileFlag, agentFlag string) (*runner.Runner, error) {
	rtCfg, err := runtime.Load("", orgDir)
	if err != nil {
		return nil, fmt.Errorf("cannot load runtime.hcl: %w", err)
	}

	switch {
	case profileFlag != "" && agentFlag != "":
		return nil, fmt.Errorf("--profile and --agent are mutually exclusive")
	case agentFlag != "":
		ac, ok := rtCfg.Agents[agentFlag]
		if !ok {
			return nil, fmt.Errorf("unknown agent %q", agentFlag)
		}
		r := minimalRunnerFromAgentConfig(orgDir, &ac)
		r.Profile = "a:" + agentFlag
		return r, nil
	default:
		if profileFlag == "" {
			profileFlag = "default"
		}
		prof, ac, _, err := rtCfg.ResolveProfile(profileFlag)
		if err != nil {
			return nil, err
		}
		r := minimalRunnerFromAgentConfig(orgDir, ac)
		r.ExtraArgs = append(r.ExtraArgs, prof.AgentExtraArgs...)
		return r, nil
	}
}

// resolveRunner builds a Runner from --profile/--agent flags, falling back to config resolution.
func resolveRunner(env *root.ResolvedEnv, profileFlag, agentFlag, action, roleID string, dockerAutoSetup bool) (*runner.Runner, error) {
	switch {
	case profileFlag != "" && agentFlag != "":
		return nil, fmt.Errorf("--profile and --agent are mutually exclusive")
	case agentFlag != "":
		return newRunnerFromAgent(env, agentFlag)
	case profileFlag != "":
		return newRunner(env, profileFlag, roleID, dockerAutoSetup)
	default:
		profileName := env.Config.ResolveProfile(action, roleID)
		return newRunner(env, profileName, roleID, dockerAutoSetup)
	}
}

// mergedPricingFromConfig builds a PricingTable that merges all agents' pricing.
// This is useful when tailing mixed agent types (codex + claude).
func mergedPricingFromConfig(cfg *runtime.Config) (agent.PricingTable, string) {
	merged := make(agent.PricingTable)
	var defaultModel string
	for _, ac := range cfg.Agents {
		t, dm := buildPricingFromConfig(ac.Pricing)
		if t == nil {
			continue
		}
		if defaultModel == "" {
			defaultModel = dm
		}
		for name, price := range t {
			merged[name] = price
		}
	}
	if len(merged) == 0 {
		return nil, ""
	}
	return merged, defaultModel
}

// buildPricingFromConfig converts config pricing to an agent PricingTable.
func buildPricingFromConfig(ap *runtime.AgentPricing) (agent.PricingTable, string) {
	if ap == nil {
		return nil, ""
	}
	table := make(agent.PricingTable, len(ap.Models))
	for name, mp := range ap.Models {
		table[name] = agent.ModelPrice{
			InputPerToken:       mp.InputPerMTok / 1e6,
			CachedInputPerToken: mp.CachedInputPerMTok / 1e6,
			OutputPerToken:      mp.OutputPerMTok / 1e6,
		}
	}
	return table, ap.DefaultModel
}

// buildAgent constructs an agent.Agent from config.
func buildAgent(ac *runtime.AgentConfig) agent.Agent {
	pricing, defaultModel := buildPricingFromConfig(ac.Pricing)
	switch ac.Type {
	case "builtin":
		return &agent.MockAgent{
			DefaultModel: defaultModel,
		}
	case "codex":
		cmd := ac.Command
		if cmd == "" {
			cmd = "codex"
		}
		return &agent.CodexAgent{
			Command:      cmd,
			Args:         ac.Args,
			Model:        ac.Model,
			Effort:       ac.Effort,
			MaxBudgetUSD: ac.MaxBudgetUSD,
			DefaultModel: defaultModel,
			Pricing:      pricing,
			Env:          ac.Env,
		}
	default: // "claude", "", or any unknown type
		cmd := ac.Command
		if cmd == "" {
			cmd = ac.Name
		}
		return &agent.ClaudeAgent{
			Command:      cmd,
			Args:         ac.Args,
			Model:        ac.Model,
			Effort:       ac.Effort,
			MaxBudgetUSD: ac.MaxBudgetUSD,
			DefaultModel: defaultModel,
			Pricing:      pricing,
			Env:          ac.Env,
		}
	}
}

// deriveDockerImageName builds the per-project Docker image name. It assumes
// the standard ateam layout where projectDir is `<orgDir>/<project>/.ateam`,
// so filepath.Dir(projectDir) is the project root and its basename is the
// project name (used to scope the image and avoid clashes between projects).
func deriveDockerImageName(projectDir string) string {
	return "ateam-" + filepath.Base(filepath.Dir(projectDir)) + ":latest"
}

// buildContainer creates a Container implementation from config.
// Returns nil for "none" type (runner treats nil as host execution).
// roleID is used for Dockerfile resolution (role-specific Dockerfiles).
func buildContainer(cc *runtime.ContainerConfig, prof *runtime.ProfileConfig, sourceDir, projectDir, orgDir, gitRepoDir, roleID string, dockerAutoSetup bool) (container.Container, error) {
	if cc == nil || cc.Type == "none" {
		return nil, nil
	}
	switch cc.Type {
	case "docker":
		image := deriveDockerImageName(projectDir)
		if dockerAutoSetup {
			if generated, names, err := runtime.AutoSetupDockerfile(sourceDir, projectDir, orgDir); err != nil {
				fmt.Fprintf(os.Stderr, "[docker] auto-setup warning: %v\n", err)
			} else if generated {
				fmt.Fprintf(os.Stderr, "[docker] auto-generated .ateam/Dockerfile (%s)\n", strings.Join(names, ", "))
			}
		}
		dockerfile, dockerfileTmpDir, err := runtime.ResolveDockerfile(cc, projectDir, orgDir, roleID)
		if err != nil {
			return nil, err
		}
		// Resolve relative paths in extra_volumes from project dir
		volumes := make([]string, len(cc.ExtraVolumes))
		for i, vol := range cc.ExtraVolumes {
			resolved, err := resolveVolumePath(vol, sourceDir, sourceDir, orgDir)
			if err != nil {
				return nil, err
			}
			volumes[i] = resolved
		}
		var extraArgs []string
		if prof != nil {
			extraArgs = prof.ContainerExtraArgs
		}
		mountDir := sourceDir
		if gitRepoDir != "" {
			mountDir = gitRepoDir
		}
		var claudeCredentialsFile string
		if cc.MountClaudeConfig {
			if home, err := os.UserHomeDir(); err == nil {
				f := filepath.Join(home, ".claude", ".credentials.json")
				if _, err := os.Stat(f); err == nil {
					claudeCredentialsFile = f
				}
			}
		}
		dc := &container.DockerContainer{
			Image:                 image,
			Dockerfile:            dockerfile,
			DockerfileTmpDir:      dockerfileTmpDir,
			ForwardEnv:            cc.ForwardEnv,
			ExtraVolumes:          volumes,
			ExtraArgs:             extraArgs,
			MountDir:              mountDir,
			SourceDir:             sourceDir,
			ProjectDir:            projectDir,
			OrgDir:                orgDir,
			HostCLIPath:           findLinuxBinary(orgDir),
			ClaudeCredentialsFile: claudeCredentialsFile,
		}
		dc.UseSharedPrepareGuard()
		return dc, nil
	case "docker-exec":
		if cc.DockerContainer == "" {
			cc.DockerContainer = "{{CONTAINER_NAME}}"
		}
		precheckCmd := runtime.ResolvePrecheckCmd(cc, projectDir, orgDir, roleID)
		containerWorkDir := "/workspace"
		if sourceDir != "" && gitRepoDir != "" && gitRepoDir != sourceDir {
			if rel, err := filepath.Rel(gitRepoDir, sourceDir); err == nil {
				containerWorkDir = filepath.Join("/workspace", rel)
			}
		}
		var hostCLIPath string
		if cc.CopyAteam {
			hostCLIPath = findLinuxBinary(orgDir)
		}
		de := &container.DockerExecContainer{
			ContainerName: cc.DockerContainer,
			ExecTemplate:  cc.ExecTemplate,
			ForwardEnv:    cc.ForwardEnv,
			WorkDir:       containerWorkDir,
			HostCLIPath:   hostCLIPath,
			PrecheckCmd:   precheckCmd,
		}
		de.UseSharedPrepareGuard()
		return de, nil
	default:
		return nil, nil
	}
}

// findLinuxBinary locates or builds a Linux/AMD64 ateam binary for Docker.
// Search order: self (if linux), companion binary, org cache, cross-compile.
func findLinuxBinary(orgDir string) string {
	// 1. Already on target platform — use the running binary.
	if goruntime.GOOS == "linux" && goruntime.GOARCH == "amd64" {
		if exe, err := os.Executable(); err == nil {
			return exe
		}
		return ""
	}

	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	exe, _ = filepath.EvalSymlinks(exe)

	// 2. build/ directory (from `make companion` in dev).
	buildDir := filepath.Join(filepath.Dir(exe), "build", "ateam-linux-amd64")
	if info, err := os.Stat(buildDir); err == nil && !info.IsDir() {
		return buildDir
	}

	// 3. Companion binary next to host binary (e.g. from a release archive).
	companion := filepath.Join(filepath.Dir(exe), "ateam-linux-amd64")
	if info, err := os.Stat(companion); err == nil && !info.IsDir() {
		return companion
	}

	// 4. Cached in orgDir from a prior auto cross-compilation.
	if orgDir != "" {
		cached := filepath.Join(orgDir, "cache", "ateam-linux-amd64")
		if info, err := os.Stat(cached); err == nil && !info.IsDir() {
			return cached
		}
	}

	// 5. Cross-compile if Go toolchain is available.
	if orgDir != "" {
		if built := crossBuildIfPossible(exe, orgDir); built != "" {
			return built
		}
	}

	fmt.Fprintf(os.Stderr, "Warning: no Linux ateam binary found for Docker; "+
		"place ateam-linux-amd64 next to %s or install Go to cross-compile\n", exe)
	return ""
}

// crossBuildIfPossible cross-compiles ateam for linux/amd64 if go.mod exists
// next to hostExe and `go` is in PATH. The result is cached in orgDir/cache/
// and reused if the host binary hasn't changed.
func crossBuildIfPossible(hostExe, orgDir string) string {
	modDir := filepath.Dir(hostExe)
	if _, err := os.Stat(filepath.Join(modDir, "go.mod")); err != nil {
		return ""
	}
	if _, err := exec.LookPath("go"); err != nil {
		return ""
	}

	cacheDir := filepath.Join(orgDir, "cache")
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		return ""
	}
	target := filepath.Join(cacheDir, "ateam-linux-amd64")

	// Rebuild only if the target is missing or older than the host binary.
	hostInfo, _ := os.Stat(hostExe)
	targetInfo, targetErr := os.Stat(target)
	if targetErr == nil && hostInfo != nil && !targetInfo.ModTime().Before(hostInfo.ModTime()) {
		return target
	}

	fmt.Fprintf(os.Stderr, "Cross-compiling ateam for linux/amd64...\n")
	cmd := exec.Command("go", "build", "-o", target, ".")
	cmd.Dir = modDir
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: cross-build failed: %v\n", err)
		return ""
	}
	return target
}

func dockerExecOutput(container string, args ...string) (string, error) {
	cmdArgs := append([]string{"exec", container}, args...)
	cmd := exec.Command("docker", cmdArgs...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("docker exec %s %s: %w", container, strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// resolveContainerName resolves a possibly-partial container name to the exact
// Docker container name via substring matching.
func resolveContainerName(name string) (string, error) {
	return container.ResolveRunningContainerName(context.Background(), name)
}

func dockerCp(src, dst string) error {
	cmd := exec.Command("docker", "cp", src, dst)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker cp %s → %s: %w", src, dst, err)
	}
	return nil
}

// gitWriteDirs returns the .git directories that need sandbox write access
// for git operations. In a worktree, this includes both the worktree's
// .git dir and the main repo's common git dir.
func gitWriteDirs(sourceDir string) []string {
	gitDir := execGitCmd(sourceDir, "rev-parse", "--git-dir")
	if gitDir == "" {
		return nil
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(sourceDir, gitDir)
	}
	dirs := []string{gitDir}
	commonDir := execGitCmd(sourceDir, "rev-parse", "--git-common-dir")
	if commonDir != "" {
		if !filepath.IsAbs(commonDir) {
			commonDir = filepath.Join(sourceDir, commonDir)
		}
		if commonDir != gitDir {
			dirs = append(dirs, commonDir)
		}
	}
	return dirs
}

func addProfileFlags(cmd *cobra.Command, profileDst, agentDst *string) {
	cmd.Flags().StringVar(profileDst, "profile", "", "runtime profile (overrides config resolution)")
	cmd.Flags().StringVar(agentDst, "agent", "", "agent name from runtime.hcl (shortcut, uses 'none' container)")
	cmd.MarkFlagsMutuallyExclusive("profile", "agent")
}

func addContainerNameFlag(cmd *cobra.Command, dst *string) {
	cmd.Flags().StringVar(dst, "container-name", "", "override container name (for docker-exec or persistent containers)")
}

// addBudgetFlags registers --max-budget-usd and --max-budget-usd-batch on cmd.
// perAgentDesc and batchDesc are the help strings, which vary per command
// (e.g. "per-agent" vs "per-role" vs "for the supervisor and every sub-run").
// Pass batchDst = nil for single-exec commands that don't manage a batch.
func addBudgetFlags(cmd *cobra.Command, perAgentDst, batchDst *string, perAgentDesc, batchDesc string) {
	cmd.Flags().StringVar(perAgentDst, "max-budget-usd", "", perAgentDesc)
	if batchDst != nil {
		cmd.Flags().StringVar(batchDst, "max-budget-usd-batch", "", batchDesc)
	}
}

// resolveVolumePath resolves relative host paths in a volume spec and validates
// that the resolved path stays within allowedDirs. Volume format:
// "hostPath:containerPath[:mode]". Relative hostPath is resolved from baseDir.
func resolveVolumePath(vol, baseDir string, allowedDirs ...string) (string, error) {
	parts := splitVolumeSpec(vol)
	if len(parts) < 2 {
		return vol, nil
	}
	hostPath := display.ExpandHome(parts[0])
	if !filepath.IsAbs(hostPath) {
		hostPath = filepath.Join(baseDir, hostPath)
	}
	hostPath = filepath.Clean(hostPath)

	allowed := false
	for _, dir := range allowedDirs {
		if dir == "" {
			continue
		}
		rel, err := filepath.Rel(dir, hostPath)
		if err != nil {
			continue
		}
		if !strings.HasPrefix(rel, "..") {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", fmt.Errorf("volume path %s escapes project boundary", hostPath)
	}

	parts[0] = hostPath
	result := parts[0] + ":" + parts[1]
	if len(parts) > 2 {
		result += ":" + parts[2]
	}
	return result, nil
}

// splitVolumeSpec splits "host:container[:mode]" respecting that
// host path on Windows might contain a drive letter (C:\...).
func splitVolumeSpec(vol string) []string {
	parts := []string{}
	for remaining := vol; remaining != ""; {
		idx := strings.IndexByte(remaining, ':')
		if idx < 0 {
			parts = append(parts, remaining)
			break
		}
		parts = append(parts, remaining[:idx])
		remaining = remaining[idx+1:]
	}
	return parts
}

const cheaperModelName = "sonnet"

func addVerboseFlag(cmd *cobra.Command, dst *bool) {
	cmd.Flags().BoolVar(dst, "verbose", false, "print agent and docker commands to stderr")
}

func addCheaperModelFlag(cmd *cobra.Command, dst *bool) {
	cmd.Flags().BoolVar(dst, "cheaper-model", false,
		"use a cheaper model ("+cheaperModelName+"); ignored if --model is also set (--model wins)")
}

// applyEffort sets the agent's reasoning effort if value is non-empty.
// Empty string is a no-op so callers can pass through unconditionally.
func applyEffort(r *runner.Runner, value string) {
	if value != "" {
		r.Agent.SetEffort(value)
	}
}

// applyModel sets the agent's model override if value is non-empty.
// Empty string is a no-op so callers can pass through unconditionally.
func applyModel(r *runner.Runner, value string) {
	if value != "" {
		r.Agent.SetModel(value)
	}
}

// parseBudgetUSD parses a USD amount from a CLI flag string. Empty returns 0/false.
func parseBudgetUSD(value string) (float64, bool, error) {
	if value == "" {
		return 0, false, nil
	}
	v, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, false, fmt.Errorf("invalid USD amount %q: %w", value, err)
	}
	if v < 0 {
		return 0, false, fmt.Errorf("budget cap must be non-negative, got %v", v)
	}
	return v, true, nil
}

// batchBudgetPrecheck builds a closure that returns an error once the recorded
// cost for `batch` crosses `capStr`. Returns (nil, nil) when no cap was
// requested. Used both as a one-shot check (for single-exec actions) and as a
// PoolOpts.PreDispatch hook. The closure only sees committed rows, so up to
// maxParallel in-flight execs can still push the total over the cap; this
// matches the user's "ballpark" tolerance.
func batchBudgetPrecheck(db *calldb.CallDB, projectID, batch, capStr string) (func() error, error) {
	cap, capSet, err := parseBudgetUSD(capStr)
	if err != nil {
		return nil, err
	}
	if !capSet || batch == "" {
		return nil, nil
	}
	return func() error {
		spent, err := db.BatchCostUSD(projectID, batch)
		if err != nil {
			return fmt.Errorf("cannot read batch cost: %w", err)
		}
		if spent >= cap {
			return fmt.Errorf("batch %q reached cap (spent $%.4f / $%.4f)", batch, spent, cap)
		}
		return nil
	}, nil
}

// applyMaxBudgetUSD sets the agent's per-run USD spend cap, gating on whether
// the agent supports it natively. Returns an error when the cap was requested
// but cannot be enforced for an action that requires hard enforcement.
//
// Only Claude has a native --max-budget-usd flag. For Codex:
//   - parallel/report: warn (best-effort; the run-level batch cap still applies)
//   - exec/review/code/verify: error (single-exec actions don't have a batch
//     fallback, so silently dropping the cap would surprise the operator)
func applyMaxBudgetUSD(r *runner.Runner, value, action string) error {
	if value == "" {
		return nil
	}
	r.Agent.SetMaxBudgetUSD(value)
	if r.Agent.Name() != agent.NameCodex {
		return nil
	}
	switch action {
	case runner.ActionParallel, runner.ActionReport:
		fmt.Fprintf(os.Stderr,
			"Warning: --max-budget-usd is not enforced per-agent on codex; relying on --max-budget-usd-batch (if set) for %s.\n",
			action)
		return nil
	default:
		return fmt.Errorf("--max-budget-usd is not supported on codex for %s (no native cap; rerun with claude or drop the flag)", action)
	}
}

// applyModelOverrides resolves --cheaper-model and --model into a single
// agent.SetModel call. --model wins if both are set (with a stderr warning),
// so the agent's configured model and the actual run command always agree.
// Use this in any command that exposes both flags; commands that only expose
// --model can keep using applyModel.
func applyModelOverrides(r *runner.Runner, cheaper bool, model string) {
	chosen := model
	if cheaper && model != "" {
		fmt.Fprintf(os.Stderr,
			"Warning: both --model %q and --cheaper-model set; --model wins (--cheaper-model ignored).\n",
			model)
	} else if cheaper {
		chosen = cheaperModelName
	}
	if chosen != "" {
		r.Agent.SetModel(chosen)
	}
}

func isProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// setSourceWritable marks the runner's container as source-writable.
// No-op if the runner has no container or the container type doesn't support it.
func setSourceWritable(r *runner.Runner) {
	if r.Container != nil {
		r.Container.SetSourceWritable(true)
	}
}

func addDockerAutoSetupFlag(cmd *cobra.Command, dst *bool) {
	cmd.Flags().BoolVar(dst, "docker-auto-setup", true, "auto-generate .ateam/Dockerfile when using docker profile")
}

func addForceFlag(cmd *cobra.Command, dst *bool) {
	cmd.Flags().BoolVar(dst, "force", false, "run even if the same action+role is already running")
}

func checkConcurrentRunsEnv(db *calldb.CallDB, env *root.ResolvedEnv, action string, roles []string) error {
	projectID := env.ProjectID()
	if projectID == "" && env.OrgDir != "" {
		return fmt.Errorf("cannot determine project ID for concurrency guard")
	}
	return checkConcurrentRuns(db, projectID, action, roles)
}

// checkConcurrentRuns returns an error if any of the given roles already have a
// live process for the same project+action. Pass roles=nil to check all roles.
func checkConcurrentRuns(db *calldb.CallDB, projectID, action string, roles []string) error {
	if db == nil {
		return nil
	}
	running, err := db.FindRunning(projectID, action)
	if err != nil || len(running) == 0 {
		return nil
	}

	roleSet := make(map[string]bool, len(roles))
	for _, r := range roles {
		roleSet[r] = true
	}

	var alive []string
	for _, r := range running {
		if len(roles) > 0 && !roleSet[r.Role] {
			continue
		}
		if r.PID > 0 && isProcessAlive(r.PID) {
			alive = append(alive, fmt.Sprintf("  %s (PID %d, started %s)", r.Role, r.PID, r.StartedAt))
		}
	}
	if len(alive) == 0 {
		return nil
	}
	return fmt.Errorf("concurrent %s already running:\n%s\nuse --force to run anyway", action, strings.Join(alive, "\n"))
}

func parseIDArgs(args []string) ([]int64, error) {
	ids := make([]int64, len(args))
	for i, arg := range args {
		id, err := strconv.ParseInt(arg, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid ID %q: %w", arg, err)
		}
		ids[i] = id
	}
	return ids, nil
}

// resolveExecIDs returns the explicit IDs from args, or the most recent run's
// ID when useLast is set and no args were given. Errors when both are
// provided so a stray --last on a typed-ID command line surfaces instead of
// being silently ignored.
func resolveExecIDs(db *calldb.CallDB, args []string, useLast bool) ([]int64, error) {
	if useLast && len(args) > 0 {
		return nil, fmt.Errorf("--last cannot be combined with explicit IDs")
	}
	if useLast {
		id, err := lastRunID(db)
		if err != nil {
			return nil, err
		}
		return []int64{id}, nil
	}
	return parseIDArgs(args)
}

// lastRunID returns the ID of the most recent agent_execs row.
func lastRunID(db *calldb.CallDB) (int64, error) {
	rows, err := db.RecentRuns(calldb.RecentFilter{Limit: 1})
	if err != nil {
		return 0, fmt.Errorf("query failed: %w", err)
	}
	if len(rows) == 0 {
		return 0, fmt.Errorf("no runs found")
	}
	return rows[0].ID, nil
}

func isTerminal() bool {
	return isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd())
}

// stdinIsPiped reports whether stdin is a pipe or redirected file rather than
// an interactive terminal. Commands use this to auto-read a piped prompt
// when no positional argument is given.
func stdinIsPiped() bool {
	fd := os.Stdin.Fd()
	return !isatty.IsTerminal(fd) && !isatty.IsCygwinTerminal(fd)
}

func secretResolver(env *root.ResolvedEnv, backend secret.Backend) *secret.Resolver {
	if env == nil {
		return secret.NewResolver("", "", backend, nil)
	}
	var opts *secret.ResolverOpts
	if env.Config != nil && env.Config.Project.KeychainKey != "" {
		opts = &secret.ResolverOpts{ProjectKeychainKey: env.Config.Project.KeychainKey}
	}
	return secret.NewResolver(env.ProjectDir, env.OrgDir, backend, opts)
}

// dryRunOpts configures what printDryRunInfo displays.
type dryRunOpts struct {
	RoleID string
	Action string
	Batch  string
	Prompt string // if non-empty, printed at the end (truncated)
}

// printDryRunInfo prints resolved execution details for a dry run.
// This is the shared core used by both `exec --dry-run` and `report --dry-run`.
func printDryRunInfo(r *runner.Runner, env *root.ResolvedEnv, opts dryRunOpts) {
	agentName := r.Agent.Name()
	var model string
	if mp, ok := r.Agent.(agent.ModelProvider); ok {
		model = agent.NormalizeModel(mp.ModelName())
	}
	tmplVars := runner.BuildTemplateVars(r, runner.RunOpts{
		RoleID: opts.RoleID,
		Action: opts.Action,
		Batch:  opts.Batch,
	}, time.Now(), 0, agentName, model)
	resolvedAgent := runner.ResolveAgentTemplateArgs(r.Agent, tmplVars)
	resolvedExtraArgs := runner.ResolveTemplateArgs(r.ExtraArgs, tmplVars)

	// Agent and profile
	fmt.Printf("Agent:     %s\n", agentName)
	if r.Profile != "" {
		fmt.Printf("Profile:   %s\n", r.Profile)
	}
	if r.ContainerType != "" && r.ContainerType != "none" {
		name := r.ContainerType
		resolvedName := runner.ResolveTemplateString(r.ContainerName, tmplVars)
		if resolvedName != "" {
			source := ""
			switch r.ContainerNameSource {
			case runner.ContainerNameSourceCLI:
				source = ", via --container-name"
			case runner.ContainerNameSourceSecret:
				source = ", via ateam secret"
			case runner.ContainerNameSourceEnv:
				source = ", via env"
			}
			name += " (" + resolvedName + source + ")"
		}
		fmt.Printf("Container: %s\n", name)
	}
	fmt.Println()

	// Build the full low-level args with container-aware additions
	fullArgs := make([]string, len(resolvedExtraArgs))
	copy(fullArgs, resolvedExtraArgs)
	if runner.IsInContainer() || r.Container != nil {
		fullArgs = append(fullArgs, runner.ResolveTemplateArgs(r.ArgsInsideContainer, tmplVars)...)
	} else {
		fullArgs = append(fullArgs, runner.ResolveTemplateArgs(r.ArgsOutsideContainer, tmplVars)...)
	}

	skipSandbox := (runner.IsInContainer() || r.Container != nil) && !r.Sandbox.InsideContainer
	if r.Sandbox.Settings != "" && !skipSandbox {
		fullArgs = append(fullArgs, "--settings", "<logs>/<exec_id>/settings.json")
	}

	// Resolved command
	cmd, args := resolvedAgent.DebugCommandArgs(fullArgs)
	fmt.Printf("Command:\n  %s %s\n", cmd, strings.Join(args, " "))
	fmt.Println()

	// CLAUDE_CONFIG_DIR — show the agent's config_dir if set, or the env var for claude agents
	configDir := display.ExpandHome(runner.ResolveTemplateString(r.ConfigDir, tmplVars))
	if configDir != "" {
		var configPath string
		if filepath.IsAbs(configDir) {
			configPath = configDir
		} else if r.ProjectDir != "" {
			configPath = filepath.Join(r.ProjectDir, configDir)
		} else {
			configPath = configDir
		}
		fmt.Printf("CLAUDE_CONFIG_DIR: %s (from agent config_dir)\n\n", configPath)
	} else if agentName == "claude" {
		if envDir, ok := os.LookupEnv("CLAUDE_CONFIG_DIR"); ok {
			fmt.Printf("CLAUDE_CONFIG_DIR: %s (from environment)\n\n", envDir)
		}
	}

	// Docker command
	if r.Container != nil {
		dockerOpts := container.RunOpts{WorkDir: r.SourceDir}
		dockerCmd := r.Container.DebugCommand(dockerOpts)
		if dockerCmd != "" {
			fmt.Printf("Docker:\n  %s\n\n", dockerCmd)
		}
	}

	// Secrets
	printDryRunSecrets(r, env)

	// Sandbox
	if r.Sandbox.Settings != "" && !skipSandbox {
		fmt.Println("Sandbox: configured (use --verbose for full JSON)")
	} else if r.Sandbox.Settings != "" && skipSandbox {
		fmt.Println("Sandbox: skipped (inside container)")
	}

	// Prompt (optional)
	if opts.Prompt != "" {
		fmt.Println("Prompt:")
		if len(opts.Prompt) > 500 {
			fmt.Printf("  %s...\n  (%d chars total)\n", opts.Prompt[:500], len(opts.Prompt))
		} else {
			fmt.Printf("  %s\n", opts.Prompt)
		}
	}
}

// logIsolationResults prints a clear message when ateam secret overrides env vars.
func logIsolationResults(w io.Writer, results []secret.IsolationResult) {
	for _, ir := range results {
		if len(ir.Stripped) == 0 {
			continue
		}
		src := ir.ActiveSource
		if src != "env" {
			src = "ateam secret (" + src + ")"
		}
		fmt.Fprintf(w, "Notice: use %s from %s, ignore %s from the environment\n",
			ir.ActiveKey, src, strings.Join(ir.Stripped, ", "))
	}
}

func printDryRunSecrets(r *runner.Runner, env *root.ResolvedEnv) {
	rtCfg, _ := runtime.Load(env.ProjectDir, env.OrgDir)
	if rtCfg == nil {
		return
	}
	var ac *runtime.AgentConfig
	var forwardEnv []string
	profileName := r.Profile
	if strings.HasPrefix(profileName, "a:") {
		an := profileName[2:]
		if a, ok := rtCfg.Agents[an]; ok {
			ac = &a
		}
	} else if profileName != "" {
		if _, a, cc, err := rtCfg.ResolveProfile(profileName); err == nil {
			ac = a
			if cc != nil {
				forwardEnv = cc.ForwardEnv
			}
		}
	}
	if ac == nil {
		return
	}
	resolver := secretResolver(env, secret.DefaultBackend())

	// Run isolation on this copy so details reflect active/stripped status.
	isoResults := secret.IsolateCredentials(ac, resolver)
	details := secret.ResolveAllRequired(ac, forwardEnv, resolver)
	if len(details) == 0 {
		return
	}

	logIsolationResults(os.Stdout, isoResults)
	for _, ir := range isoResults {
		if len(ir.Stripped) > 0 {
			fmt.Println()
			break
		}
	}

	fmt.Println("Secrets (resolution: ateam secret store → env fallback):")
	for _, d := range details {
		switch {
		case !d.Found:
			fmt.Printf("  %-30s ✗ not found\n", d.Name)
		case d.Status == "stripped":
			fmt.Printf("  %-30s ✗ stripped  (found in %s but overridden by ateam secret)\n", d.Name, d.Source)
		case d.Status == "active":
			label := d.Source
			if d.Source != "env" {
				label = "ateam secret, " + d.Source
			}
			fmt.Printf("  %-30s ✓ active   %s (%s)\n", d.Name, d.Masked, label)
		default:
			fmt.Printf("  %-30s ✓ %s (%s, %s)\n", d.Name, d.Masked, d.Source, d.Backend)
		}
	}
	fmt.Println()

	// Show agent env overrides (excluding secrets already shown above).
	if ac.Env != nil {
		secretKeys := map[string]bool{}
		for _, d := range details {
			secretKeys[d.Name] = true
		}
		var setVars, unsetVars []string
		for k, v := range ac.Env {
			if secretKeys[k] {
				continue
			}
			if v == "" {
				unsetVars = append(unsetVars, k)
			} else {
				setVars = append(setVars, k)
			}
		}
		sort.Strings(setVars)
		sort.Strings(unsetVars)
		if len(setVars) > 0 || len(unsetVars) > 0 {
			fmt.Println("Agent env overrides:")
			for _, k := range setVars {
				fmt.Printf("  %-30s = %s\n", k, ac.Env[k])
			}
			for _, k := range unsetVars {
				fmt.Printf("  %-30s   (excluded from parent env)\n", k)
			}
			fmt.Println()
		}
	}
}
