package prompts

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ateam-poc/internal/gitutil"
)

const (
	ReportPromptFile                = "report_prompt.md"
	ReportBasePromptFile            = "report_base_prompt.md"
	ReportExtraPromptFile           = "report_extra_prompt.md"
	CodePromptFile                  = "code_prompt.md"
	CodeBasePromptFile              = "code_base_prompt.md"
	CodeExtraPromptFile             = "code_extra_prompt.md"
	ReviewPromptFile                = "review_prompt.md"
	ReviewExtraPromptFile           = "review_extra_prompt.md"
	ReportCommissioningPromptFile   = "report_commissioning_prompt.md"
	CodeManagementPromptFile        = "code_management_prompt.md"
	CodeManagementExtraPromptFile   = "code_management_extra_prompt.md"
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
// When skipPreviousReport is false, the agent's existing full_report.md is
// included as a "Previous Report" section so the agent can build on prior findings.
func AssembleAgentPrompt(orgDir, projectDir, agentID, sourceDir, extraPrompt string, pinfo ProjectInfoParams, skipPreviousReport bool) (string, error) {
	return assembleAgentAction(orgDir, projectDir, agentID, sourceDir, extraPrompt, pinfo,
		ReportBasePromptFile, ReportPromptFile, ReportExtraPromptFile, skipPreviousReport)
}

// AssembleAgentCodePrompt builds the full prompt for an agent code run.
// When skipPreviousReport is false, the agent's existing full_report.md is
// included so the agent has context about prior findings.
func AssembleAgentCodePrompt(orgDir, projectDir, agentID, sourceDir, extraPrompt string, pinfo ProjectInfoParams, skipPreviousReport bool) (string, error) {
	return assembleAgentAction(orgDir, projectDir, agentID, sourceDir, extraPrompt, pinfo,
		CodeBasePromptFile, CodePromptFile, CodeExtraPromptFile, skipPreviousReport)
}

// Prompt sequence: ATeam Project Context → Base prompt → Role-specific prompt → Extra prompts → Previous report → CLI extra
func assembleAgentAction(orgDir, projectDir, agentID, sourceDir, extraPrompt string, pinfo ProjectInfoParams, baseFile, roleFile, extraFile string, skipPreviousReport bool) (string, error) {
	rolePrompt := readFileOr3Level(
		filepath.Join(projectDir, "agents", agentID, roleFile),
		filepath.Join(orgDir, "agents", agentID, roleFile),
		filepath.Join(orgDir, "defaults", "agents", agentID, roleFile),
	)

	basePrompt := readFileOr3Level(
		filepath.Join(projectDir, baseFile),
		filepath.Join(orgDir, baseFile),
		filepath.Join(orgDir, "defaults", baseFile),
	)

	if rolePrompt == "" && basePrompt == "" {
		return "", fmt.Errorf("no prompt found for agent %s action %s", agentID, strings.TrimSuffix(roleFile, ".md"))
	}

	var parts []string
	if info := FormatProjectInfo(pinfo); info != "" {
		parts = append(parts, info)
	}
	if basePrompt != "" {
		parts = append(parts, strings.ReplaceAll(basePrompt, "{{SOURCE_DIR}}", sourceDir))
	}
	if rolePrompt != "" {
		parts = append(parts, strings.ReplaceAll(rolePrompt, "{{SOURCE_DIR}}", sourceDir))
	}

	extras := collectAgentExtras(orgDir, projectDir, agentID, extraFile)
	parts = append(parts, extras...)

	if !skipPreviousReport {
		if content, modTime, err := readFileWithModTime(filepath.Join(projectDir, "agents", agentID, FullReportFile)); err == nil && content != "" {
			age := time.Since(modTime)
			header := fmt.Sprintf("# Previous Report\n\nWhat follows is the previous report that was generated (and possibly updated with the tasks completed) on %s (%s ago). It might be outdated but it will give you some context of what has been done.\n\n",
				modTime.Format("2006-01-02 15:04:05 MST"), formatAge(age))
			parts = append(parts, header+content)
		}
	}

	if extraPrompt != "" {
		parts = append(parts, "# Additional Instructions\n\n"+extraPrompt)
	}

	return strings.Join(parts, "\n\n---\n\n"), nil
}

// collectAgentExtras gathers extra prompt files from all levels (no defaults).
// Order: org broad → org agent-specific → project broad → project agent-specific.
func collectAgentExtras(orgDir, projectDir, agentID, extraFile string) []string {
	paths := []string{
		filepath.Join(orgDir, extraFile),
		filepath.Join(orgDir, "agents", agentID, extraFile),
		filepath.Join(projectDir, extraFile),
		filepath.Join(projectDir, "agents", agentID, extraFile),
	}
	return readAllExisting(paths)
}

// collectSupervisorExtras gathers extra prompt files from org and project levels.
func collectSupervisorExtras(orgDir, projectDir, extraFile string) []string {
	paths := []string{
		filepath.Join(orgDir, "supervisor", extraFile),
		filepath.Join(projectDir, "supervisor", extraFile),
	}
	return readAllExisting(paths)
}

func readAllExisting(paths []string) []string {
	var results []string
	for _, p := range paths {
		if data, err := os.ReadFile(p); err == nil {
			content := strings.TrimSpace(string(data))
			if content != "" {
				results = append(results, content)
			}
		}
	}
	return results
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
	parts = append(parts, collectSupervisorExtras(orgDir, projectDir, ReviewExtraPromptFile)...)
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
	parts = append(parts, collectSupervisorExtras(orgDir, projectDir, CodeManagementExtraPromptFile)...)

	parts = append(parts, "# Review\n\n"+reviewContent)

	return strings.Join(parts, "\n\n---\n\n"), nil
}

// ProjectInfoParams holds the values needed to build the project info section.
type ProjectInfoParams struct {
	OrgDir      string // absolute path to .ateamorg/
	ProjectDir  string // absolute path to .ateam/
	ProjectName string
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
	if s := readFileOr3Level(projectPath, orgPath, defaultPath); s != "" {
		return s, nil
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

func readFileWithModTime(path string) (string, time.Time, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", time.Time{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", time.Time{}, err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return "", time.Time{}, err
	}
	return strings.TrimSpace(string(data)), info.ModTime(), nil
}

func formatAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", h)
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day"
		}
		return fmt.Sprintf("%d days", days)
	}
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
