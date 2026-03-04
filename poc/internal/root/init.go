package root

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ateam-poc/internal/config"
	"github.com/ateam-poc/internal/prompts"
)

// Install creates a new .ateam/ directory at parentDir with default prompts.
func Install(parentDir string) (string, error) {
	ateamRoot := filepath.Join(parentDir, ".ateam")

	for _, dir := range []string{
		filepath.Join(ateamRoot, "projects"),
		filepath.Join(ateamRoot, "expertise"),
	} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return "", fmt.Errorf("cannot create directory %s: %w", dir, err)
		}
	}

	if err := prompts.WriteRootDefaults(ateamRoot, false); err != nil {
		return "", err
	}

	return ateamRoot, nil
}

// AutoInitProject creates a project entry under ateamRoot/projects/.
func AutoInitProject(ateamRoot, sourceDir, relPath string, agentIDs []string) (string, error) {
	projectDir := filepath.Join(ateamRoot, "projects", relPath)

	dirs := []string{
		projectDir,
		filepath.Join(projectDir, "supervisor", "history"),
	}
	for _, agentID := range agentIDs {
		dirs = append(dirs, filepath.Join(projectDir, "agents", agentID, "history"))
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return "", fmt.Errorf("cannot create directory %s: %w", dir, err)
		}
	}

	name := filepath.Base(relPath)
	ateamParent := filepath.Dir(ateamRoot)
	relSourceDir, err := filepath.Rel(ateamParent, sourceDir)
	if err != nil {
		relSourceDir = sourceDir
	}
	cfg := config.DefaultConfig(name, relSourceDir, agentIDs)
	if err := config.Save(projectDir, cfg); err != nil {
		return "", err
	}

	return projectDir, nil
}

// EnsureAgents creates missing agent dirs and prompt files for the given agents.
func EnsureAgents(ateamRoot, projectDir string, agentIDs []string) error {
	for _, agentID := range agentIDs {
		// Root-level default prompt
		rootAgentDir := filepath.Join(ateamRoot, "agents", agentID)
		if err := os.MkdirAll(rootAgentDir, 0755); err != nil {
			return fmt.Errorf("cannot create agent directory %s: %w", rootAgentDir, err)
		}
		content := prompts.CombinedAgentPrompt(agentID)
		if content != "" {
			if err := prompts.WriteIfNotExists(filepath.Join(rootAgentDir, prompts.ReportPromptFile), content); err != nil {
				return fmt.Errorf("cannot write default prompt for %s: %w", agentID, err)
			}
		}

		// Project-level agent dir with history
		if err := os.MkdirAll(filepath.Join(projectDir, "agents", agentID, "history"), 0755); err != nil {
			return fmt.Errorf("cannot create project agent directory: %w", err)
		}
	}
	return nil
}
