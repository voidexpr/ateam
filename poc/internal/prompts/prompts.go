package prompts

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ateam-poc/internal/gitutil"
)

const (
	ReportPromptFile                = "report_prompt.md"
	CodePromptFile                  = "code_prompt.md"
	ExtraReportPromptFile           = "extra_report_prompt.md"
	ReviewPromptFile                = "review_prompt.md"
	ReportCommissioningPromptFile   = "report_commissioning_prompt.md"
	FullReportFile                  = "full_report.md"
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
// Resolution order for role prompt: project → org agents → org defaults.
// Report instructions always come from org defaults.
// meta is optional — if nil, git metadata is omitted from the prompt.
func AssembleAgentPrompt(orgDir, projectDir, agentID, sourceDir, extraPrompt string, meta *gitutil.ProjectMeta) (string, error) {
	rolePrompt, err := readWith3LevelFallback(
		filepath.Join(projectDir, "agents", agentID, ReportPromptFile),
		filepath.Join(orgDir, "agents", agentID, ReportPromptFile),
		filepath.Join(orgDir, "defaults", "agents", agentID, ReportPromptFile),
		"agent "+agentID,
	)
	if err != nil {
		return "", err
	}

	instructions := readFileOr(
		filepath.Join(orgDir, "defaults", ReportPromptFile),
		"",
	)

	promptContent := rolePrompt
	if instructions != "" {
		promptContent += "\n\n---\n\n" + instructions
	}

	promptContent = strings.ReplaceAll(promptContent, "{{SOURCE_DIR}}", sourceDir)

	parts := []string{promptContent}

	if meta != nil {
		parts = append(parts, gitutil.FormatMetadataSection(meta, time.Now()))
	}

	extraFilePath := filepath.Join(projectDir, "agents", agentID, ExtraReportPromptFile)
	if data, err := os.ReadFile(extraFilePath); err == nil {
		parts = append(parts, "# Project-Specific Instructions\n\n"+string(data))
	}

	if extraPrompt != "" {
		parts = append(parts, "# Additional Instructions\n\n"+extraPrompt)
	}

	return strings.Join(parts, "\n\n---\n\n"), nil
}

// AgentReport holds metadata about a discovered agent report file.
type AgentReport struct {
	AgentID string
	Path    string
	ModTime time.Time
	Content string
}

// DiscoverReports scans the project's agents directory for full_report.md files.
func DiscoverReports(projectDir string) ([]AgentReport, error) {
	agentsDir := filepath.Join(projectDir, "agents")
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		return nil, fmt.Errorf("cannot read agents directory: %w (run 'ateam report' first)", err)
	}

	var reports []AgentReport
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		reportPath := filepath.Join(agentsDir, entry.Name(), FullReportFile)
		data, err := os.ReadFile(reportPath)
		if err != nil {
			continue
		}
		info, err := os.Stat(reportPath)
		if err != nil {
			continue
		}
		reports = append(reports, AgentReport{
			AgentID: entry.Name(),
			Path:    reportPath,
			ModTime: info.ModTime(),
			Content: string(data),
		})
	}
	return reports, nil
}

// AssembleReviewPrompt builds the full prompt for a supervisor review.
// meta is optional — if nil, git metadata is omitted from the prompt.
func AssembleReviewPrompt(orgDir, projectDir string, meta *gitutil.ProjectMeta, extraPrompt, customPrompt string) (string, error) {
	reports, err := DiscoverReports(projectDir)
	if err != nil {
		return "", err
	}
	if len(reports) == 0 {
		return "", fmt.Errorf("no report files found in %s/agents — run 'ateam report' first", projectDir)
	}

	var reportContents []string
	var manifestLines []string
	for _, r := range reports {
		reportContents = append(reportContents,
			fmt.Sprintf("# Agent Report: %s\n\n%s", r.AgentID, r.Content))
		manifestLines = append(manifestLines,
			fmt.Sprintf("| %s | %s |", r.AgentID, r.ModTime.Format("2006-01-02 15:04:05")))
	}

	allReports := strings.Join(reportContents, "\n\n---\n\n")

	var contextParts []string

	if len(manifestLines) > 0 {
		manifest := "# Reports Under Review\n\n| Agent | Generated |\n|-------|----------|\n" +
			strings.Join(manifestLines, "\n")
		contextParts = append(contextParts, manifest)
	}

	if meta != nil {
		contextParts = append(contextParts, gitutil.FormatMetadataSection(meta, time.Now()))
	}

	contextSection := strings.Join(contextParts, "\n\n")

	if customPrompt != "" {
		parts := []string{customPrompt}
		if contextSection != "" {
			parts = append(parts, contextSection)
		}
		parts = append(parts, "# Agent Reports\n\n"+allReports)
		return strings.Join(parts, "\n\n---\n\n"), nil
	}

	supervisorPrompt, err := readWith3LevelFallback(
		filepath.Join(projectDir, "supervisor", ReviewPromptFile),
		filepath.Join(orgDir, "agents", "supervisor", ReviewPromptFile),
		filepath.Join(orgDir, "defaults", "supervisor", ReviewPromptFile),
		"supervisor",
	)
	if err != nil {
		return "", err
	}

	parts := []string{supervisorPrompt}
	if contextSection != "" {
		parts = append(parts, contextSection)
	}
	parts = append(parts, "# Agent Reports\n\n"+allReports)
	if extraPrompt != "" {
		parts = append(parts, "# Additional Instructions\n\n"+extraPrompt)
	}

	return strings.Join(parts, "\n\n---\n\n"), nil
}

// readWith3LevelFallback tries projectPath, then orgPath, then defaultPath.
func readWith3LevelFallback(projectPath, orgPath, defaultPath, label string) (string, error) {
	if data, err := os.ReadFile(projectPath); err == nil {
		return string(data), nil
	}
	if data, err := os.ReadFile(orgPath); err == nil {
		return string(data), nil
	}
	if data, err := os.ReadFile(defaultPath); err == nil {
		return string(data), nil
	}
	return "", fmt.Errorf("no prompt found for %s (checked %s, %s, and %s)", label, projectPath, orgPath, defaultPath)
}

// readFileOr reads a file or returns fallback if it can't be read.
func readFileOr(path, fallback string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return fallback
	}
	return string(data)
}

// WriteIfNotExists writes content to path only if the file does not already exist.
func WriteIfNotExists(path, content string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	_, err = f.WriteString(content)
	return err
}
