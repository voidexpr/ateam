// Package prompts is ateam's prompt resolver — the "how" half of the
// composition story. Flow owns "when" prompts resolve; this package
// owns the Prompt interface, its three impls, and the resolver
// contract.
//
// # The Prompt interface
//
// Three implementations cover every shape of prompt body:
//
//   - RawTextPrompt — bytes-through, no engine. `ateam exec --raw`
//     wraps the body in this.
//   - PromptText — engine-expanded inline text (vars + dynamics, no
//     anchor walk). Default for `ateam exec` and `ateam parallel`.
//   - PromptFile — anchored composition: walks project → org →
//     embedded and joins pre/main/post slots. The factory just sets
//     {Path, PrePrompt, PostPrompt, CustomBody}; the assembler and
//     vars are sourced from ctx.Env().Assembler() and ctx.Vars().
//
// # Responsibilities
//
//   - The resolver contract: ResolveContext.{Env, Vars, Mode,
//     Dynamics}. Vars() returns a resolver that dispatches by
//     namespace (exec.* from flow's Runtime, everything else from the
//     bundle's BaseVars).
//   - Dynamics: PromptDynamicFunction = func(ctx, args...) (string,
//     error). Mode-aware — branch on ctx.Mode() to return a sentinel
//     in ModePreview and a real value in ModeReal.
//   - Env-shaped bridge helpers: BuildEngine, ProjectInfoDynamic —
//     take *root.ResolvedEnv as a parameter, not as a closure capture.
//
// # Does not know about
//
//   - When or how often a prompt resolves — flow's job.
//   - Agent execution — runner's job.
//   - Data / role metadata (AllRoleIDs, WriteOrgDefaults,
//     FormatProjectInfo, etc.) — those live in internal/promptdata so
//     that internal/root can import them without creating a cycle.
//
// # Boundary interfaces
//
// Consumes *root.ResolvedEnv (project paths, config) and
// assembler.Assembler (anchor walk).
//
// # Two modes
//
//   - ModePreview (verify, `ateam prompt --action X`, dry-run):
//     generated-artifact dynamics return preview sentinels (e.g.
//     `{{AT RUNTIME: review-reports block}}`). exec.* renders as the
//     `{{AT RUNTIME:exec.<key>}}` sentinel.
//   - ModeReal (live flow.Run between Prepare and Execute):
//     everything resolves to real values; an unpopulated load-bearing
//     exec.* field is a wiring bug and surfaces as an error.
//
// # Package layering
//
// cmd → flow → prompts → root → promptdata. The arrow is one-way; the
// cycle that previously blocked ResolveContext.Env() was broken by
// extracting data helpers into internal/promptdata.
//
// # What also lives here (not part of the resolver)
//
//   - Report discovery (DiscoverReports, ReviewSelector, RoleReport)
//   - Token estimation + display helpers (EstimateTokens, PromptSource)
//
// Design rationale: plans/feature_prompt_cmd_bundle_aware.md.
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
