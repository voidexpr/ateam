package prompts

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ateam/defaults"
	"github.com/ateam/internal/display"
	"github.com/ateam/internal/gitutil"
)

// TimestampFormat is an alias for display.TimestampFormat.
const TimestampFormat = display.TimestampFormat

const (
	ReportPromptFile              = "report_prompt.md"
	ReportBasePromptFile          = "report_base_prompt.md"
	ReportExtraPromptFile         = "report_extra_prompt.md"
	CodePromptFile                = "code_prompt.md"
	CodeBasePromptFile            = "code_base_prompt.md"
	CodeExtraPromptFile           = "code_extra_prompt.md"
	ReviewPromptFile              = "review_prompt.md"
	ReviewExtraPromptFile         = "review_extra_prompt.md"
	ReportCommissioningPromptFile = "report_commissioning_prompt.md"
	CodeManagementPromptFile      = "code_management_prompt.md"
	CodeManagementExtraPromptFile = "code_management_extra_prompt.md"
	AutoSetupPromptFile           = "auto_setup_prompt.md"
	TaskDebugPromptFile           = "task_debug_prompt.md"
	ReportFile                    = "report.md"
	ReportErrorFile               = "report_error.md"
	SandboxSettingsFile           = "ateam_claude_sandbox_extra_settings.json"
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

// AssembleRolePrompt builds the full prompt for a role report run.
// When skipPreviousReport is false, the role's existing report.md is
// included as a "Previous Report" section so the role can build on prior findings.
func AssembleRolePrompt(orgDir, projectDir, roleID, sourceDir, extraPrompt string, pinfo ProjectInfoParams, skipPreviousReport bool) (string, error) {
	return assembleRoleAction(orgDir, projectDir, roleID, sourceDir, extraPrompt, pinfo,
		ReportBasePromptFile, ReportPromptFile, ReportExtraPromptFile, skipPreviousReport)
}

// AssembleRoleCodePrompt builds the full prompt for a role code run.
// Code prompts never include the previous report.
func AssembleRoleCodePrompt(orgDir, projectDir, roleID, sourceDir, extraPrompt string, pinfo ProjectInfoParams) (string, error) {
	return assembleRoleAction(orgDir, projectDir, roleID, sourceDir, extraPrompt, pinfo,
		CodeBasePromptFile, CodePromptFile, CodeExtraPromptFile, true)
}

// Prompt sequence: ATeam Project Context → Role-specific prompt → Base prompt (format/output) → Extra prompts → Previous report → CLI extra
func assembleRoleAction(orgDir, projectDir, roleID, sourceDir, extraPrompt string, pinfo ProjectInfoParams, baseFile, roleFile, extraFile string, skipPreviousReport bool) (string, error) {
	rolePrompt := readFileOr3Level(
		filepath.Join(projectDir, "roles", roleID, roleFile),
		filepath.Join(orgDir, "roles", roleID, roleFile),
		filepath.Join(orgDir, "defaults", "roles", roleID, roleFile),
		filepath.Join("roles", roleID, roleFile),
	)

	basePrompt := readFileOr3Level(
		filepath.Join(projectDir, baseFile),
		filepath.Join(orgDir, baseFile),
		filepath.Join(orgDir, "defaults", baseFile),
		baseFile,
	)

	if rolePrompt == "" && basePrompt == "" {
		return "", fmt.Errorf("no prompt found for role %s action %s", roleID, strings.TrimSuffix(roleFile, ".md"))
	}

	var parts []string
	if info := FormatProjectInfo(pinfo); info != "" {
		parts = append(parts, info)
	}
	if rolePrompt != "" {
		_, roleBody := ParsePromptFrontmatter(rolePrompt)
		parts = append(parts, strings.ReplaceAll(roleBody, "{{SOURCE_DIR}}", "."))
	}
	if basePrompt != "" {
		parts = append(parts, strings.ReplaceAll(basePrompt, "{{SOURCE_DIR}}", "."))
	}

	extras := collectRoleExtras(orgDir, projectDir, roleID, extraFile)
	parts = append(parts, extras...)

	if !skipPreviousReport {
		if content, modTime, err := readFileWithModTime(filepath.Join(projectDir, "roles", roleID, ReportFile)); err == nil && content != "" {
			age := time.Since(modTime)
			header := fmt.Sprintf("# Previous Report\n\nWhat follows is the previous report that was generated (and possibly updated with the tasks completed) on %s (%s ago). It might be outdated but it will give you some context of what has been done.\n\n",
				modTime.Format(TimestampFormat), formatAge(age))
			parts = append(parts, header+content)
		}
	}

	if extraPrompt != "" {
		parts = append(parts, "# Additional Instructions\n\n"+extraPrompt)
	}

	return strings.Join(parts, "\n\n---\n\n"), nil
}

// collectRoleExtras gathers extra prompt files from all levels (no defaults).
// Order: org broad → org role-specific → project broad → project role-specific.
func collectRoleExtras(orgDir, projectDir, roleID, extraFile string) []string {
	paths := []string{
		filepath.Join(orgDir, extraFile),
		filepath.Join(orgDir, "roles", roleID, extraFile),
		filepath.Join(projectDir, extraFile),
		filepath.Join(projectDir, "roles", roleID, extraFile),
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

// RoleReport holds metadata about a discovered role report file.
type RoleReport struct {
	RoleID  string
	Path    string
	ModTime time.Time
	Content string
}

// DiscoverReports scans the project's roles directory for report.md files.
func DiscoverReports(projectDir string) ([]RoleReport, error) {
	rolesDir := filepath.Join(projectDir, "roles")
	entries, err := os.ReadDir(rolesDir)
	if err != nil {
		return nil, fmt.Errorf("cannot read roles directory: %w (run 'ateam report' first)", err)
	}

	var reports []RoleReport
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		reportPath := filepath.Join(rolesDir, entry.Name(), ReportFile)
		data, err := os.ReadFile(reportPath)
		if err != nil {
			continue
		}
		info, err := os.Stat(reportPath)
		if err != nil {
			continue
		}
		reports = append(reports, RoleReport{
			RoleID:  entry.Name(),
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
		return "", fmt.Errorf("no report files found in %s/roles — run 'ateam report' first", projectDir)
	}

	var reportContents []string
	var manifestLines []string
	for _, r := range reports {
		reportContents = append(reportContents,
			fmt.Sprintf("# Role Report: %s\n\n%s", r.RoleID, r.Content))
		manifestLines = append(manifestLines,
			fmt.Sprintf("| %s | %s |", r.RoleID, r.ModTime.Format(TimestampFormat)))
	}

	allReports := strings.Join(reportContents, "\n\n---\n\n")

	var manifest string
	if len(manifestLines) > 0 {
		manifest = "# Reports Under Review\n\n| Role | Generated |\n|------|----------|\n" +
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
		parts = append(parts, "# Role Reports\n\n"+allReports)
		return strings.Join(parts, "\n\n---\n\n"), nil
	}

	supervisorPrompt, err := readWith3LevelFallback(
		filepath.Join(projectDir, "supervisor", ReviewPromptFile),
		filepath.Join(orgDir, "supervisor", ReviewPromptFile),
		filepath.Join(orgDir, "defaults", "supervisor", ReviewPromptFile),
		filepath.Join("supervisor", ReviewPromptFile),
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
	parts = append(parts, "# Role Reports\n\n"+allReports)
	if extraPrompt != "" {
		parts = append(parts, "# Additional Instructions\n\n"+extraPrompt)
	}

	return strings.Join(parts, "\n\n---\n\n"), nil
}

// AssembleCodeManagementPrompt builds the full prompt for a supervisor code run.
// reviewContent is the review document to include. customPrompt overrides 3-level fallback if non-empty.
func AssembleCodeManagementPrompt(orgDir, projectDir, sourceDir string, pinfo ProjectInfoParams, reviewContent, customPrompt, extraPrompt string) (string, error) {
	var mgmtPrompt string
	var err error

	if customPrompt != "" {
		mgmtPrompt = customPrompt
	} else {
		mgmtPrompt, err = readWith3LevelFallback(
			filepath.Join(projectDir, "supervisor", CodeManagementPromptFile),
			filepath.Join(orgDir, "supervisor", CodeManagementPromptFile),
			filepath.Join(orgDir, "defaults", "supervisor", CodeManagementPromptFile),
			filepath.Join("supervisor", CodeManagementPromptFile),
			"code management",
		)
		if err != nil {
			return "", err
		}
	}

	mgmtPrompt = strings.ReplaceAll(mgmtPrompt, "{{SOURCE_DIR}}", ".")

	var parts []string
	if info := FormatProjectInfo(pinfo); info != "" {
		parts = append(parts, info)
	}
	parts = append(parts, mgmtPrompt)
	parts = append(parts, collectSupervisorExtras(orgDir, projectDir, CodeManagementExtraPromptFile)...)

	parts = append(parts, "# Review\n\n"+reviewContent)
	if extraPrompt != "" {
		parts = append(parts, "# Additional Instructions\n\n"+extraPrompt)
	}

	return strings.Join(parts, "\n\n---\n\n"), nil
}

// AssembleAutoSetupPrompt builds the prompt for the auto-setup command.
func AssembleAutoSetupPrompt(orgDir, projectDir string, pinfo ProjectInfoParams) (string, error) {
	setupPrompt, err := readWith3LevelFallback(
		filepath.Join(projectDir, "supervisor", AutoSetupPromptFile),
		filepath.Join(orgDir, "supervisor", AutoSetupPromptFile),
		filepath.Join(orgDir, "defaults", "supervisor", AutoSetupPromptFile),
		filepath.Join("supervisor", AutoSetupPromptFile),
		"auto-setup",
	)
	if err != nil {
		return "", err
	}

	var parts []string
	if info := FormatProjectInfo(pinfo); info != "" {
		parts = append(parts, info)
	}
	parts = append(parts, setupPrompt)
	return strings.Join(parts, "\n\n---\n\n"), nil
}

// AssembleTaskDebugPrompt builds the prompt for the ps-files --auto-debug command.
// debugContext contains the run metadata and file paths to investigate.
func AssembleTaskDebugPrompt(orgDir, projectDir, debugContext string, pinfo ProjectInfoParams) (string, error) {
	debugPrompt, err := readWith3LevelFallback(
		filepath.Join(projectDir, "supervisor", TaskDebugPromptFile),
		filepath.Join(orgDir, "supervisor", TaskDebugPromptFile),
		filepath.Join(orgDir, "defaults", "supervisor", TaskDebugPromptFile),
		filepath.Join("supervisor", TaskDebugPromptFile),
		"task-debug",
	)
	if err != nil {
		return "", err
	}

	debugPrompt = strings.ReplaceAll(debugPrompt, "{{TASK_DEBUG_CONTEXT}}", debugContext)

	var parts []string
	if info := FormatProjectInfo(pinfo); info != "" {
		parts = append(parts, info)
	}
	parts = append(parts, debugPrompt)
	return strings.Join(parts, "\n\n---\n\n"), nil
}

// ProjectInfoParams holds the values needed to build the project info section.
type ProjectInfoParams struct {
	OrgDir      string // absolute path to .ateamorg/
	ProjectDir  string // absolute path to .ateam/
	ProjectName string
	SourceDir   string // absolute path to project root
	GitRepoDir  string // absolute path to git repo root (may differ from SourceDir)
	Role        string // e.g. "role security" or "the supervisor"
	Action      string // e.g. "report", "review", "code"
	Meta        *gitutil.ProjectMeta
}

// FormatProjectInfo builds the ateam project context section.
// Returns "" if p has no Role set (zero value).
func FormatProjectInfo(p ProjectInfoParams) string {
	if p.Role == "" {
		return ""
	}
	var b strings.Builder
	// Header line: "project_name role action" — helps identify sessions in --resume lists.
	if p.ProjectName != "" {
		b.WriteString(p.ProjectName)
		b.WriteString(" ")
		b.WriteString(p.Role)
		if p.Action != "" {
			b.WriteString(" ")
			b.WriteString(p.Action)
		}
		b.WriteString("\n\n")
	}
	b.WriteString("# ATeam Project Context\n\n")
	b.WriteString("You are part of the ateam software:\n")
	fmt.Fprintf(&b, "* project name: %s\n", p.ProjectName)
	fmt.Fprintf(&b, "* role: %s\n", p.Role)
	b.WriteString("* project directory: . (working directory)\n")
	b.WriteString("* reports and reviews: .ateam\n")
	if p.GitRepoDir != "" && p.GitRepoDir != p.SourceDir {
		rel := shortRelPath(p.SourceDir, p.GitRepoDir)
		fmt.Fprintf(&b, "\n**IMPORTANT**: Your working directory is the project directory (.), not the git repo root (%s). Limit your findings to the project directory. Do not look at or report on code outside it.\n", rel)
	}
	if p.Meta != nil {
		ts := time.Now().Format(TimestampFormat)
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

// shortRelPath returns target relative to base. Falls back to ~/... shorthand
// for paths under $HOME, or the absolute path if neither works.
func shortRelPath(base, target string) string {
	if rel, err := filepath.Rel(base, target); err == nil && !filepath.IsAbs(rel) {
		return rel
	}
	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, target); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.Join("~", rel)
		}
	}
	return target
}

// readWith3LevelFallback tries projectPath, then orgPath, then defaultPath,
// then embedded defaults. embeddedPath is relative to defaults.FS root.
func readWith3LevelFallback(projectPath, orgPath, defaultPath, embeddedPath, label string) (string, error) {
	if s := readFileOr3Level(projectPath, orgPath, defaultPath, embeddedPath); s != "" {
		return s, nil
	}
	return "", fmt.Errorf("no prompt found for %s (checked %s, %s, %s, and embedded)", label, projectPath, orgPath, defaultPath)
}

// readFileOr3Level tries three filesystem paths via traceFileOr3Level, then
// falls back to embedded defaults. embeddedPath is relative to defaults.FS root.
func readFileOr3Level(projectPath, orgPath, defaultPath, embeddedPath string) string {
	if src := traceFileOr3Level(projectPath, orgPath, defaultPath); src != nil {
		return src.Content
	}
	if embeddedPath != "" {
		if data, err := defaults.FS.ReadFile(embeddedPath); err == nil {
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
