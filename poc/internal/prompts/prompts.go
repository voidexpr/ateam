package prompts

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolveValue handles the @filename convention:
// if the value starts with "@", the rest is treated as a file path and read.
// Otherwise the value is returned as-is.
func ResolveValue(value string) (string, error) {
	if strings.HasPrefix(value, "@") {
		path := value[1:]
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("cannot read prompt file %s: %w", path, err)
		}
		return string(data), nil
	}
	return value, nil
}

// WriteDefaults writes all default prompt files to the project directory.
// It does not overwrite existing files.
func WriteDefaults(projectDir string, agentIDs []string) error {
	promptsDir := filepath.Join(projectDir, "prompts")
	agentsDir := filepath.Join(promptsDir, "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		return fmt.Errorf("cannot create prompts directory: %w", err)
	}

	// Write shared prompts
	sharedFiles := map[string]string{
		"report_instructions.md": DefaultReportInstructions,
		"supervisor_role.md":     DefaultSupervisorRole,
		"review_instructions.md": DefaultReviewInstructions,
	}
	for name, content := range sharedFiles {
		path := filepath.Join(promptsDir, name)
		if err := writeIfNotExists(path, content); err != nil {
			return err
		}
	}

	// Write per-agent prompts
	for _, id := range agentIDs {
		prompt, ok := DefaultAgentPrompts[id]
		if !ok {
			continue
		}
		path := filepath.Join(agentsDir, id+".md")
		if err := writeIfNotExists(path, prompt); err != nil {
			return err
		}
	}

	return nil
}

// AssembleAgentPrompt reads the prompt files for an agent and combines them
// with the source directory and optional extra prompt.
func AssembleAgentPrompt(projectDir, agentID, sourceDir, extraPrompt string) (string, error) {
	// Read agent role prompt
	agentPath := filepath.Join(projectDir, "prompts", "agents", agentID+".md")
	agentPrompt, err := os.ReadFile(agentPath)
	if err != nil {
		return "", fmt.Errorf("cannot read agent prompt for %s: %w", agentID, err)
	}

	// Read report instructions
	instrPath := filepath.Join(projectDir, "prompts", "report_instructions.md")
	instrPrompt, err := os.ReadFile(instrPath)
	if err != nil {
		return "", fmt.Errorf("cannot read report instructions: %w", err)
	}

	// Replace source dir placeholder
	instructions := strings.ReplaceAll(string(instrPrompt), "{{SOURCE_DIR}}", sourceDir)

	var parts []string
	parts = append(parts, string(agentPrompt))
	parts = append(parts, instructions)
	if extraPrompt != "" {
		parts = append(parts, "# Additional Instructions\n\n"+extraPrompt)
	}

	return strings.Join(parts, "\n\n---\n\n"), nil
}

// AssembleReviewPrompt reads the supervisor prompt files and appends all report contents.
func AssembleReviewPrompt(projectDir, extraPrompt, customPrompt string) (string, error) {
	// Gather all report files
	reportsDir := filepath.Join(projectDir, "reports")
	entries, err := os.ReadDir(reportsDir)
	if err != nil {
		return "", fmt.Errorf("cannot read reports directory: %w (run 'ateam report' first)", err)
	}

	var reportContents []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".report.md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(reportsDir, entry.Name()))
		if err != nil {
			continue
		}
		agentName := strings.TrimSuffix(entry.Name(), ".report.md")
		reportContents = append(reportContents,
			fmt.Sprintf("# Agent Report: %s\n\n%s", agentName, string(data)))
	}

	if len(reportContents) == 0 {
		return "", fmt.Errorf("no report files found in %s — run 'ateam report' first", reportsDir)
	}

	allReports := strings.Join(reportContents, "\n\n---\n\n")

	// If custom prompt provided, use it directly with reports appended
	if customPrompt != "" {
		return customPrompt + "\n\n---\n\n# Agent Reports\n\n" + allReports, nil
	}

	// Otherwise assemble from prompt files
	supervisorPath := filepath.Join(projectDir, "prompts", "supervisor_role.md")
	supervisorPrompt, err := os.ReadFile(supervisorPath)
	if err != nil {
		return "", fmt.Errorf("cannot read supervisor prompt: %w", err)
	}

	reviewPath := filepath.Join(projectDir, "prompts", "review_instructions.md")
	reviewPrompt, err := os.ReadFile(reviewPath)
	if err != nil {
		return "", fmt.Errorf("cannot read review instructions: %w", err)
	}

	var parts []string
	parts = append(parts, string(supervisorPrompt))
	parts = append(parts, string(reviewPrompt))
	parts = append(parts, "# Agent Reports\n\n"+allReports)
	if extraPrompt != "" {
		parts = append(parts, "# Additional Instructions\n\n"+extraPrompt)
	}

	return strings.Join(parts, "\n\n---\n\n"), nil
}

func writeIfNotExists(path, content string) error {
	if _, err := os.Stat(path); err == nil {
		return nil // file exists, skip
	}
	return os.WriteFile(path, []byte(content), 0644)
}
