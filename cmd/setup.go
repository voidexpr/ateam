package cmd

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/root"
	"github.com/spf13/cobra"
)

// Where unattended agents keep their config dir (CLAUDE_CONFIG_DIR /
// CODEX_HOME): one per user, one per project, or the CLI's own default.
const (
	agentConfigHome    = "home"
	agentConfigProject = "project"
	agentConfigNone    = "none"
)

var (
	setupAgentConfig  string
	setupAgents       []string
	setupModel        bool
	setupModelChoice  string
	setupLayer        string
	setupRefreshModel bool
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Set up unattended agents: config directory, model pin, price tables",
	Long: `Two optional steps for the agents ateam runs unattended, per agent CLI
found on PATH (claude, codex). With no flags both run; a flag selects one.

Config directory (--agent-config): choose where unattended runs keep
their configuration and credentials. Advised, not required.

Why a separate directory: your interactive Claude or Codex install carries
hooks, plugins, MCP servers and notifications that are noise or interfere
in an unattended run, and a separate directory keeps unattended sessions
and history out of the interactive one. Nothing is copied; 'ateam login'
creates the credentials there.

Choices, per agent CLI found on PATH:
  home     ~/.ateam/state/agents/<agent>: one unattended identity for every project (default)
  project  <project>/.ateam/agents/<agent>: this project only
  none     inherit the interactive install (~/.claude, ~/.codex)

The choice is written as config_dir on the agent block of the chosen
layer's ateam.hcl. Without a terminal, pass --agent-config.

Model pin (--model): propose a model for unattended runs. With nothing
pinned the agent CLI picks its own default, often the top model; the
pricing table's suggested_model is the tier below it, shown with its
price next to the alternatives. The pick is written as model on the
agent block of --layer (home, every project; or project).

--refresh-models fetches the current pricing tables and suggested models
from the ateam repository (ATEAM_DEFAULTS_URL overrides the source) into
a managed section of ~/.ateam/ateam.hcl. 'ateam env' warns when the
tables are older than 90 days.

Examples:
  ateam setup
  ateam setup --agent-config home
  ateam setup --agent-config project --agent codex
  ateam setup --model
  ateam setup --model-choice suggested --layer project
  ateam setup --refresh-models`,
	Args: cobra.NoArgs,
	RunE: runSetup,
}

func init() {
	setupCmd.Flags().StringVar(&setupAgentConfig, "agent-config", "", "where unattended agents keep their config: home, project, or none (default: ask)")
	setupCmd.Flags().StringSliceVar(&setupAgents, "agent", nil, "agent CLIs to configure: claude, codex (default: every one found on PATH)")
	setupCmd.Flags().BoolVar(&setupModel, "model", false, "propose a model pin for unattended runs (asks)")
	setupCmd.Flags().StringVar(&setupModelChoice, "model-choice", "", "model pin without asking: suggested, none, or a model id")
	setupCmd.Flags().StringVar(&setupLayer, "layer", agentConfigHome, "where --model writes the pin: home or project")
	setupCmd.Flags().BoolVar(&setupRefreshModel, "refresh-models", false, "fetch the current pricing tables and suggested models into ~/.ateam/ateam.hcl")
}

func runSetup(cmd *cobra.Command, args []string) error {
	env, err := lookupEnv()
	if err != nil {
		return err
	}
	if setupRefreshModel {
		return refreshPricing(env)
	}
	configStep := setupAgentConfig != "" || (!setupModel && setupModelChoice == "")
	modelStep := setupModel || setupModelChoice != "" || setupAgentConfig == ""
	if configStep {
		if err := setupAgentConfigDirs(env, setupAgentConfig, setupAgents, false); err != nil {
			return err
		}
	}
	if modelStep {
		if configStep {
			fmt.Println()
		}
		if err := setupModelPins(env, setupModelChoice, setupLayer, setupAgents); err != nil {
			return err
		}
	}
	return nil
}

// setupAgentConfigDirs runs the setup for each agent type: skips the ones
// that already have a config_dir, asks (or uses choice) for the others,
// and writes the result. firstUse is set by `ateam init` the first time
// the user directory is created, which is when asking makes sense without
// the user having run `ateam setup` explicitly.
func setupAgentConfigDirs(env *root.ResolvedEnv, choice string, agents []string, firstUse bool) error {
	if choice != "" && choice != agentConfigHome && choice != agentConfigProject && choice != agentConfigNone {
		return fmt.Errorf("--agent-config must be home, project, or none (got %q)", choice)
	}
	if len(agents) == 0 {
		agents = agentCLIsOnPath()
		if len(agents) == 0 {
			fmt.Println("No agent CLI (claude, codex) found on PATH; nothing to set up.")
			return nil
		}
	}
	interactive := choice == "" && !stdinIsPiped() && isTerminal()
	for _, agentType := range agents {
		ac, ok := env.Runtime.Agents[agentType]
		if !ok {
			return fmt.Errorf("unknown agent %q (expected claude or codex)", agentType)
		}
		if ac.ConfigDir != "" {
			src, _ := env.Runtime.Source("agent." + agentType + ".config_dir")
			fmt.Printf("%s: config_dir already set to %s (%s); log into it with 'ateam login --agent %s'\n",
				agentType, ac.ConfigDir, tildeHome(src.Layer), agentType)
			continue
		}
		pick := choice
		if pick == "" {
			if !interactive {
				fmt.Printf("%s: unattended runs inherit the interactive install; run 'ateam setup' (or pass --agent-config home|project|none) to give them their own config dir\n", agentType)
				continue
			}
			pick = promptAgentConfig(env, agentType)
		}
		if pick == agentConfigNone {
			fmt.Printf("%s: unattended runs keep using the interactive install\n", agentType)
			continue
		}
		file, value, dir, err := agentConfigPlan(env, agentType, pick)
		if err != nil {
			return err
		}
		if err := applyAgentConfig(file, agentType, value); err != nil {
			return err
		}
		fmt.Printf("%s: config_dir = %q written to %s (%s)\n", agentType, value, tildeHome(file), tildeHome(dir))
		fmt.Printf("  next: ateam login --agent %s\n", agentType)
	}
	return nil
}

// agentCLIsOnPath lists the agent types whose CLI is installed, in a fixed order.
func agentCLIsOnPath() []string {
	var out []string
	for _, t := range []string{agent.NameClaude, agent.NameCodex} {
		if _, err := exec.LookPath(agent.DefaultCommand(t)); err == nil {
			out = append(out, t)
		}
	}
	return out
}

// agentConfigPlan returns the ateam.hcl to edit, the config_dir value to
// write, and the directory it resolves to. The home choice writes an
// absolute (~-form) path because a relative config_dir resolves against
// the run's state dir, which is the project's .ateam/ inside a project.
func agentConfigPlan(env *root.ResolvedEnv, agentType, choice string) (file, value, dir string, err error) {
	switch choice {
	case agentConfigHome:
		dir = filepath.Join(env.HomeStateDir(), "agents", agentType)
		return filepath.Join(env.HomeDir, root.ConfigFile), tildeHome(dir), dir, nil
	case agentConfigProject:
		if env.ProjectDir == "" {
			return "", "", "", fmt.Errorf("--agent-config project needs a project; run from one or use --agent-config home")
		}
		value = filepath.ToSlash(filepath.Join("agents", agentType))
		return filepath.Join(env.ProjectDir, root.ConfigFile), value, filepath.Join(env.ProjectDir, value), nil
	}
	return "", "", "", fmt.Errorf("unknown choice %q", choice)
}

// applyAgentConfig appends an agent block setting config_dir to file.
// A second block with the same name merges per attribute, so appending is
// safe whatever the file already holds.
func applyAgentConfig(file, agentType, value string) error {
	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("cannot open %s: %w", file, err)
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "\n// Added by ateam setup: unattended %s runs keep their config and\n// credentials here instead of the interactive install. Log into it with:\n//   ateam login --agent %s\nagent %q {\n  config_dir = %q\n}\n", agentType, agentType, agentType, value)
	return err
}

func promptAgentConfig(env *root.ResolvedEnv, agentType string) string {
	home := tildeHome(filepath.Join(env.HomeStateDir(), "agents", agentType))
	fmt.Fprintf(os.Stderr, "\nWhere should unattended %s runs keep their config and credentials?\n", agentType)
	fmt.Fprintf(os.Stderr, "A separate directory keeps your interactive hooks, plugins, MCP servers and notifications\nout of unattended runs, and their sessions out of your history. Advised, not required.\n")
	fmt.Fprintf(os.Stderr, "  1) %s  — one identity for every project [default]\n", home)
	if env.ProjectDir != "" {
		fmt.Fprintf(os.Stderr, "  2) %s  — this project only\n", tildeHome(filepath.Join(env.ProjectDir, "agents", agentType)))
	}
	fmt.Fprintf(os.Stderr, "  3) none  — inherit the interactive install\n> ")
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return agentConfigNone
	}
	switch strings.TrimSpace(scanner.Text()) {
	case "", "1":
		return agentConfigHome
	case "2":
		if env.ProjectDir != "" {
			return agentConfigProject
		}
		return agentConfigHome
	default:
		return agentConfigNone
	}
}
