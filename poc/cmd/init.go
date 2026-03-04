package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ateam-poc/internal/agents"
	"github.com/ateam-poc/internal/config"
	"github.com/ateam-poc/internal/prompts"
	"github.com/spf13/cobra"
)

var (
	initSource  string
	initAgents  []string
	initWorkDir string
)

var initCmd = &cobra.Command{
	Use:   "init NAME",
	Short: "Initialize an ATeam project directory",
	Long: `Create an ATeam working directory with prompt files and config for the given project.

If the directory already exists, only new agents are added (existing prompts are not overwritten).

Example:
  ateam init myproject --source /path/to/code --agents all
  ateam init myproject --source /path/to/code --agents testing_basic,security,refactor_small`,
	Args: cobra.ExactArgs(1),
	RunE: runInit,
}

func init() {
	initCmd.Flags().StringVar(&initSource, "source", "", "path to project source code (required)")
	initCmd.Flags().StringSliceVar(&initAgents, "agents", []string{"all"}, "comma-separated agent list, or 'all'")
	initCmd.Flags().StringVar(&initWorkDir, "work-dir", "", "parent directory for the project (default: current directory)")
	_ = initCmd.MarkFlagRequired("source")
}

func runInit(cmd *cobra.Command, args []string) error {
	name := args[0]

	// Resolve source directory to absolute path
	sourceDir, err := filepath.Abs(initSource)
	if err != nil {
		return fmt.Errorf("cannot resolve source path: %w", err)
	}
	if info, err := os.Stat(sourceDir); err != nil || !info.IsDir() {
		return fmt.Errorf("source directory does not exist: %s", sourceDir)
	}

	// Resolve work directory
	workDir := initWorkDir
	if workDir == "" {
		workDir, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("cannot get current directory: %w", err)
		}
	}

	// Resolve agent list
	agentIDs, err := agents.ResolveAgentList(initAgents)
	if err != nil {
		return err
	}

	projectDir := filepath.Join(workDir, name)

	// Create directory structure
	dirs := []string{
		projectDir,
		filepath.Join(projectDir, "prompts", "agents"),
		filepath.Join(projectDir, "reports"),
		filepath.Join(projectDir, "archive"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("cannot create directory %s: %w", dir, err)
		}
	}

	// Write config.toml (only if it doesn't exist, otherwise merge agents)
	configPath := filepath.Join(projectDir, "config.toml")
	if _, err := os.Stat(configPath); err == nil {
		// Config exists — load it and add any new agents
		existing, err := config.Load(projectDir)
		if err != nil {
			return err
		}
		merged := mergeAgents(existing.Agents.Enabled, agentIDs)
		existing.Agents.Enabled = merged
		if err := config.Save(projectDir, *existing); err != nil {
			return err
		}
		fmt.Printf("Updated %s — added agents: %v\n", configPath, agentIDs)
	} else {
		// Create new config
		cfg := config.DefaultConfig(name, sourceDir, agentIDs)
		if err := config.Save(projectDir, cfg); err != nil {
			return err
		}
	}

	// Write default prompt files (skips existing)
	if err := prompts.WriteDefaults(projectDir, agentIDs); err != nil {
		return err
	}

	fmt.Printf("Initialized ATeam project: %s\n", projectDir)
	fmt.Printf("  Source: %s\n", sourceDir)
	fmt.Printf("  Agents: %v\n", agentIDs)
	fmt.Printf("\nPrompt files are in %s/prompts/ — customize them before running reports.\n", projectDir)
	fmt.Printf("\nNext steps:\n")
	fmt.Printf("  cd %s\n", projectDir)
	fmt.Printf("  ateam report --agents all\n")
	fmt.Printf("  ateam review\n")

	return nil
}

// mergeAgents adds new agent IDs to an existing list without duplicates.
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
