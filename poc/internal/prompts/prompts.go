package prompts

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	ReportPromptFile      = "report_prompt.md"
	ExtraReportPromptFile = "extra_report_prompt.md"
	ReviewPromptFile      = "review_prompt.md"
	FullReportFile        = "full_report.md"
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

// ResolveOptional resolves a prompt value, returning "" for empty input.
func ResolveOptional(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	return ResolveValue(value)
}

// AssembleAgentPrompt builds the full prompt for an agent run.
// Checks project-level override first, falls back to root default.
func AssembleAgentPrompt(ateamRoot, projectDir, agentID, sourceDir, extraPrompt string) (string, error) {
	promptContent, err := readWithFallback(
		filepath.Join(projectDir, "agents", agentID, ReportPromptFile),
		filepath.Join(ateamRoot, "agents", agentID, ReportPromptFile),
		"agent "+agentID,
	)
	if err != nil {
		return "", err
	}

	promptContent = strings.ReplaceAll(promptContent, "{{SOURCE_DIR}}", sourceDir)

	parts := []string{promptContent}

	extraFilePath := filepath.Join(projectDir, "agents", agentID, ExtraReportPromptFile)
	if data, err := os.ReadFile(extraFilePath); err == nil {
		parts = append(parts, "# Project-Specific Instructions\n\n"+string(data))
	}

	if extraPrompt != "" {
		parts = append(parts, "# Additional Instructions\n\n"+extraPrompt)
	}

	return strings.Join(parts, "\n\n---\n\n"), nil
}

// AssembleReviewPrompt builds the full prompt for a supervisor review.
func AssembleReviewPrompt(ateamRoot, projectDir, extraPrompt, customPrompt string) (string, error) {
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
		reportPath := filepath.Join(agentsDir, entry.Name(), FullReportFile)
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

	supervisorPrompt, err := readWithFallback(
		filepath.Join(projectDir, "supervisor", ReviewPromptFile),
		filepath.Join(ateamRoot, "supervisor", ReviewPromptFile),
		"supervisor",
	)
	if err != nil {
		return "", err
	}

	parts := []string{supervisorPrompt, "# Agent Reports\n\n" + allReports}
	if extraPrompt != "" {
		parts = append(parts, "# Additional Instructions\n\n"+extraPrompt)
	}

	return strings.Join(parts, "\n\n---\n\n"), nil
}

// readWithFallback tries projectPath first, then rootPath.
func readWithFallback(projectPath, rootPath, label string) (string, error) {
	if data, err := os.ReadFile(projectPath); err == nil {
		return string(data), nil
	}
	if data, err := os.ReadFile(rootPath); err == nil {
		return string(data), nil
	}
	return "", fmt.Errorf("no prompt found for %s (checked %s and %s)", label, projectPath, rootPath)
}

// WriteIfNotExists writes content to path only if the file does not already exist.
func WriteIfNotExists(path, content string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return os.WriteFile(path, []byte(content), 0644)
}
