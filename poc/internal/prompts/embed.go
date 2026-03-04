package prompts

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ateam-poc/internal/agents"
)

//go:embed defaults/agents/*/report_prompt.md defaults/report_instructions.md defaults/supervisor/review_prompt.md
var defaultsFS embed.FS

func readEmbedded(name string) string {
	data, err := defaultsFS.ReadFile(name)
	if err != nil {
		panic(fmt.Sprintf("missing embedded prompt file: %s", name))
	}
	return string(data)
}

func defaultAgentPrompt(agentID string) string {
	return readEmbedded(fmt.Sprintf("defaults/agents/%s/report_prompt.md", agentID))
}

func defaultReportInstructions() string {
	return readEmbedded("defaults/report_instructions.md")
}

// CombinedAgentPrompt returns the default agent role prompt combined with
// report instructions for the given agent ID.
func CombinedAgentPrompt(agentID string) string {
	return defaultAgentPrompt(agentID) + "\n\n---\n\n" + defaultReportInstructions()
}

// CombinedSupervisorPrompt returns the default supervisor role combined with
// review instructions.
func CombinedSupervisorPrompt() string {
	return readEmbedded("defaults/supervisor/review_prompt.md")
}

// WriteRootDefaults writes default prompt files to the .ateam root directory.
// If overwrite is true, existing files are replaced; otherwise they are skipped.
func WriteRootDefaults(ateamRoot string, overwrite bool) error {
	write := WriteIfNotExists
	if overwrite {
		write = func(path, content string) error {
			return os.WriteFile(path, []byte(content), 0644)
		}
	}

	for _, id := range agents.AllAgentIDs {
		agentDir := filepath.Join(ateamRoot, "agents", id)
		if err := os.MkdirAll(agentDir, 0755); err != nil {
			return fmt.Errorf("cannot create agent directory %s: %w", id, err)
		}
		if err := write(filepath.Join(agentDir, ReportPromptFile), CombinedAgentPrompt(id)); err != nil {
			return err
		}
	}

	supervisorDir := filepath.Join(ateamRoot, "supervisor")
	if err := os.MkdirAll(supervisorDir, 0755); err != nil {
		return fmt.Errorf("cannot create supervisor directory: %w", err)
	}
	return write(filepath.Join(supervisorDir, ReviewPromptFile), CombinedSupervisorPrompt())
}
