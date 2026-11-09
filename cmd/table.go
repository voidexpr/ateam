package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/config"
	"github.com/ateam/internal/container"
	"github.com/ateam/internal/display"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/ateam/internal/runtime"
	"github.com/ateam/internal/secret"
	"github.com/mattn/go-isatty"
)

// cmdContext returns the root context for long-running commands. Like
// signal.NotifyContext it cancels on SIGINT/SIGTERM, but it records which
// signal arrived as the cancel cause (agent.ErrSignalInterrupt /
// agent.ErrSignalTerminate) so failure classification can distinguish an
// interactive Ctrl-C from a parent process tearing the run down.
func cmdContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case sig := <-sigCh:
			if sig == syscall.SIGTERM {
				cancel(agent.ErrSignalTerminate)
			} else {
				cancel(agent.ErrSignalInterrupt)
			}
		case <-ctx.Done():
		}
	}()
	stop := func() {
		signal.Stop(sigCh)
		cancel(nil)
	}
	return ctx, stop
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

// printArtifact prints the on-disk artifact at path for --print. The shipped
// single-file prompts (review, verify, report, auto_setup) instruct the agent
// to write its output via the Write tool and end with a one-line confirmation
// message — "the on-disk file is the source of truth, anything you stream as
// text is discarded." So --print must read the file, not the stream. The
// prompts' documented recovery path is "if Write fails, emit the body as the
// final message," so streamFallback is printed when the file is missing or
// empty. Quiet no-op when both are empty.
func printArtifact(path, streamFallback string) {
	if path != "" {
		data, err := os.ReadFile(path)
		if err == nil && len(data) > 0 {
			fmt.Printf("\n%s", string(data))
			if data[len(data)-1] != '\n' {
				fmt.Println()
			}
			return
		}
	}
	if streamFallback != "" {
		fmt.Printf("\n%s\n", streamFallback)
	}
}

// newRunner creates a AgentExecutor using the resolved profile from runtime.hcl.
// roleID is optional — used for role-specific Dockerfile resolution.
func newRunner(env *root.ResolvedEnv, profileName, roleID string, dockerAutoSetup bool) (*runner.AgentExecutor, error) {
	rtCfg, err := runtime.Load(env.Layers)
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

	// codex-tmux is host-only in v1: container support requires tmux+codex
	// inside the image plus host↔container path translation that isn't
	// wired up. Reject loudly here instead of silently mis-staging things
	// inside a container that can't run tmux.
	if ac.Type == agent.NameCodexTmux && cc != nil && cc.Type != "none" {
		return nil, fmt.Errorf("codex-tmux is host-only in v1; profile %q binds it to container type %q. Use a container=none profile or `--agent codex-tmux`", profileName, cc.Type)
	}
	run := rtCfg.AgentForRun(prof, ac, runInsideContainer(cc))
	ac = &run

	// Validate secrets. IsolateCredentials always runs to strip competing
	// alternatives from the agent env — this is safe even when no secrets
	// are configured.
	resolver := secretResolver(env, secret.DefaultBackend())
	if cc != nil && cc.Type != "none" {
		// Launching the agent into a fresh container: it can't do interactive
		// login, so required creds must be present up front. Hard failure.
		if err := secret.ValidateSecrets(ac, resolver); err != nil {
			return nil, err
		}
	} else if container.IsInContainer() {
		// ateam itself is running inside a container with container=none, so
		// the agent runs in-place. The container may have been pre-configured
		// with interactive/regular auth (e.g. ~/.claude/.credentials.json), in
		// which case missing required_env is fine. Warn instead of failing so
		// the pre-configured-agent workflow keeps working; the agent will
		// error loudly itself if it truly has no usable auth.
		warnMissingSecretsInContainer(secret.ValidateSecrets(ac, resolver), ac)
	}
	logIsolationResults(os.Stderr, secret.IsolateCredentials(ac, resolver))

	r := runnerFromAgentConfig(env, ac)
	r.Profile = profileName
	r.ProjectID = env.ProjectID()
	r.ExtraArgs = append(r.ExtraArgs, prof.AgentExtraArgs...)
	if err := preflightContainerSupportsWorkDir(cc, env); err != nil {
		return nil, err
	}
	ct, err := buildContainer(cc, prof, env, roleID, dockerAutoSetup)
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
func applyContainerNameOverride(r *runner.AgentExecutor, name string) {
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
func applyContainerName(r *runner.AgentExecutor, env *root.ResolvedEnv, cliFlag string) error {
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

// newRunnerFromAgent creates a AgentExecutor using a named agent directly (no profile).
func newRunnerFromAgent(env *root.ResolvedEnv, agentName string) (*runner.AgentExecutor, error) {
	rtCfg, err := runtime.Load(env.Layers)
	if err != nil {
		return nil, fmt.Errorf("cannot load runtime.hcl: %w", err)
	}

	ac, ok := rtCfg.Agents[agentName]
	if !ok {
		return nil, fmt.Errorf("unknown agent %q", agentName)
	}
	ac = rtCfg.AgentForRun(nil, &ac, runInsideContainer(nil))

	// No container launch — the agent runs in-place. On the host it handles
	// its own auth; inside a pre-configured container it may rely on regular
	// auth (~/.claude/.credentials.json), so a missing required_env is only a
	// warning, not a hard failure. IsolateCredentials still runs to strip
	// competing env vars.
	resolver := secretResolver(env, secret.DefaultBackend())
	if container.IsInContainer() {
		warnMissingSecretsInContainer(secret.ValidateSecrets(&ac, resolver), &ac)
	}
	logIsolationResults(os.Stderr, secret.IsolateCredentials(&ac, resolver))

	r := runnerFromAgentConfig(env, &ac)
	r.Profile = "a:" + agentName
	r.ProfileSource = config.ProfileSourceAgent
	r.ProjectID = env.ProjectID()
	r.ContainerType = "none"
	return r, nil
}

func minimalRunnerFromAgentConfig(env *root.ResolvedEnv, ac *runtime.AgentConfig) *runner.AgentExecutor {
	r := &runner.AgentExecutor{
		Agent:   buildAgent(ac),
		HomeDir: env.HomeDir,
		Sandbox: runner.SandboxConfig{
			Settings: ac.Sandbox,
			RWPaths:  ac.RWPaths,
			ROPaths:  ac.ROPaths,
			Denied:   ac.DeniedPaths,
		},
		ConfigDir: ac.ConfigDir,
	}
	// Scratch mode: codex-tmux roots its socket at <HomeDir>/state/cache/tmux/.
	setCodexTmuxProjectDir(r.Agent, env.StateDir())
	return r
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

// setCodexTmuxProjectDir is called by the runner builders after the agent is
// constructed but before the AgentExecutor is returned. codex-tmux needs ProjectDir
// to derive its tmux socket path (`<ProjectDir>/.ateam/cache/tmux/exec-<id>.sock`),
// and the buildAgent helper doesn't see ResolvedEnv directly.
func setCodexTmuxProjectDir(a agent.Agent, projectDir string) {
	if ct, ok := a.(*agent.CodexTmuxAgent); ok {
		ct.ProjectDir = projectDir
	}
}

func runnerFromAgentConfig(env *root.ResolvedEnv, ac *runtime.AgentConfig) *runner.AgentExecutor {
	// Sandbox grants and AgentExecutor.SourceDir follow WorkDir (where the agent
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
	r := &runner.AgentExecutor{
		Agent:       buildAgent(ac),
		ProjectDir:  env.ProjectDir,
		HomeDir:     env.HomeDir,
		SourceDir:   env.WorkDir,
		ProjectName: env.ProjectName,
		Sandbox: runner.SandboxConfig{
			Settings:       ac.Sandbox,
			RWPaths:        ac.RWPaths,
			ROPaths:        ac.ROPaths,
			Denied:         ac.DeniedPaths,
			ExtraWriteDirs: extraWriteDirs,
		},
		ConfigDir: ac.ConfigDir,
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
	setCodexTmuxProjectDir(r.Agent, env.StateDir())
	return r
}

// newRunnerDefault creates a AgentExecutor using the default profile.
func newRunnerDefault(env *root.ResolvedEnv) (*runner.AgentExecutor, error) {
	return newRunnerFromChoice(env, env.Config.ResolveProfile("", ""), "", false)
}

// newRunnerFromChoice is newRunner for a profile picked by config.toml,
// stamping the setting that picked it onto the runner.
func newRunnerFromChoice(env *root.ResolvedEnv, choice config.ProfileChoice, roleID string, dockerAutoSetup bool) (*runner.AgentExecutor, error) {
	r, err := newRunner(env, choice.Name, roleID, dockerAutoSetup)
	if err != nil {
		return nil, err
	}
	r.ProfileSource = choice.Source
	return r, nil
}

// resolveRunnerMinimal builds a AgentExecutor without project context (just org dir).
// Docker containers are not supported without project context.
func resolveRunnerMinimal(env *root.ResolvedEnv, profileFlag, agentFlag string) (*runner.AgentExecutor, error) {
	rtCfg, err := runtime.Load(env.Layers)
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
		if err := rejectCodexTmuxWithoutStateDir(&ac, env.StateDir()); err != nil {
			return nil, err
		}
		ac = rtCfg.AgentForRun(nil, &ac, runInsideContainer(nil))
		r := minimalRunnerFromAgentConfig(env, &ac)
		r.Profile = "a:" + agentFlag
		r.ProfileSource = config.ProfileSourceAgent
		return r, nil
	default:
		choice := config.ProfileChoice{Name: profileFlag, Source: config.ProfileSourceFlag}
		if profileFlag == "" {
			choice = config.ProfileChoice{Name: "default", Source: config.ProfileSourceBuiltin}
		}
		prof, ac, _, err := rtCfg.ResolveProfile(choice.Name)
		if err != nil {
			return nil, err
		}
		if err := rejectCodexTmuxWithoutStateDir(ac, env.StateDir()); err != nil {
			return nil, err
		}
		// Docker is not supported without project context: the agent runs
		// in-place, so the container block does not pick the context side.
		run := rtCfg.AgentForRun(prof, ac, runInsideContainer(nil))
		r := minimalRunnerFromAgentConfig(env, &run)
		r.Profile = choice.Name
		r.ProfileSource = choice.Source
		r.ExtraArgs = append(r.ExtraArgs, prof.AgentExtraArgs...)
		return r, nil
	}
}

// rejectCodexTmuxWithoutStateDir errors out when codex-tmux has no anchor for
// its tmux socket. The agent writes to `<StateDir>/cache/tmux/`, where
// StateDir is the project's .ateam/ if present else ~/.ateam/state.
// With neither resolved we'd fail inside the agent with a cryptic "requires
// project context" message; catch it here so the operator gets actionable
// guidance instead.
func rejectCodexTmuxWithoutStateDir(ac *runtime.AgentConfig, stateDir string) error {
	if ac == nil || ac.Type != agent.NameCodexTmux {
		return nil
	}
	if stateDir != "" {
		return nil
	}
	return fmt.Errorf("codex-tmux requires a state directory (a project .ateam/ or the user ateam directory); run from inside a project or use a different agent")
}

// resolveRunner builds a AgentExecutor from --profile/--agent flags, falling back to config resolution.
func resolveRunner(env *root.ResolvedEnv, profileFlag, agentFlag, action, roleID string, dockerAutoSetup bool) (*runner.AgentExecutor, error) {
	switch {
	case profileFlag != "" && agentFlag != "":
		return nil, fmt.Errorf("--profile and --agent are mutually exclusive")
	case agentFlag != "":
		return newRunnerFromAgent(env, agentFlag)
	case profileFlag != "":
		return newRunnerFromChoice(env, config.ProfileChoice{Name: profileFlag, Source: config.ProfileSourceFlag}, roleID, dockerAutoSetup)
	default:
		return newRunnerFromChoice(env, env.Config.ResolveProfile(action, roleID), roleID, dockerAutoSetup)
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
			DefaultModel:              defaultModel,
			WriteResponseToOutputFile: true,
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
	case "codex-tmux":
		cmd := ac.Command
		if cmd == "" {
			cmd = "codex"
		}
		// ProjectDir is set later in runnerFromAgentConfig (after we know
		// the resolved env) — buildAgent is called from minimalRunner too
		// where ProjectDir isn't available; the runner layer fills it in.
		return &agent.CodexTmuxAgent{
			Command:          cmd,
			Args:             ac.Args,
			Model:            ac.Model,
			Effort:           ac.Effort,
			MaxBudgetUSD:     ac.MaxBudgetUSD,
			DefaultModel:     defaultModel,
			Pricing:          pricing,
			Env:              ac.Env,
			StartTimeout:     parseRuntimeDuration(ac.StartTimeout, "start_timeout"),
			BusyTimeout:      parseRuntimeDuration(ac.BusyTimeout, "busy_timeout"),
			QuiescenceWindow: parseRuntimeDuration(ac.QuiescenceWindow, "quiescence_window"),
			TmuxWidth:        ac.TmuxWidth,
			TmuxHeight:       ac.TmuxHeight,
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

func parseRuntimeDuration(value, field string) time.Duration {
	if value == "" {
		return 0
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: invalid runtime.hcl %s %q: %v\n", field, value, err)
		return 0
	}
	return d
}

// deriveDockerImageName builds the per-project Docker image name. It assumes
// the standard ateam layout where projectDir is `<project root>/.ateam`,
// so filepath.Dir(projectDir) is the project root and its basename is the
// project name (used to scope the image and avoid clashes between projects).
func deriveDockerImageName(projectDir string) string {
	return "ateam-" + filepath.Base(filepath.Dir(projectDir)) + ":latest"
}

// buildContainer creates a Container implementation from config.
// Returns nil for "none" type (runner treats nil as host execution).
// roleID is used for Dockerfile resolution (role-specific Dockerfiles).
func buildContainer(cc *runtime.ContainerConfig, prof *runtime.ProfileConfig, env *root.ResolvedEnv, roleID string, dockerAutoSetup bool) (container.Container, error) {
	if cc == nil || cc.Type == "none" {
		return nil, nil
	}
	sourceDir, projectDir, homeDir, gitRepoDir := env.WorkDir, env.ProjectDir, env.HomeDir, env.GitRepoDir
	switch cc.Type {
	case "docker":
		image := deriveDockerImageName(projectDir)
		if dockerAutoSetup {
			if generated, names, err := runtime.AutoSetupDockerfile(sourceDir, projectDir, env.Layers); err != nil {
				fmt.Fprintf(os.Stderr, "[docker] auto-setup warning: %v\n", err)
			} else if generated {
				fmt.Fprintf(os.Stderr, "[docker] auto-generated .ateam/Dockerfile (%s)\n", strings.Join(names, ", "))
			}
		}
		dockerfile, dockerfileTmpDir, err := runtime.ResolveDockerfile(cc, env.Layers, roleID)
		if err != nil {
			return nil, err
		}
		// Resolve relative paths in extra_volumes from project dir
		volumes := make([]string, len(cc.ExtraVolumes))
		for i, vol := range cc.ExtraVolumes {
			resolved, err := resolveVolumePath(vol, sourceDir, sourceDir, homeDir)
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
			HomeDir:               homeDir,
			HostCLIPath:           findLinuxBinary(homeDir),
			ClaudeCredentialsFile: claudeCredentialsFile,
		}
		dc.UseSharedPrepareGuard()
		return dc, nil
	case "docker-exec":
		if cc.DockerContainer == "" {
			cc.DockerContainer = "{{CONTAINER_NAME}}"
		}
		precheckCmd := runtime.ResolvePrecheckCmd(cc, env.Layers, roleID)
		containerWorkDir := "/workspace"
		if sourceDir != "" && gitRepoDir != "" && gitRepoDir != sourceDir {
			if rel, err := filepath.Rel(gitRepoDir, sourceDir); err == nil {
				containerWorkDir = filepath.Join("/workspace", rel)
			}
		}
		var hostCLIPath string
		if cc.CopyAteam {
			hostCLIPath = findLinuxBinary(homeDir)
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

// linuxCompanionName is the on-disk name of the linux companion binary for
// the host's CPU arch (e.g. ateam-linux-arm64 on Apple Silicon,
// ateam-linux-amd64 on Intel). Matched to the host arch because the
// container engine on the host typically defaults to the same platform —
// running an amd64 ateam under x86_64 emulation in an arm64 container
// triggers Go runtime GC crashes ("found pointer to free object").
func linuxCompanionName() string {
	return "ateam-linux-" + goruntime.GOARCH
}

// findLinuxBinary locates or builds a linux ateam binary for the host's
// arch, suitable for pushing into a same-arch Docker container.
// Search order: self (if linux), companion binary, the user cache under
// ~/.ateam/state/cache, cross-compile.
func findLinuxBinary(homeDir string) string {
	name := linuxCompanionName()

	// 1. Already on linux — the running binary is linux/<host arch> by
	// definition, so just use it.
	if goruntime.GOOS == "linux" {
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
	buildDir := filepath.Join(filepath.Dir(exe), "build", name)
	if info, err := os.Stat(buildDir); err == nil && !info.IsDir() {
		return buildDir
	}

	// 3. Companion binary next to host binary (e.g. from a release archive).
	companion := filepath.Join(filepath.Dir(exe), name)
	if info, err := os.Stat(companion); err == nil && !info.IsDir() {
		return companion
	}

	// 4. Cached under the user state dir from a prior auto cross-compilation.
	if homeDir != "" {
		cached := filepath.Join(binaryCacheDir(homeDir), name)
		if info, err := os.Stat(cached); err == nil && !info.IsDir() {
			return cached
		}
	}

	// 5. Cross-compile if Go toolchain is available.
	if homeDir != "" {
		if built := crossBuildIfPossible(exe, homeDir); built != "" {
			return built
		}
	}

	fmt.Fprintf(os.Stderr, "Warning: no Linux ateam binary found for Docker; "+
		"place %s next to %s or install Go to cross-compile\n", name, exe)
	return ""
}

// binaryCacheDir is where cross-compiled companion binaries are cached.
func binaryCacheDir(homeDir string) string {
	return filepath.Join(homeDir, runtime.StateSubdir, "cache")
}

// crossBuildIfPossible cross-compiles ateam for linux/<host arch> if go.mod
// exists next to hostExe and `go` is in PATH. The result is cached under
// ~/.ateam/state/cache/ and reused if the host binary hasn't changed.
func crossBuildIfPossible(hostExe, homeDir string) string {
	modDir := filepath.Dir(hostExe)
	if _, err := os.Stat(filepath.Join(modDir, "go.mod")); err != nil {
		return ""
	}
	if _, err := exec.LookPath("go"); err != nil {
		return ""
	}

	cacheDir := binaryCacheDir(homeDir)
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		return ""
	}
	target := filepath.Join(cacheDir, linuxCompanionName())

	// Rebuild only if the target is missing or older than the host binary.
	hostInfo, _ := os.Stat(hostExe)
	targetInfo, targetErr := os.Stat(target)
	if targetErr == nil && hostInfo != nil && !targetInfo.ModTime().Before(hostInfo.ModTime()) {
		return target
	}

	fmt.Fprintf(os.Stderr, "Cross-compiling ateam for linux/%s...\n", goruntime.GOARCH)
	cmd := exec.Command("go", "build", "-o", target, ".")
	cmd.Dir = modDir
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+goruntime.GOARCH, "CGO_ENABLED=0")
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
// Docker container name via substring matching. Stored in a var so tests can
// stub out the Docker dependency, mirroring runAutoRoles.
var resolveContainerName = func(name string) (string, error) {
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

// resolveBatch picks the batch ID for a supervisor-driven run. When override
// is non-empty (the user passed --batch), it wins; otherwise the helper mints
// "<prefix>-<timestamp>" using the same TimestampFormat all the renderers
// agree on. Centralized so every supervisor entry point (code, parallel,
// report, run-all, ...) produces the same shape — and so prefix typos stay
// out of LatestBatch lookups downstream.
func resolveBatch(override, prefix string) string {
	if override != "" {
		return override
	}
	return prefix + "-" + time.Now().Format(display.TimestampFormat)
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
func setSourceWritable(r *runner.AgentExecutor) {
	if r.Container != nil {
		r.Container.SetSourceWritable(true)
	}
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
		return secret.NewResolver("", backend, nil)
	}
	var opts *secret.ResolverOpts
	if env.KeychainKey != "" {
		opts = &secret.ResolverOpts{ProjectKeychainKey: env.KeychainKey}
	}
	return secret.NewResolver(env.ProjectDir, backend, opts)
}
