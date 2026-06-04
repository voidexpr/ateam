// Package promptdata holds the data accessors and embedded-defaults
// machinery that internal/root needs without pulling in the
// Prompt-resolution machinery in internal/prompts. This package sits
// below both internal/prompts and internal/root in the import graph,
// breaking what would otherwise be a root→prompts→root cycle once
// internal/prompts grows ResolveContext.Env() returning *root.ResolvedEnv.
//
// What lives here:
//   - Role discovery / metadata (AllRoleIDs, RoleMeta, IsValidRole,
//     ResolveRoleList, AllKnownRoleIDs, RoleFlagUsage)
//   - Frontmatter parsing (ParsePromptFrontmatter)
//   - Embedded-defaults installation (WriteOrgDefaults, DiffOrgDefaults)
//   - Project-info formatting (ProjectInfoParams, FormatProjectInfo)
//   - Auto-roles marker (AutoRolesMarker)
//
// What stays in internal/prompts: Prompt / PromptFile / PromptText /
// RawTextPrompt / ResolveContext / PromptDynamic / NewDispatcher etc. —
// the composition pipeline itself.
package promptdata

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ateam/defaults"
	"github.com/ateam/internal/config"
)

// PromptDiff describes a prompt file that differs from the embedded default.
type PromptDiff struct {
	RelPath string // e.g. "defaults/roles/security/report_prompt.md"
	Status  string // "changed", "missing"
}

// AllRoleIDs is the sorted list of role IDs discovered from embedded prompt files.
var AllRoleIDs = discoverRoleIDs()

// RoleMetadata holds the recognized frontmatter fields of a role prompt.
// Legacy roles are predecessors superseded by a dotted-prefix replacement —
// they are hidden from `ateam roles --docs` but still discoverable when named
// explicitly. Deprecated roles still appear in the docs but are flagged so
// users see the deprecation before adopting them.
type RoleMetadata struct {
	Description string
	Deprecated  bool
	Legacy      bool
}

// ParsePromptFrontmatter extracts YAML frontmatter from a markdown prompt.
// Returns the parsed metadata and the body without frontmatter.
func ParsePromptFrontmatter(content string) (meta RoleMetadata, body string) {
	if !strings.HasPrefix(content, "---\n") {
		return RoleMetadata{}, content
	}
	end := strings.Index(content[4:], "\n---\n")
	if end < 0 {
		return RoleMetadata{}, content
	}
	frontmatter := content[4 : 4+end]
	body = strings.TrimLeft(content[4+end+5:], "\n")
	for _, line := range strings.Split(frontmatter, "\n") {
		if v, ok := trimFrontmatterField(line, "description:"); ok {
			meta.Description = v
		} else if v, ok := trimFrontmatterField(line, "deprecated:"); ok {
			meta.Deprecated = v == "true"
		} else if v, ok := trimFrontmatterField(line, "legacy:"); ok {
			meta.Legacy = v == "true"
		}
	}
	return meta, body
}

func trimFrontmatterField(line, prefix string) (string, bool) {
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(line, prefix)), true
}

// RoleMeta returns the parsed frontmatter for a built-in role. Returns the
// zero value when the role isn't embedded or has no frontmatter.
//
// Reads from the v1 path (defaults/prompts/report/<role>.prompt.md);
// the legacy roles/<role>/report_prompt.md fallback was dropped when
// the dual-shipped defaults tree went away.
func RoleMeta(roleID string) RoleMetadata {
	path := fmt.Sprintf("prompts/report/%s.prompt.md", roleID)
	data, err := defaults.FS.ReadFile(path)
	if err != nil {
		return RoleMetadata{}
	}
	meta, _ := ParsePromptFrontmatter(string(data))
	return meta
}

// discoverRoleIDs walks the embedded prompts/report/ tree and returns the
// list of <role> basenames (i.e. the part before .prompt.md). Role IDs may
// contain dots ("code.bugs", "report.security") — those are preserved.
//
// Panics on read failure or empty result. Either means the embed itself is
// broken (a `//go:embed` directive in defaults/embed.go was renamed,
// mistyped, or accidentally narrowed). Failing at binary boot is preferable
// to silently shipping a CLI with zero roles.
func discoverRoleIDs() []string {
	entries, err := fs.ReadDir(defaults.FS, "prompts/report")
	if err != nil {
		panic(fmt.Sprintf("read embedded prompts/report: %v", err))
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".prompt.md") || strings.HasPrefix(name, "_") {
			continue
		}
		ids = append(ids, strings.TrimSuffix(name, ".prompt.md"))
	}
	if len(ids) == 0 {
		panic("embedded prompts/report/ contains no <role>.prompt.md files — defaults/embed.go regression?")
	}
	sort.Strings(ids)
	return ids
}

// IsValidRole returns true if id is a built-in role, exists in configRoles,
// or has a v1 role prompt under projectDir or orgDir. The auto-migrator runs
// at every env Resolve() (see internal/migrate/v1_layout.go), so projects
// with legacy `roles/<id>/report_prompt.md` files get upgraded on first
// contact — by the time validation runs, the v1 paths exist. Validation here
// matches what the assembler can actually consume; users opting out of
// migration via ATEAM_NO_MIGRATE=1 must place files at the v1 paths.
func IsValidRole(id string, configRoles map[string]string, projectDir, orgDir string) bool {
	for _, known := range AllRoleIDs {
		if known == id {
			return true
		}
	}
	if _, ok := configRoles[id]; ok {
		return true
	}
	for _, dir := range []string{projectDir, orgDir} {
		if dir == "" {
			continue
		}
		v1 := filepath.Join(dir, "prompts", "report", id+".prompt.md")
		if _, err := os.Stat(v1); err == nil {
			return true
		}
	}
	return false
}

// ResolveRoleList expands "all" and validates role IDs.
// configRoles provides additional valid role IDs from the project config.
// When "all" is used and configRoles is non-nil, only enabled roles are returned.
func ResolveRoleList(ids []string, configRoles map[string]string, projectDir, orgDir string) ([]string, error) {
	allKnown := AllKnownRoleIDs(configRoles, projectDir, orgDir)
	var result []string
	for _, id := range ids {
		if id == "all" {
			return enabledRoleIDs(configRoles, allKnown), nil
		}
		if !IsValidRole(id, configRoles, projectDir, orgDir) {
			return nil, fmt.Errorf("unknown role: %s\nValid roles: %s", id, strings.Join(allKnown, ", "))
		}
		result = append(result, id)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("no roles specified")
	}
	return result, nil
}

// enabledRoleIDs filters allKnown to roles whose configRoles status is
// considered enabled by config.IsRoleEnabled. Roles are expensive to run, so
// enablement is always opt-in: unlisted roles are excluded from "all"
// expansion. When configRoles is nil, all are returned so callers without a
// loaded config still get the full set.
func enabledRoleIDs(configRoles map[string]string, allKnown []string) []string {
	if configRoles == nil {
		return allKnown
	}
	var enabled []string
	for _, id := range allKnown {
		if config.IsRoleEnabled(configRoles[id]) {
			enabled = append(enabled, id)
		}
	}
	return enabled
}

// AllKnownRoleIDs returns the sorted union of embedded defaults, config-defined,
// .ateamorg/ org-level, and .ateam/ project-level role IDs at the v1 path
// (prompts/report/<id>.prompt.md). Pre-migration `roles/<id>/` directories
// are NOT scanned — the auto-migrator (default-on) upgrades them before this
// runs, and the v1 assembler can't consume legacy paths anyway.
func AllKnownRoleIDs(configRoles map[string]string, projectDir, orgDir string) []string {
	seen := make(map[string]bool, len(AllRoleIDs)+len(configRoles))
	for _, id := range AllRoleIDs {
		seen[id] = true
	}
	for id := range configRoles {
		seen[id] = true
	}
	for _, dir := range []string{projectDir, orgDir} {
		if dir == "" {
			continue
		}
		scanV1Roles(seen, filepath.Join(dir, "prompts", "report"))
	}
	all := make([]string, 0, len(seen))
	for id := range seen {
		all = append(all, id)
	}
	sort.Strings(all)
	return all
}

// scanV1Roles walks reportDir for <id>.prompt.md files and adds each <id> to
// the set. Hidden / dir-level structural files (basename starts with `_`) and
// dotfiles (basename starts with `.`) are skipped — those are framework files
// or OS detritus, not role identifiers.
func scanV1Roles(seen map[string]bool, reportDir string) {
	entries, err := os.ReadDir(reportDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".prompt.md") {
			continue
		}
		if strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".") {
			continue
		}
		seen[strings.TrimSuffix(name, ".prompt.md")] = true
	}
}

// RoleFlagUsage returns a help string listing built-in role IDs for use in flag descriptions.
func RoleFlagUsage() string {
	return "comma-separated role list, or 'all'. Built-in: " + strings.Join(AllRoleIDs, ", ")
}

func readEmbedded(name string) string {
	data, err := defaults.FS.ReadFile(name)
	if err != nil {
		panic(fmt.Sprintf("missing embedded prompt file: %s", name))
	}
	return string(data)
}

// DefaultSandboxSettings returns the embedded sandbox-settings JSON. Only
// per-file accessor kept after the legacy dual-ship was dropped — sandbox
// settings live at the defaults/ root, not under defaults/prompts/.
func DefaultSandboxSettings() string {
	return readEmbedded(SandboxSettingsFile)
}

type embeddedFile struct {
	rel     string
	content string
}

// embeddedFiles enumerates the default files that DiffOrgDefaults /
// WriteOrgDefaults sync into the user's .ateamorg/defaults/ tree. Walks the
// v1 prompts/ subtree once (so adding a default to defaults/prompts/ shows
// up here automatically, no manual list to maintain) plus the standalone
// sandbox-settings JSON.
//
// The returned paths are relative-to-orgDir: `defaults/prompts/...` and
// `defaults/ateam_claude_sandbox_extra_settings.json`. Callers prefix
// orgDir to get the on-disk destination.
func embeddedFiles() []embeddedFile {
	var files []embeddedFile
	err := fs.WalkDir(defaults.FS, "prompts", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		data, err := defaults.FS.ReadFile(p)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", p, err)
		}
		files = append(files, embeddedFile{
			rel:     filepath.Join("defaults", filepath.FromSlash(p)),
			content: string(data),
		})
		return nil
	})
	if err != nil {
		// Walking defaults.FS only fails when the embed itself is broken
		// (e.g. a `//go:embed` directive was mistyped or the subtree was
		// renamed). That's a build-time programmer error; surface it
		// loudly at first call rather than silently shipping an empty
		// org tree.
		panic(fmt.Sprintf("embed walk failed: %v", err))
	}
	if len(files) == 0 {
		panic("embed contains no prompts/*.md files — defaults/embed.go regression?")
	}
	files = append(files, embeddedFile{
		rel:     filepath.Join("defaults", SandboxSettingsFile),
		content: DefaultSandboxSettings(),
	})
	return files
}

// DiffOrgDefaults compares on-disk prompt files against embedded defaults
// and returns a list of files that differ.
func DiffOrgDefaults(orgDir string) []PromptDiff {
	var diffs []PromptDiff
	for _, f := range embeddedFiles() {
		diskPath := filepath.Join(orgDir, f.rel)
		data, err := os.ReadFile(diskPath)
		if err != nil {
			diffs = append(diffs, PromptDiff{RelPath: f.rel, Status: "missing"})
			continue
		}
		if strings.TrimSpace(string(data)) != strings.TrimSpace(f.content) {
			diffs = append(diffs, PromptDiff{RelPath: f.rel, Status: "changed"})
		}
	}
	return diffs
}

// WriteOrgDefaults writes default prompt files to the org directory's defaults/.
// If overwrite is true, existing files are replaced; otherwise they are skipped.
//
// Also strips stale legacy defaults from upgraded orgs — `defaults/roles/`,
// `defaults/supervisor/`, and the root-level `*_base_prompt.md` files were
// removed from the embed in commit 97b55e0 but still sit on disk from
// prior installs. Leaving them there confuses the legacy trace helpers
// (internal/prompts/trace.go) that the web UI still consults; cleanup
// keeps the org tree honest.
func WriteOrgDefaults(orgDir string, overwrite bool) error {
	write := WriteIfNotExists
	if overwrite {
		write = func(path, content string) error {
			return os.WriteFile(path, []byte(content), 0644)
		}
	}

	for _, f := range embeddedFiles() {
		diskPath := filepath.Join(orgDir, f.rel)
		if err := os.MkdirAll(filepath.Dir(diskPath), 0755); err != nil {
			return fmt.Errorf("cannot create directory for %s: %w", f.rel, err)
		}
		if err := write(diskPath, f.content); err != nil {
			return err
		}
	}
	cleanLegacyOrgDefaults(orgDir)
	return nil
}

// cleanLegacyOrgDefaults removes the pre-v1 defaults tree from an upgraded
// org. Idempotent (RemoveAll is a no-op on missing paths and handles both
// files and dirs). Emits a one-line stderr notice per removal so the user
// has a record of what was cleaned. Failures are logged but not fatal —
// the org sync itself already succeeded.
func cleanLegacyOrgDefaults(orgDir string) {
	defaultsDir := filepath.Join(orgDir, "defaults")
	stale := []string{"roles", "supervisor", "report_base_prompt.md", "code_base_prompt.md"}
	for _, rel := range stale {
		path := filepath.Join(defaultsDir, rel)
		if _, err := os.Stat(path); err != nil {
			continue // absent — nothing to clean.
		}
		if err := os.RemoveAll(path); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not remove stale %s: %v\n", path, err)
			continue
		}
		fmt.Fprintf(os.Stderr, "removed stale legacy defaults: %s\n", path)
	}
}
