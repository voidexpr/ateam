package cmd

import (
	"fmt"

	"github.com/ateam-poc/internal/agents"
	"github.com/ateam-poc/internal/config"
	"github.com/ateam-poc/internal/root"
	"github.com/spf13/cobra"
)

var initAgents []string

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize the current git project for ATeam",
	Long: `Discovers the source git directory and .ateam/ directory, then creates or updates
the project entry with the specified agents.

If the project already exists, new agents are merged into the config.

Example:
  ateam init --agents all
  ateam init --agents testing_basic,security`,
	Args: cobra.NoArgs,
	RunE: runInit,
}

func init() {
	initCmd.Flags().StringSliceVar(&initAgents, "agents", []string{"all"}, agents.FlagUsage())
}

func runInit(cmd *cobra.Command, args []string) error {
	agentIDs, err := agents.ResolveAgentList(initAgents)
	if err != nil {
		return err
	}

	proj, err := root.Resolve(agentIDs)
	if err != nil {
		return err
	}

	// Merge new agents into existing config
	merged := mergeAgents(proj.Config.Agents.Enabled, agentIDs)
	proj.Config.Agents.Enabled = merged
	if err := config.Save(proj.ProjectDir, *proj.Config); err != nil {
		return err
	}

	// Ensure agent dirs and prompts exist
	if err := root.EnsureAgents(proj.AteamRoot, proj.ProjectDir, agentIDs); err != nil {
		return err
	}

	fmt.Printf("Project: %s\n", proj.ProjectRelPath)
	fmt.Printf("  Source git: %s\n", proj.SourceDir)
	fmt.Printf("  ATeam root: %s\n", proj.AteamRoot)
	fmt.Printf("  Agents: %v\n", merged)
	fmt.Printf("\nNext steps:\n")
	fmt.Printf("  ateam report --agents all\n")
	fmt.Printf("  ateam review\n")

	return nil
}

func mergeAgents(existing, new []string) []string {
	seen := make(map[string]bool)
	for _, id := range existing {
		seen[id] = true
	}
	result := append([]string{}, existing...)
	for _, id := range new {
		if !seen[id] {
			result = append(result, id)
			seen[id] = true
		}
	}
	return result
}
