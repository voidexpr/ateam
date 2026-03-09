package cmd

import (
	"fmt"
	"sort"

	"github.com/ateam-poc/internal/root"
	"github.com/spf13/cobra"
)

var (
	agentsEnabled   bool
	agentsAvailable bool
)

var agentsCmd = &cobra.Command{
	Use:   "agents",
	Short: "List agents for the current project",
	Long: `List agents configured for the current project.

By default (--available), shows all known agents with their status.
With --enabled, shows only enabled agents.

Example:
  ateam agents
  ateam agents --enabled
  ateam agents --available`,
	Args: cobra.NoArgs,
	RunE: runAgents,
}

func init() {
	agentsCmd.Flags().BoolVar(&agentsEnabled, "enabled", false, "list enabled agents only")
	agentsCmd.Flags().BoolVar(&agentsAvailable, "available", false, "list all agents with status (default)")
	agentsCmd.MarkFlagsMutuallyExclusive("enabled", "available")
}

func runAgents(cmd *cobra.Command, args []string) error {
	env, err := root.Resolve(orgFlag, projectFlag)
	if err != nil {
		return err
	}

	if env.Config == nil || len(env.Config.Agents) == 0 {
		fmt.Println("No agents configured.")
		return nil
	}

	if agentsEnabled {
		agents := env.Config.EnabledAgents()
		if len(agents) == 0 {
			fmt.Println("No enabled agents.")
			return nil
		}
		for _, name := range agents {
			fmt.Println(name)
		}
		return nil
	}

	// Default: --available — all agents with status
	var names []string
	for name := range env.Config.Agents {
		names = append(names, name)
	}
	sort.Strings(names)

	w := newTable()
	fmt.Fprintln(w, "AGENT\tSTATUS")
	for _, name := range names {
		fmt.Fprintf(w, "%s\t%s\n", name, env.Config.Agents[name])
	}
	w.Flush()

	return nil
}
