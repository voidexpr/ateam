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
	agentMap := make(map[string]string, len(agentIDs))
	for _, id := range agentIDs {
		agentMap[id] = "enabled"
	}
	cfg := config.DefaultConfig(name, relSourceDir, agentMap)
	if err := config.Save(projectDir, cfg); err != nil {
		return "", err
	}

	return projectDir, nil
}

// EnsureAgents creates missing agent dirs under the project for the given agents.
func EnsureAgents(projectDir string, agentIDs []string) error {
	for _, agentID := range agentIDs {
		if err := os.MkdirAll(filepath.Join(projectDir, "agents", agentID, "history"), 0755); err != nil {
			return fmt.Errorf("cannot create project agent directory: %w", err)
		}
	}
	return nil
}
