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
	agentsDir := filepath.Join(projectDir, "agents")
	supervisorDir := filepath.Join(projectDir, "supervisor")
	if err := os.MkdirAll(supervisorDir, 0755); err != nil {
		return fmt.Errorf("cannot create supervisor directory: %w", err)
	}

	// Write shared report instructions
	if err := writeIfNotExists(filepath.Join(agentsDir, "report_prompt.md"), DefaultReportInstructions); err != nil {
		return err
	}

	// Write supervisor prompts
	if err := writeIfNotExists(filepath.Join(supervisorDir, "prompt.md"), DefaultSupervisorRole); err != nil {
		return err
	}
	if err := writeIfNotExists(filepath.Join(supervisorDir, "review_prompt.md"), DefaultReviewInstructions); err != nil {
		return err
	}

	// Write per-agent prompts
	for _, id := range agentIDs {
		prompt, ok := DefaultAgentPrompts[id]
		if !ok {
			continue
		}
		agentDir := filepath.Join(agentsDir, id)
		if err := os.MkdirAll(agentDir, 0755); err != nil {
			return fmt.Errorf("cannot create agent directory %s: %w", id, err)
		}
		if err := writeIfNotExists(filepath.Join(agentDir, "prompt.md"), prompt); err != nil {
			return err
		}
	}

	return nil
}

// AssembleAgentPrompt reads the prompt files for an agent and combines them
// with the source directory and optional extra prompt.
func AssembleAgentPrompt(projectDir, agentID, sourceDir, extraPrompt string) (string, error) {
	agentPath := filepath.Join(projectDir, "agents", agentID, "prompt.md")
	agentPrompt, err := os.ReadFile(agentPath)
	if err != nil {
		return "", fmt.Errorf("cannot read agent prompt for %s: %w", agentID, err)
	}

	instrPath := filepath.Join(projectDir, "agents", "report_prompt.md")
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
	agentsDir := filepath.Join(projectDir, "agents")
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		return "", fmt.Errorf("cannot read agents directory: %w (run 'ateam report' first)", err)
	}

	var reportContents []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		reportPath := filepath.Join(agentsDir, entry.Name(), entry.Name()+".report.md")
		data, err := os.ReadFile(reportPath)
		if err != nil {
			continue
		}
		reportContents = append(reportContents,
			fmt.Sprintf("# Agent Report: %s\n\n%s", entry.Name(), string(data)))
	}

	if len(reportContents) == 0 {
		return "", fmt.Errorf("no report files found in %s — run 'ateam report' first", agentsDir)
	}

	allReports := strings.Join(reportContents, "\n\n---\n\n")

	if customPrompt != "" {
		return customPrompt + "\n\n---\n\n# Agent Reports\n\n" + allReports, nil
	}

	supervisorPath := filepath.Join(projectDir, "supervisor", "prompt.md")
	supervisorPrompt, err := os.ReadFile(supervisorPath)
	if err != nil {
		return "", fmt.Errorf("cannot read supervisor prompt: %w", err)
	}

	reviewPath := filepath.Join(projectDir, "supervisor", "review_prompt.md")
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
