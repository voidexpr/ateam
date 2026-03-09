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
	CodeManagementPromptFile        = "code_management.md"
	FullReportFile                  = "full_report.md"
	FullReportErrorFile             = "full_report_error.md"
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

// AssembleAgentPrompt builds the full prompt for an agent report run.
// Resolution order for both role prompt and global instructions: project → org → org defaults.
func AssembleAgentPrompt(orgDir, projectDir, agentID, sourceDir, extraPrompt string, pinfo ProjectInfoParams) (string, error) {
	return assembleAgentAction(orgDir, projectDir, agentID, sourceDir, extraPrompt, pinfo, ReportPromptFile)
}

// AssembleAgentCodePrompt builds the full prompt for an agent code run.
func AssembleAgentCodePrompt(orgDir, projectDir, agentID, sourceDir, extraPrompt string, pinfo ProjectInfoParams) (string, error) {
	return assembleAgentAction(orgDir, projectDir, agentID, sourceDir, extraPrompt, pinfo, CodePromptFile)
}

func assembleAgentAction(orgDir, projectDir, agentID, sourceDir, extraPrompt string, pinfo ProjectInfoParams, promptFile string) (string, error) {
	rolePrompt, err := readWith3LevelFallback(
		filepath.Join(projectDir, "agents", agentID, promptFile),
		filepath.Join(orgDir, "agents", agentID, promptFile),
		filepath.Join(orgDir, "defaults", "agents", agentID, promptFile),
		"agent "+agentID,
	)
	if err != nil {
		return "", err
	}

	// Global instructions use the same file name (e.g. report_prompt.md, code_prompt.md).
	instructions := readFileOr3Level(
		filepath.Join(projectDir, promptFile),
		filepath.Join(orgDir, promptFile),
		filepath.Join(orgDir, "defaults", promptFile),
	)

	promptContent := rolePrompt
	if instructions != "" {
		promptContent += "\n\n---\n\n" + instructions
	}

	promptContent = strings.ReplaceAll(promptContent, "{{SOURCE_DIR}}", sourceDir)

	var parts []string
	if info := FormatProjectInfo(pinfo); info != "" {
		parts = append(parts, info)
	}
	parts = append(parts, promptContent)

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
func AssembleReviewPrompt(orgDir, projectDir string, pinfo ProjectInfoParams, extraPrompt, customPrompt string) (string, error) {
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

	var manifest string
	if len(manifestLines) > 0 {
		manifest = "# Reports Under Review\n\n| Agent | Generated |\n|-------|----------|\n" +
			strings.Join(manifestLines, "\n")
	}

	projectInfo := FormatProjectInfo(pinfo)

	if customPrompt != "" {
		var parts []string
		if projectInfo != "" {
			parts = append(parts, projectInfo)
		}
		parts = append(parts, customPrompt)
		if manifest != "" {
			parts = append(parts, manifest)
		}
		parts = append(parts, "# Agent Reports\n\n"+allReports)
		return strings.Join(parts, "\n\n---\n\n"), nil
	}

	supervisorPrompt, err := readWith3LevelFallback(
		filepath.Join(projectDir, "supervisor", ReviewPromptFile),
		filepath.Join(orgDir, "supervisor", ReviewPromptFile),
		filepath.Join(orgDir, "defaults", "supervisor", ReviewPromptFile),
		"supervisor",
	)
	if err != nil {
		return "", err
	}

	var parts []string
	if projectInfo != "" {
		parts = append(parts, projectInfo)
	}
	parts = append(parts, supervisorPrompt)
	if manifest != "" {
		parts = append(parts, manifest)
	}
	parts = append(parts, "# Agent Reports\n\n"+allReports)
	if extraPrompt != "" {
		parts = append(parts, "# Additional Instructions\n\n"+extraPrompt)
	}

	return strings.Join(parts, "\n\n---\n\n"), nil
}

// AssembleCodeManagementPrompt builds the full prompt for a supervisor code run.
// reviewContent is the review document to include. customPrompt overrides 3-level fallback if non-empty.
func AssembleCodeManagementPrompt(orgDir, projectDir, sourceDir string, pinfo ProjectInfoParams, reviewContent, customPrompt string) (string, error) {
	var mgmtPrompt string
	var err error

	if customPrompt != "" {
		mgmtPrompt = customPrompt
	} else {
		mgmtPrompt, err = readWith3LevelFallback(
			filepath.Join(projectDir, "supervisor", CodeManagementPromptFile),
			filepath.Join(orgDir, "supervisor", CodeManagementPromptFile),
			filepath.Join(orgDir, "defaults", "supervisor", CodeManagementPromptFile),
			"code management",
		)
		if err != nil {
			return "", err
		}
	}

	mgmtPrompt = strings.ReplaceAll(mgmtPrompt, "{{SOURCE_DIR}}", sourceDir)

	var parts []string
	if info := FormatProjectInfo(pinfo); info != "" {
		parts = append(parts, info)
	}
	parts = append(parts, mgmtPrompt)

	parts = append(parts, "# Review\n\n"+reviewContent)

	return strings.Join(parts, "\n\n---\n\n"), nil
}

// ProjectInfoParams holds the values needed to build the project info section.
type ProjectInfoParams struct {
	OrgDir      string // absolute path to .ateamorg/
	ProjectDir  string // absolute path to .ateam/
	ProjectName string
	ProjectUUID string
	SourceDir   string // absolute path to project root
	GitRepoDir  string // absolute path to git repo root (may differ from SourceDir)
	Role        string // e.g. "agent security" or "the supervisor"
	Meta        *gitutil.ProjectMeta
}

// FormatProjectInfo builds the ateam project context section.
// Returns "" if p has no Role set (zero value).
func FormatProjectInfo(p ProjectInfoParams) string {
	if p.Role == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("# ATeam Project Context\n\n")
	b.WriteString("You are part of the ateam software:\n")
	fmt.Fprintf(&b, "* runtime files: %s\n", p.OrgDir)
	fmt.Fprintf(&b, "* project name: %s\n", p.ProjectName)
	fmt.Fprintf(&b, "* project UUID: %s\n", p.ProjectUUID)
	fmt.Fprintf(&b, "* role: %s\n", p.Role)
	fmt.Fprintf(&b, "* source code: %s\n", p.SourceDir)
	if p.GitRepoDir != "" && p.GitRepoDir != p.SourceDir {
		fmt.Fprintf(&b, "  * allowed to read but not modify up to: %s\n", p.GitRepoDir)
	}
	fmt.Fprintf(&b, "* reports and reviews: %s\n", p.ProjectDir)
	if p.Meta != nil {
		ts := time.Now().Format("2006-01-02 15:04:05 MST")
		fmt.Fprintf(&b, "* timestamp: %s\n", ts)
		hash := p.Meta.CommitHash
		if len(hash) > 12 {
			hash = hash[:12]
		}
		fmt.Fprintf(&b, "* last commit: %s - %s - \"%s\"\n", hash, p.Meta.CommitDate, p.Meta.CommitMessage)
		if len(p.Meta.Uncommitted) > 0 {
			fmt.Fprintf(&b, "* uncommitted changes: %d file(s)\n", len(p.Meta.Uncommitted))
			for _, f := range p.Meta.Uncommitted {
				fmt.Fprintf(&b, "  * `%s`\n", f)
			}
		} else {
			b.WriteString("* working tree: clean\n")
		}
	}
	return b.String()
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

// readFileOr3Level tries three paths and returns the first one that exists, or "" if none do.
func readFileOr3Level(projectPath, orgPath, defaultPath string) string {
	for _, p := range []string{projectPath, orgPath, defaultPath} {
		if data, err := os.ReadFile(p); err == nil {
			return string(data)
		}
	}
	return ""
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
