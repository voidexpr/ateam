package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	goruntime "runtime"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/config"
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
	fmt.Printf("Done (%s%s)\n\n", runner.FormatDuration(r.Duration), costSuffix)
}

func fmtInt(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf("%d", n)
}

// openProjectDB opens the per-project state.sqlite in .ateam/.
// Falls back to the legacy org-level state.sqlite if the project DB doesn't exist.
func openProjectDB(env *root.ResolvedEnv) *calldb.CallDB {
	if env.ProjectDir != "" {
		dbPath := env.ProjectDBPath()
		db, err := calldb.OpenIfExists(dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: cannot open project database: %v\n", err)
			return nil
		}
		if db != nil {
			return db
		}
		// Project DB doesn't exist yet; fall back to org-level DB.
	}
	return openCallDB(env.OrgDir)
}

func openCallDB(orgDir string) *calldb.CallDB {
	if orgDir == "" {
		return nil
	}
	db, err := calldb.Open(filepath.Join(orgDir, "state.sqlite"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: cannot open call database: %v\n", err)
		return nil
	}
	return db
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
		return nil, err
	}

	// Only validate secrets for container runs — agents handle their own auth on host.
	if cc != nil && cc.Type != "none" {
		resolver := secretResolver(env, secret.DefaultBackend())
		if err := secret.ValidateSecrets(ac, resolver); err != nil {
			return nil, err
		}
	}

	r := runnerFromAgentConfig(env, ac)
	r.Profile = profileName
	r.ProjectID = ""
	r.ExtraArgs = append(r.ExtraArgs, prof.AgentExtraArgs...)
	ct, err := buildContainer(cc, prof, env.SourceDir, env.ProjectDir, env.OrgDir, env.GitRepoDir, roleID, dockerAutoSetup)
	if err != nil {
		return nil, err
	}
	// Merge per-project container extras from config.toml [container-extra]
	if dc, ok := ct.(*container.DockerContainer); ok && env.Config != nil {
		ce := env.Config.ContainerExtra
		dc.ExtraArgs = append(dc.ExtraArgs, ce.ExtraArgs...)
		dc.ForwardEnv = append(dc.ForwardEnv, ce.ForwardEnv...)
		if len(ce.Env) > 0 {
			if dc.Env == nil {
				dc.Env = make(map[string]string, len(ce.Env))
			}
			for k, v := range ce.Env {
				dc.Env[k] = v
			}
		}
	}
	r.Container = ct
	if cc != nil && cc.Type != "none" {
		r.ContainerType = cc.Type
		if dc, ok := ct.(*container.DockerContainer); ok {
			r.ContainerName = dc.ContainerName
		}
	} else {
		r.ContainerType = "none"
	}
	return r, nil
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

	r := runnerFromAgentConfig(env, &ac)
	r.Profile = "a:" + agentName
	r.ProjectID = ""
	r.ContainerType = "none"
	return r, nil
}

func minimalRunnerFromAgentConfig(orgDir string, ac *runtime.AgentConfig) *runner.Runner {
	return &runner.Runner{
		Agent:           buildAgent(ac),
		OrgDir:          orgDir,
		SandboxSettings: ac.Sandbox,
		SandboxRWPaths:  ac.RWPaths,
		SandboxROPaths:  ac.ROPaths,
		SandboxDenied:   ac.DeniedPaths,
		ConfigDir:       ac.ConfigDir,
	}
}

func runnerFromAgentConfig(env *root.ResolvedEnv, ac *runtime.AgentConfig) *runner.Runner {
	extraWriteDirs := gitWriteDirs(env.SourceDir)
	r := &runner.Runner{
		Agent:           buildAgent(ac),
		LogFile:         env.RunnerLogPath(),
		ProjectDir:      env.ProjectDir,
		OrgDir:          env.OrgDir,
		SourceDir:       env.SourceDir,
		ProjectName:     env.ProjectName,
		ExtraWriteDirs:  extraWriteDirs,
		SandboxSettings: ac.Sandbox,
		SandboxRWPaths:  ac.RWPaths,
		SandboxROPaths:  ac.ROPaths,
		SandboxDenied:   ac.DeniedPaths,
		ConfigDir:       ac.ConfigDir,
	}
	if env.Config != nil {
		r.SandboxExtraWrite = env.Config.SandboxExtra.AllowWrite
		r.SandboxExtraRead = env.Config.SandboxExtra.AllowRead
		r.SandboxExtraDomains = env.Config.SandboxExtra.AllowDomains
		r.SandboxExtraExcludedCmd = env.Config.SandboxExtra.UnsandboxedCommands
	}
	// Grant read access to the entire git repo when project is nested within it
	if env.GitRepoDir != "" && env.GitRepoDir != env.SourceDir {
		r.SandboxExtraRead = append(r.SandboxExtraRead, env.GitRepoDir)
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
			InputPerToken:  mp.InputPerMTok / 1e6,
			OutputPerToken: mp.OutputPerMTok / 1e6,
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
			Pricing:      pricing,
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
			DefaultModel: defaultModel,
			Env:          ac.Env,
		}
	}
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
		// Image name derived from project dir name
		image := "ateam-" + filepath.Base(filepath.Dir(projectDir)) + ":latest"
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
			volumes[i] = resolveVolumePath(vol, sourceDir)
		}
		var extraArgs []string
		if prof != nil {
			extraArgs = prof.ContainerExtraArgs
		}
		mountDir := sourceDir
		if gitRepoDir != "" {
			mountDir = gitRepoDir
		}

		persistent := cc.Mode == "persistent"
		var containerName string
		if persistent {
			containerName = buildContainerName(sourceDir, orgDir, roleID)
		}

		precheckScript := runtime.ResolvePrecheckScript(cc, projectDir, orgDir, roleID)

		return &container.DockerContainer{
			Image:            image,
			Dockerfile:       dockerfile,
			DockerfileTmpDir: dockerfileTmpDir,
			ForwardEnv:       cc.ForwardEnv,
			ExtraVolumes:     volumes,
			ExtraArgs:        extraArgs,
			Persistent:       persistent,
			ContainerName:    containerName,
			MountDir:         mountDir,
			SourceDir:        sourceDir,
			ProjectDir:       projectDir,
			OrgDir:           orgDir,
			HostCLIPath:      findLinuxBinary(orgDir),
			PrecheckScript:   precheckScript,
		}, nil
	case "devcontainer":
		configPath := cc.ConfigPath
		if configPath == "" {
			configPath = ".devcontainer/devcontainer.json"
		}
		if !filepath.IsAbs(configPath) {
			configPath = filepath.Join(sourceDir, configPath)
		}
		return &container.DevcontainerContainer{
			ConfigPath:   configPath,
			WorkspaceDir: sourceDir,
			ForwardEnv:   cc.ForwardEnv,
		}, nil
	case "docker-sandbox":
		sandboxName := buildContainerName(sourceDir, orgDir, roleID)
		mountDir := sourceDir
		if gitRepoDir != "" {
			mountDir = gitRepoDir
		}
		var claudeDir string
		if cc.CopyClaudeConfig {
			if home, err := os.UserHomeDir(); err == nil {
				claudeDir = filepath.Join(home, ".claude")
			}
		}
		return &container.DockerSandboxContainer{
			WorkspaceDir:  sourceDir,
			MountDir:      mountDir,
			OrgDir:        orgDir,
			ClaudeDir:     claudeDir,
			CacheDir:      filepath.Join(projectDir, "cache"),
			ForwardEnv:    cc.ForwardEnv,
			SandboxName:   sandboxName,
			NetworkPolicy: cc.NetworkPolicy,
			BuildVersion:  GitCommit,
		}, nil
	default:
		return nil, nil
	}
}

func buildContainerName(sourceDir, orgDir, roleID string) string {
	if roleID == "" {
		roleID = "adhoc"
	}
	if orgDir == "" {
		return "ateam-adhoc"
	}
	orgRoot := filepath.Dir(orgDir)
	relPath, err := filepath.Rel(orgRoot, sourceDir)
	if err != nil || relPath == "" {
		return "ateam-adhoc"
	}
	projectID := config.PathToProjectID(relPath)
	return "ateam-" + projectID + "-" + roleID
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

	// 2. Companion binary next to host binary (e.g. from a release archive).
	companion := filepath.Join(filepath.Dir(exe), "ateam-linux-amd64")
	if info, err := os.Stat(companion); err == nil && !info.IsDir() {
		return companion
	}

	// 3. Cached in orgDir from a prior cross-compilation.
	if orgDir != "" {
		cached := filepath.Join(orgDir, "cache", "ateam-linux-amd64")
		if info, err := os.Stat(cached); err == nil && !info.IsDir() {
			return cached
		}
	}

	// 4. Cross-compile if Go toolchain is available.
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
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
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

// resolveVolumePath resolves relative host paths in a volume spec.
// Volume format: "hostPath:containerPath[:mode]"
// Relative hostPath is resolved from baseDir (project source dir).
func resolveVolumePath(vol, baseDir string) string {
	parts := splitVolumeSpec(vol)
	if len(parts) < 2 {
		return vol
	}
	hostPath := parts[0]
	if !filepath.IsAbs(hostPath) {
		hostPath = filepath.Join(baseDir, hostPath)
	}
	parts[0] = hostPath
	result := parts[0] + ":" + parts[1]
	if len(parts) > 2 {
		result += ":" + parts[2]
	}
	return result
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
	cmd.Flags().BoolVar(dst, "cheaper-model", false, "use a cheaper model ("+cheaperModelName+")")
}

func applyCheaperModel(r *runner.Runner, cheaper bool) {
	if cheaper {
		r.ExtraArgs = append(r.ExtraArgs, "--model", cheaperModelName)
	}
}

func isProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// setSourceWritable marks the runner's docker container as source-writable.
// No-op if the runner doesn't use a docker container.
func setSourceWritable(r *runner.Runner) {
	if dc, ok := r.Container.(*container.DockerContainer); ok {
		dc.SourceWritable = true
	}
}

func addDockerAutoSetupFlag(cmd *cobra.Command, dst *bool) {
	cmd.Flags().BoolVar(dst, "docker-auto-setup", true, "auto-generate .ateam/Dockerfile when using docker profile")
}

func addForceFlag(cmd *cobra.Command, dst *bool) {
	cmd.Flags().BoolVar(dst, "force", false, "run even if the same action+role is already running")
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

func isTerminal() bool {
	return isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd())
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

func fmtDateAge(t time.Time) string {
	date := t.Format("01/02")
	age := time.Since(t)
	switch {
	case age < time.Minute:
		return date + " (just now)"
	case age < time.Hour:
		return fmt.Sprintf("%s (%dm ago)", date, int(age.Minutes()))
	case age < 24*time.Hour:
		return fmt.Sprintf("%s (%dh ago)", date, int(age.Hours()))
	default:
		days := int(age.Hours()) / 24
		return fmt.Sprintf("%s (%dd ago)", date, days)
	}
}
