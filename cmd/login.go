package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/config"
	"github.com/ateam/internal/display"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runtime"
	"github.com/spf13/cobra"
)

var (
	loginProfile string
	loginAgent   string
	loginDryRun  bool
)

var loginCmd = &cobra.Command{
	Use:   "login [--profile NAME | --agent NAME] [-- LOGIN_ARGS...]",
	Short: "Log into the agent CLI that unattended runs use",
	Long: `Run the agent CLI's own interactive login against the config directory
the unattended agent uses (the agent's config_dir in runtime.hcl), so the
credentials land where ateam's runs will look for them.

The agent is picked like a run picks it: --agent, else --profile, else the
project's default profile. Credential env vars that would shadow a stored
login (CLAUDE_CODE_OAUTH_TOKEN, ANTHROPIC_API_KEY, OPENAI_API_KEY) are
removed from the login process. Works on any OS. Inside a container,
'ateam claude' starts an interactive session with the same config dir.

Examples:
  ateam login                       # the default profile's agent
  ateam login --profile codex
  ateam login --agent claude-high
  ateam login --dry-run             # show the command and env, run nothing
  ateam login -- --help             # pass args to the CLI's login command`,
	Args: cobra.ArbitraryArgs,
	RunE: runLogin,
}

func init() {
	addProfileFlags(loginCmd, &loginProfile, &loginAgent)
	loginCmd.Flags().BoolVar(&loginDryRun, "dry-run", false, "show what would be executed without running")
}

// loginTarget is the agent an interactive login (or `ateam claude`) acts on.
type loginTarget struct {
	Agent     *runtime.AgentConfig
	Type      string // agent type with the claude default applied
	Source    string // what picked the agent: --agent, --profile, or a config.toml setting
	ConfigDir string // absolute config dir; "" when the agent uses the CLI's own default
}

// resolveLoginTarget picks the agent the way a run would (--agent, then
// --profile, then the project's default profile) and resolves its
// config_dir. env may be nil (no org or project found).
func resolveLoginTarget(env *root.ResolvedEnv, rtCfg *runtime.Config, profileFlag, agentFlag string) (loginTarget, error) {
	var (
		ac     *runtime.AgentConfig
		source string
	)
	switch {
	case profileFlag != "" && agentFlag != "":
		return loginTarget{}, fmt.Errorf("--profile and --agent are mutually exclusive")
	case agentFlag != "":
		a, ok := rtCfg.Agents[agentFlag]
		if !ok {
			return loginTarget{}, fmt.Errorf("unknown agent %q", agentFlag)
		}
		ac, source = &a, config.ProfileSourceAgent
	default:
		choice := config.ProfileChoice{Name: profileFlag, Source: config.ProfileSourceFlag}
		if profileFlag == "" {
			choice = config.ProfileChoice{Name: "default", Source: config.ProfileSourceBuiltin}
			if env != nil && env.Config != nil {
				choice = env.Config.ResolveProfile("", "")
			}
		}
		_, a, _, err := rtCfg.ResolveProfile(choice.Name)
		if err != nil {
			// config.toml may name an agent where a profile is expected (see newRunner).
			shorthand, ok := rtCfg.Agents[choice.Name]
			if !ok {
				return loginTarget{}, err
			}
			a = &shorthand
		}
		ac, source = a, fmt.Sprintf("profile %q, chosen by %s", choice.Name, choice.Source)
	}

	stateDir := ""
	if env != nil {
		stateDir = env.StateDir()
	}
	dir, err := agentConfigDirPath(ac, stateDir)
	if err != nil {
		return loginTarget{}, err
	}
	return loginTarget{Agent: ac, Type: ac.TypeName(), Source: source, ConfigDir: dir}, nil
}

// agentConfigDirPath resolves an agent's config_dir the way the runner does:
// ~ expanded, relative paths anchored at the state dir (.ateam/, else
// ~/.ateam/state). Template variables cannot be resolved outside a run.
func agentConfigDirPath(ac *runtime.AgentConfig, stateDir string) (string, error) {
	dir := display.ExpandHome(ac.ConfigDir)
	switch {
	case dir == "":
		return "", nil
	case strings.Contains(dir, "{{"):
		return "", fmt.Errorf("agent %q config_dir %q uses template variables that only resolve during a run", ac.Name, ac.ConfigDir)
	case filepath.IsAbs(dir):
		return dir, nil
	case stateDir == "":
		return "", fmt.Errorf("agent %q has a relative config_dir %q but no project .ateam/ or user ateam directory was found to anchor it", ac.Name, ac.ConfigDir)
	}
	return filepath.Join(stateDir, dir), nil
}

// agentCLIEnv builds the process env for an interactive use of the agent
// CLI: the agent block's env map applied (empty value = unset), the
// credential vars that shadow a stored login removed, and the config dir
// var set when the agent has one. Returns the env plus the vars it unset.
func agentCLIEnv(t loginTarget) (env []string, unset []string) {
	env = os.Environ()
	for k, v := range t.Agent.Env {
		if v == "" {
			env = unsetEnv(env, k)
		} else {
			env = setEnv(env, k, v)
		}
	}
	for _, key := range agent.AuthEnvVars(t.Type) {
		if os.Getenv(key) != "" {
			unset = append(unset, key)
		}
		env = unsetEnv(env, key)
	}
	if t.ConfigDir != "" {
		env = setEnv(env, agent.ConfigDirEnv(t.Type), t.ConfigDir)
	}
	return env, unset
}

func agentBinary(t loginTarget) (string, error) {
	command := t.Agent.Command
	if command == "" {
		command = agent.DefaultCommand(t.Type)
	}
	if command == "" {
		return "", fmt.Errorf("agent %q (type %q) has no CLI to log into", t.Agent.Name, t.Type)
	}
	binary, err := exec.LookPath(command)
	if err != nil {
		return "", fmt.Errorf("%s not found in PATH: %w", command, err)
	}
	return binary, nil
}

func runLogin(cmd *cobra.Command, args []string) error {
	env, _ := lookupEnvOptional()
	rtCfg, err := loadRuntime(env)
	if err != nil {
		return err
	}
	target, err := resolveLoginTarget(env, rtCfg, loginProfile, loginAgent)
	if err != nil {
		return err
	}
	loginArgs := agent.LoginArgs(target.Type)
	if loginArgs == nil {
		return fmt.Errorf("agent %q (type %q) has no interactive login", target.Agent.Name, target.Type)
	}
	binary, err := agentBinary(target)
	if err != nil {
		return err
	}
	loginArgs = append(loginArgs, args...)
	childEnv, unset := agentCLIEnv(target)

	prefix := ""
	if loginDryRun {
		prefix = "[dry-run] "
	}
	fmt.Printf("  %sagent %s (%s)\n", prefix, target.Agent.Name, target.Source)
	if target.ConfigDir != "" {
		fmt.Printf("  %sset   %s=%s\n", prefix, agent.ConfigDirEnv(target.Type), target.ConfigDir)
	} else {
		fmt.Printf("  %sconfig dir: the %s CLI default (no config_dir on this agent)\n", prefix, target.Type)
	}
	for _, key := range unset {
		fmt.Printf("  %sunset %s\n", prefix, key)
	}
	fmt.Printf("  %srun   %s %s\n\n", prefix, binary, strings.Join(loginArgs, " "))
	if loginDryRun {
		return nil
	}

	if target.ConfigDir != "" {
		if err := os.MkdirAll(target.ConfigDir, 0700); err != nil {
			return fmt.Errorf("cannot create config dir %s: %w", target.ConfigDir, err)
		}
	}
	login := exec.Command(binary, loginArgs...)
	login.Env = childEnv
	login.Stdin, login.Stdout, login.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := login.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", filepath.Base(binary), strings.Join(loginArgs, " "), err)
	}

	status := exec.Command(binary, agent.AuthStatusArgs(target.Type)...)
	status.Env = childEnv
	status.Stdout, status.Stderr = os.Stdout, os.Stderr
	fmt.Println()
	_ = status.Run()
	return nil
}
