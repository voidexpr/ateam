// Package prompts hosts the supporting types and helpers around prompt
// assembly. The v1 composition pipeline lives in internal/prompts/assembler
// (anchor-based filename-driven composition); this package keeps the
// runtime-side surfaces that aren't part of that pipeline:
//
//   - Report discovery (DiscoverReports, ReviewSelector, RoleReport)
//   - Project-info formatting (FormatProjectInfo, ProjectInfoParams)
//   - Frontmatter + role metadata (ParsePromptFrontmatter, RoleMeta, AllRoleIDs,
//     IsValidRole, AllKnownRoleIDs) — see embed.go
//   - Token estimation + display helpers (EstimateTokens, PromptSource)
//   - Embedded-defaults installation (DiffOrgDefaults, WriteOrgDefaults)
//   - Report-set selection for review (ReviewSelector, ReviewFunnel,
//     ReviewEmptyError), consumed by the v1 assembly helpers in cmd/*_v1.go.
package prompts

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ateam/internal/display"
	"github.com/ateam/internal/gitutil"
)

const (
	// CodeVerifyPromptFile is the legacy verify-prompt filename the web UI's
	// prompt-source lookup still keys on (internal/web/handlers.go).
	CodeVerifyPromptFile = "code_verify_prompt.md"
	// SandboxSettingsFile is the standalone sandbox-settings JSON shipped
	// alongside the embedded prompts (see DefaultSandboxSettings).
	SandboxSettingsFile = "ateam_claude_sandbox_extra_settings.json"
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

// RoleReport holds metadata about a discovered role report file.
type RoleReport struct {
	RoleID  string
	Path    string
	ModTime time.Time
	Content string
}

// DiscoverReports scans `shared/report/<role>/<role>.md` (the v1 spec path)
// and returns one RoleReport per role. Auto-migration handles the legacy
// `roles/<role>/report.md` and the pre-Step-6 `shared/report/<role>/report.md`
// before this is consulted.
func DiscoverReports(projectDir string) ([]RoleReport, error) {
	sharedReportDir := filepath.Join(projectDir, "shared", "report")
	entries, err := os.ReadDir(sharedReportDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no report files found under %s (run 'ateam report' first)", sharedReportDir)
		}
		return nil, fmt.Errorf("cannot read shared/report directory: %w", err)
	}

	out := make([]RoleReport, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		role := entry.Name()
		reportPath := filepath.Join(sharedReportDir, role, role+".md")
		data, err := os.ReadFile(reportPath)
		if err != nil {
			continue
		}
		info, err := os.Stat(reportPath)
		if err != nil {
			continue
		}
		out = append(out, RoleReport{
			RoleID:  role,
			Path:    reportPath,
			ModTime: info.ModTime(),
			Content: string(data),
		})
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("no report files found under %s (run 'ateam report' first)", sharedReportDir)
	}

	// Sort by RoleID so a given report set always yields the same order:
	// downstream ReviewSelector.Filter and formatReportsBlock preserve this
	// order, so without it the review prompt (and its hash) would vary run to
	// run from Go's randomized map iteration.
	sort.Slice(out, func(i, j int) bool {
		return out[i].RoleID < out[j].RoleID
	})
	return out, nil
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

// ReviewEmptyError is returned by the review assembly path (cmd's
// assembleReviewV1) when ReviewSelector's filters eliminate every report. The
// funnel lets cmd/review.go format a breakdown so the user knows which step
// zeroed things out.
type ReviewEmptyError struct {
	Funnel ReviewFunnel
}

func (e *ReviewEmptyError) Error() string {
	return "no reports left after filters"
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
