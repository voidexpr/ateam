package cmd

import (
	"fmt"
	"os"
	"os/exec"
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
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/ateam/internal/runtime"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

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

func fmtCost(cost float64) string {
	if cost <= 0 {
		return ""
	}
	return fmt.Sprintf("$%.2f", cost)
}

func printDone(r runner.RunSummary) {
	costSuffix := ""
	if c := fmtCost(r.Cost); c != "" {
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
		db, err := calldb.Open(dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: cannot open project database: %v\n", err)
			return nil
		}
		return db
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

// resolveStreamPath resolves a stream_file path from the DB.
// New layout: relative to .ateam/ (e.g. "logs/roles/security/...").
// Legacy layout: relative to .ateamorg/ (e.g. "projects/<id>/roles/...").
// Detection: paths starting with "projects/" are legacy; otherwise new.
func resolveStreamPath(env *root.ResolvedEnv, sf string) string {
	if sf == "" || filepath.IsAbs(sf) {
		return sf
	}
	if strings.HasPrefix(sf, "projects/") && env.OrgDir != "" {
		return filepath.Join(env.OrgDir, sf)
	}
	return filepath.Join(env.ProjectDir, sf)
}

// newRunner creates a Runner using the resolved profile from runtime.hcl.
// roleID is optional — used for role-specific Dockerfile resolution.
func newRunner(env *root.ResolvedEnv, profileName, roleID string) (*runner.Runner, error) {
	rtCfg, err := runtime.Load(env.ProjectDir, env.OrgDir)
	if err != nil {
		return nil, fmt.Errorf("cannot load runtime.hcl: %w", err)
	}

	prof, ac, cc, err := rtCfg.ResolveProfile(profileName)
	if err != nil {
		return nil, err
	}

	r := runnerFromAgentConfig(env, ac)
	r.Profile = profileName
	r.ProjectID = ""
	r.ExtraArgs = append(r.ExtraArgs, prof.AgentExtraArgs...)
	ct, err := buildContainer(cc, prof, env.SourceDir, env.ProjectDir, env.OrgDir, env.GitRepoDir, roleID)
	if err != nil {
		return nil, err
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
	var extraWriteDirs []string
	if env.OrgDir != "" {
		extraWriteDirs = []string{env.OrgDir}
	}
	return &runner.Runner{
		Agent:           buildAgent(ac),
		LogFile:         env.RunnerLogPath(),
		ProjectDir:      env.ProjectDir,
		OrgDir:          env.OrgDir,
		ExtraWriteDirs:  extraWriteDirs,
		SandboxSettings: ac.Sandbox,
		SandboxRWPaths:  ac.RWPaths,
		SandboxROPaths:  ac.ROPaths,
		SandboxDenied:   ac.DeniedPaths,
		ConfigDir:       ac.ConfigDir,
	}
}

// newRunnerDefault creates a Runner using the default profile.
func newRunnerDefault(env *root.ResolvedEnv) (*runner.Runner, error) {
	profileName := env.Config.ResolveProfile("", "")
	return newRunner(env, profileName, "")
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
func resolveRunner(env *root.ResolvedEnv, profileFlag, agentFlag, action, roleID string) (*runner.Runner, error) {
	switch {
	case profileFlag != "" && agentFlag != "":
		return nil, fmt.Errorf("--profile and --agent are mutually exclusive")
	case agentFlag != "":
		return newRunnerFromAgent(env, agentFlag)
	case profileFlag != "":
		return newRunner(env, profileFlag, roleID)
	default:
		profileName := env.Config.ResolveProfile(action, roleID)
		return newRunner(env, profileName, roleID)
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
func buildContainer(cc *runtime.ContainerConfig, prof *runtime.ProfileConfig, sourceDir, projectDir, orgDir, gitRepoDir, roleID string) (container.Container, error) {
	if cc == nil || cc.Type == "none" {
		return nil, nil
	}
	switch cc.Type {
	case "docker":
		dockerfile, err := runtime.ResolveDockerfile(cc, projectDir, orgDir, roleID)
		if err != nil {
			return nil, err
		}
		// Resolve relative paths in extra_volumes from project dir
		volumes := make([]string, len(cc.ExtraVolumes))
		for i, vol := range cc.ExtraVolumes {
			volumes[i] = resolveVolumePath(vol, sourceDir)
		}
		// Image name derived from project dir name
		image := "ateam-" + filepath.Base(filepath.Dir(projectDir)) + ":latest"
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

		return &container.DockerContainer{
			Image:         image,
			Dockerfile:    dockerfile,
			ForwardEnv:    cc.ForwardEnv,
			ExtraVolumes:  volumes,
			ExtraArgs:     extraArgs,
			Persistent:    persistent,
			ContainerName: containerName,
			MountDir:      mountDir,
			SourceDir:     sourceDir,
			ProjectDir:    projectDir,
			OrgDir:        orgDir,
			HostCLIPath:   crossBuildCLI(orgDir),
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

// crossBuildCLI cross-compiles the ateam binary for linux/amd64 when the host
// is not Linux. The result is cached in orgDir/cache/ and reused if the host
// binary hasn't changed. Returns the path to the Linux binary, or "" on failure.
func crossBuildCLI(orgDir string) string {
	if goruntime.GOOS == "linux" && goruntime.GOARCH == "amd64" {
		// Already on the target platform; use the running binary directly.
		if exe, err := os.Executable(); err == nil {
			return exe
		}
		return ""
	}

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: cannot find ateam binary for cross-build: %v\n", err)
		return ""
	}
	exe, _ = filepath.EvalSymlinks(exe)

	// The go module dir is the directory containing the binary (make build outputs there).
	modDir := filepath.Dir(exe)
	goMod := filepath.Join(modDir, "go.mod")
	if _, err := os.Stat(goMod); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: go.mod not found next to %s, cannot cross-build\n", exe)
		return ""
	}

	cacheDir := filepath.Join(orgDir, "cache")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return ""
	}
	target := filepath.Join(cacheDir, "ateam-linux-amd64")

	// Rebuild only if the target is missing or older than the host binary.
	hostInfo, _ := os.Stat(exe)
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
	for i, remaining := 0, vol; remaining != ""; i++ {
		idx := findVolumeSep(remaining)
		if idx < 0 {
			parts = append(parts, remaining)
			break
		}
		parts = append(parts, remaining[:idx])
		remaining = remaining[idx+1:]
	}
	return parts
}

func findVolumeSep(s string) int {
	for i, c := range s {
		if c == ':' {
			return i
		}
	}
	return -1
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
