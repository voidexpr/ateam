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
)

// CodeVerifyPromptFile is the legacy verify-prompt filename the web
// UI's prompt-source lookup still keys on (internal/web/handlers.go).
const CodeVerifyPromptFile = "code_verify_prompt.md"

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

// DiscoverReports scans `shared/report/<role>.md` (the v1 flat layout) and
// returns one RoleReport per role. Auto-migration handles the legacy
// `roles/<role>/report.md` and the pre-flat `shared/report/<role>/<role>.md`
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
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		role := strings.TrimSuffix(name, ".md")
		reportPath := filepath.Join(sharedReportDir, name)
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

// ProjectInfoParams, FormatProjectInfo, shortRelPath, WriteIfNotExists,
// AutoRolesMarker moved to internal/promptdata in spec step 9 to break
// the root→prompts import cycle. Callers that previously used
// prompts.ProjectInfoParams etc. now use promptdata.ProjectInfoParams.
