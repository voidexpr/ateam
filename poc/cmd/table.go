package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/ateam-poc/internal/agent"
	"github.com/ateam-poc/internal/container"
	"github.com/ateam-poc/internal/root"
	"github.com/ateam-poc/internal/runner"
	"github.com/ateam-poc/internal/runtime"
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
	r.ExtraArgs = append(r.ExtraArgs, prof.AgentExtraArgs...)
	ct, err := buildContainer(cc, prof, env.SourceDir, env.ProjectDir, env.OrgDir, env.GitRepoDir, roleID)
	if err != nil {
		return nil, err
	}
	r.Container = ct
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

	return runnerFromAgentConfig(env, &ac), nil
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
	return &runner.Runner{
		Agent:           buildAgent(ac),
		LogFile:         env.RunnerLogPath(),
		ProjectDir:      env.ProjectDir,
		OrgDir:          env.OrgDir,
		ExtraWriteDirs:  []string{env.OrgDir},
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
		return minimalRunnerFromAgentConfig(orgDir, &ac), nil
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

// buildAgent constructs an agent.Agent from config.
func buildAgent(ac *runtime.AgentConfig) agent.Agent {
	switch ac.Type {
	case "builtin":
		return &agent.MockAgent{}
	case "codex":
		cmd := ac.Command
		if cmd == "" {
			cmd = "codex"
		}
		return &agent.CodexAgent{
			Command: cmd,
			Args:    ac.Args,
			Model:   ac.Model,
			Env:     ac.Env,
		}
	default:
		cmd := ac.Command
		if cmd == "" {
			cmd = ac.Name
		}
		return &agent.ClaudeAgent{
			Command: cmd,
			Args:    ac.Args,
			Model:   ac.Model,
			Env:     ac.Env,
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
		return &container.DockerContainer{
			Image:        image,
			Dockerfile:   dockerfile,
			ForwardEnv:   cc.ForwardEnv,
			ExtraVolumes: volumes,
			ExtraArgs:    extraArgs,
			MountDir:     mountDir,
			SourceDir:    sourceDir,
			ProjectDir:   projectDir,
			OrgDir:       orgDir,
		}, nil
	default:
		return nil, nil
	}
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
