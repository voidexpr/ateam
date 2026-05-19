// Package prompts handles prompt template assembly and rendering for agent execution.
//
// # Fallback hierarchy
//
// Every role and base prompt is resolved through a 4-level hierarchy, tried in order:
//
//  1. Project level  – .ateam/<file> or .ateam/roles/<role>/<file>
//  2. Org level      – .ateamorg/<file> or .ateamorg/roles/<role>/<file>
//  3. Org-defaults   – .ateamorg/defaults/<file> or .ateamorg/defaults/roles/<role>/<file>
//  4. Embedded       – the defaults bundled into the binary via defaults.FS
//
// For role prompts and base prompts the first non-empty file found wins (first-found).
// Extra prompts (*_extra_prompt.md) are additive: all four levels that contain a
// non-empty file contribute, concatenated in order (org-broad → org-role → project-broad
// → project-role).
//
// # Debugging prompt sources
//
// The Trace* functions in trace.go (TraceRolePromptSources, TraceRoleCodePromptSources,
// TraceReviewPromptSources, TraceCodeManagementPromptSources) return the list of
// PromptSource entries that would be assembled for a given call, without actually
// assembling the prompt. Use these to inspect which files contribute and estimate
// token counts.
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

const (
	ReportPromptFile              = "report_prompt.md"
	ReportBasePromptFile          = "report_base_prompt.md"
	ReportExtraPromptFile         = "report_extra_prompt.md"
	CodePromptFile                = "code_prompt.md"
	CodeBasePromptFile            = "code_base_prompt.md"
	CodeExtraPromptFile           = "code_extra_prompt.md"
	ReviewPromptFile              = "review_prompt.md"
	ReviewExtraPromptFile         = "review_extra_prompt.md"
	ReportAutoRolesPromptFile     = "report_auto_roles_prompt.md"
	CodeManagementPromptFile      = "code_management_prompt.md"
	CodeManagementExtraPromptFile = "code_management_extra_prompt.md"
	CodeVerifyPromptFile          = "code_verify_prompt.md"
	CodeVerifyExtraPromptFile     = "code_verify_extra_prompt.md"
	AutoSetupPromptFile           = "auto_setup_prompt.md"
	ExecDebugPromptFile           = "exec_debug_prompt.md"
	ReportFile                    = "report.md"
	ReportErrorFile               = "report_error.md"
	SandboxSettingsFile           = "ateam_claude_sandbox_extra_settings.json"
)

// AutoRolesMarker is the contract line the `--auto-roles` planner agent
// writes at the end of its output file. The substring after the colon is
// comma-separated role IDs (no spaces); an empty value means "no roles need
// running". Substituted into the prompt as `{{AUTO_ROLES_MARKER}}` and parsed
// back by cmd/auto_roles.go::parseAutoRolesOutput. Keeping it as a single
// exported constant prevents prompt-vs-parser drift.
const AutoRolesMarker = "RECOMMENDED_ROLES:"

// ResolveValue resolves a prompt value:
//   - "-" or "@-": read the prompt from stdin (terminated by EOF).
//   - "@<path>":   read the prompt from the named file.
//   - anything else: return the value as-is (literal prompt text).
//
// When reading stdin yields no content (e.g. an empty pipe), an error is
// returned rather than a silent empty prompt.
func ResolveValue(value string) (string, error) {
	if value == "-" || value == "@-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("cannot read prompt from stdin: %w", err)
		}
		if len(strings.TrimSpace(string(data))) == 0 {
			return "", fmt.Errorf("stdin is empty: pipe a prompt or pass one as an argument")
		}
		return string(data), nil
	}
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
				modTime.Format(display.TimestampFormat), formatAge(age))
			parts = append(parts, header+content)
		} else {
			parts = append(parts, "# Prior Report Status\n\nNo prior report exists for this role. This is a fresh cycle — disregard any \"merge prior findings\" guidance in the base prompt and produce a complete standalone report. Do not search `.ateam/` for one; it isn't there.")
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

// ReviewSelector chooses which role reports a supervisor review actually sees.
// All filters are applied in order and produce a ReviewFunnel for diagnostics.
//
//  1. Available           — DiscoverReports' result (every report.md on disk).
//  2. Enabled filter      — drop reports whose role is not RoleEnabled (skipped if IncludeDisabled OR if Roles names them authoritatively).
//  3. Roles intersection  — keep only reports whose role appears in Roles (if non-empty).
//  4. Freshness           — drop reports older than now - MaxAge (skipped if MaxAge == 0).
type ReviewSelector struct {
	Roles           []string      // empty = no role filter
	IncludeDisabled bool          // true = skip the enabled-only step
	MaxAge          time.Duration // zero = no freshness filter
}

// runsEnabledGate reports whether the enabled-only step should run. It's
// skipped when --all (IncludeDisabled) is set, or when --roles names roles
// authoritatively (config.toml's enabled status doesn't gate explicit names;
// the Roles filter still narrows scope to exactly those names).
func (s ReviewSelector) runsEnabledGate() bool {
	return !s.IncludeDisabled && len(s.Roles) == 0
}

// ReviewFunnel records the count of reports surviving each filter step. Used
// to build a clear "no reports left" error when filters eliminate everything.
//
// Enabled is only populated when the enabled-only step actually ran (i.e.
// !IncludeDisabled) — HadEnabled signals that. Whether the roles / max-age
// steps ran is derivable from UsedRoles / MaxAge so they aren't stored.
type ReviewFunnel struct {
	Available   int
	Enabled     int // populated only when HadEnabled
	RolesMatch  int
	FreshEnough int
	HadEnabled  bool          // true when the enabled-only step ran
	MaxAge      time.Duration // zero when no freshness filter applied
	UsedRoles   []string      // copy of selector.Roles; empty when no --roles narrowing
}

// HadRoles reports whether a non-empty --roles list narrowed the set.
func (f ReviewFunnel) HadRoles() bool { return len(f.UsedRoles) > 0 }

// HadMaxAge reports whether a non-zero --max-age window was applied.
func (f ReviewFunnel) HadMaxAge() bool { return f.MaxAge > 0 }

// roleStatusOff is the value `config.RoleDisabled` resolves to. Hardcoded
// here to avoid pulling internal/config into prompts (the package graph keeps
// prompts upstream of config to prevent cycles).
const roleStatusOff = "off"

// Filter returns the reports kept after all selector steps run, plus the
// funnel counts. configRoles is the env's role-status map (typically
// env.Config.Roles); pass nil when there is no project config.
func (s ReviewSelector) Filter(all []RoleReport, configRoles map[string]string) ([]RoleReport, ReviewFunnel) {
	funnel := ReviewFunnel{
		Available:  len(all),
		HadEnabled: s.runsEnabledGate(),
		MaxAge:     s.MaxAge,
		UsedRoles:  append([]string(nil), s.Roles...),
	}

	kept := all
	if s.runsEnabledGate() {
		next := kept[:0:0]
		for _, r := range kept {
			if isRoleEnabled(configRoles, r.RoleID) {
				next = append(next, r)
			}
		}
		kept = next
		funnel.Enabled = len(kept)
	}

	if len(s.Roles) > 0 {
		want := make(map[string]bool, len(s.Roles))
		for _, r := range s.Roles {
			want[r] = true
		}
		next := kept[:0:0]
		for _, r := range kept {
			if want[r.RoleID] {
				next = append(next, r)
			}
		}
		kept = next
	}
	funnel.RolesMatch = len(kept)

	if s.MaxAge > 0 {
		cutoff := time.Now().Add(-s.MaxAge)
		next := kept[:0:0]
		for _, r := range kept {
			if !r.ModTime.Before(cutoff) {
				next = append(next, r)
			}
		}
		kept = next
	}
	funnel.FreshEnough = len(kept)

	return kept, funnel
}

// isRoleEnabled mirrors config.IsRoleEnabled but stays in this package to
// avoid an import cycle (config sits below prompts in the import graph). An
// unknown role defaults to enabled (matches config.IsRoleEnabled's default).
func isRoleEnabled(roles map[string]string, id string) bool {
	if roles == nil {
		return true
	}
	v, ok := roles[id]
	if !ok {
		return true
	}
	return v != roleStatusOff
}

// ReviewEmptyError is returned by AssembleReviewPrompt when ReviewSelector's
// filters eliminate every report. The funnel lets cmd/review.go format a
// breakdown so the user knows which step zeroed things out.
type ReviewEmptyError struct {
	Funnel ReviewFunnel
}

func (e *ReviewEmptyError) Error() string {
	return "no reports left after filters"
}

// AssembleReviewPrompt builds the full prompt for a supervisor review.
// selector chooses which reports the supervisor actually sees; configRoles is
// the env's role-status map (env.Config.Roles, or nil if absent).
func AssembleReviewPrompt(orgDir, projectDir string, pinfo ProjectInfoParams, extraPrompt, customPrompt string, selector ReviewSelector, configRoles map[string]string) (string, error) {
	all, err := DiscoverReports(projectDir)
	if err != nil {
		return "", err
	}
	if len(all) == 0 {
		return "", fmt.Errorf("no report files found in %s/roles — run 'ateam report' first", projectDir)
	}

	reports, funnel := selector.Filter(all, configRoles)
	if len(reports) == 0 {
		return "", &ReviewEmptyError{Funnel: funnel}
	}

	var reportContents []string
	var manifestLines []string
	for _, r := range reports {
		reportContents = append(reportContents,
			fmt.Sprintf("# Role Report: %s\n\n%s", r.RoleID, r.Content))
		manifestLines = append(manifestLines,
			fmt.Sprintf("| %s | %s |", r.RoleID, r.ModTime.Format(display.TimestampFormat)))
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

// AssembleCodeVerifyPrompt builds the supervisor prompt for verifying code
// changes made by the most recent `ateam code` run. The supervisor inspects
// recent commits and runs the test suite; no review document is included
// because the source of truth is the git history itself.
func AssembleCodeVerifyPrompt(orgDir, projectDir string, pinfo ProjectInfoParams, extraPrompt string) (string, error) {
	return assembleSupervisorPrompt(orgDir, projectDir, pinfo, CodeVerifyPromptFile, CodeVerifyExtraPromptFile, "code verify", extraPrompt, nil)
}

// AssembleAutoRolesPrompt builds the supervisor prompt that recommends which
// roles to run this round, based on git history since the last review, prior
// reports, the latest review file, and the last code-cycle execution report.
// Invoked by `ateam report --auto-roles` and `ateam all --auto-roles`.
func AssembleAutoRolesPrompt(orgDir, projectDir string, pinfo ProjectInfoParams) (string, error) {
	return assembleSupervisorPrompt(orgDir, projectDir, pinfo, ReportAutoRolesPromptFile, "", "auto-roles", "", nil)
}

// assembleSupervisorPrompt is the shared backbone for supervisor prompts that
// load a single prompt file via the 3-level fallback, prepend project info,
// append supervisor extras, and optionally append a list of injected sections
// (e.g. role reports for review) plus the CLI --extra-prompt.
func assembleSupervisorPrompt(orgDir, projectDir string, pinfo ProjectInfoParams, promptFile, extraFile, label, extraPrompt string, sections []string) (string, error) {
	body, err := readWith3LevelFallback(
		filepath.Join(projectDir, "supervisor", promptFile),
		filepath.Join(orgDir, "supervisor", promptFile),
		filepath.Join(orgDir, "defaults", "supervisor", promptFile),
		filepath.Join("supervisor", promptFile),
		label,
	)
	if err != nil {
		return "", err
	}

	var parts []string
	if info := FormatProjectInfo(pinfo); info != "" {
		parts = append(parts, info)
	}
	parts = append(parts, body)
	if extraFile != "" {
		parts = append(parts, collectSupervisorExtras(orgDir, projectDir, extraFile)...)
	}
	parts = append(parts, sections...)
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

// AssembleExecDebugPrompt builds the prompt for the ps-files --auto-debug command.
// debugContext contains the agent_exec metadata and file paths to investigate.
func AssembleExecDebugPrompt(orgDir, projectDir, debugContext string, pinfo ProjectInfoParams) (string, error) {
	debugPrompt, err := readWith3LevelFallback(
		filepath.Join(projectDir, "supervisor", ExecDebugPromptFile),
		filepath.Join(orgDir, "supervisor", ExecDebugPromptFile),
		filepath.Join(orgDir, "defaults", "supervisor", ExecDebugPromptFile),
		filepath.Join("supervisor", ExecDebugPromptFile),
		"exec-debug",
	)
	if err != nil {
		return "", err
	}

	debugPrompt = strings.ReplaceAll(debugPrompt, "{{EXEC_DEBUG_CONTEXT}}", debugContext)

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
	WorkDir     string // absolute path of the agent's working directory
	GitRepoDir  string // absolute path to git repo root (may differ from WorkDir)
	Role        string // e.g. "role security" or "the supervisor"
	Action      string // e.g. "report", "review", "code"
	Meta        *gitutil.ProjectMeta

	// QuickOrientation is appended to the project context as an auto-generated
	// orientation block (top-level layout, recent commits, detected manifests,
	// …). Produced by internal/projectinfo.Info.Markdown(). Empty when
	// collection fails. See plans/Feature_TokenReduction.md (Phase 0.5).
	QuickOrientation string
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
	b.WriteString("* working directory: .\n")
	// The .ateam directory may live anywhere relative to the working
	// directory: same dir (typical), parent (subdir of project), or an
	// unrelated path (remote-project mode where --project points elsewhere).
	ateamRel := ".ateam"
	if p.WorkDir != "" && p.ProjectDir != "" {
		ateamRel = shortRelPath(p.WorkDir, p.ProjectDir)
	}
	fmt.Fprintf(&b, "* reports and reviews: %s\n", ateamRel)
	if p.GitRepoDir != "" && p.GitRepoDir != p.WorkDir {
		rel := shortRelPath(p.WorkDir, p.GitRepoDir)
		fmt.Fprintf(&b, "\n**IMPORTANT**: Your working directory (.) is a subdirectory of a wider git repo at %s. Limit your findings to the working directory. Do not look at or report on code outside it.\n", rel)
	}
	if p.Meta != nil {
		ts := time.Now().Format(display.TimestampFormat)
		fmt.Fprintf(&b, "* timestamp: %s\n", ts)
		// When QuickOrientation is enabled it carries the commit + working-tree
		// state (and the uncommitted-file list), so don't duplicate them here.
		if p.QuickOrientation == "" {
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
	}
	if p.QuickOrientation != "" {
		b.WriteString("\n")
		b.WriteString(p.QuickOrientation)
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
