// Package promptdata holds the data accessors and embedded-defaults
// machinery that internal/root needs, without pulling in the
// Prompt-resolution machinery in internal/prompts.
//
// # Why this exists
//
// Package layering is one-way: cmd → flow → prompts → root →
// promptdata. internal/prompts imports internal/root for
// *root.ResolvedEnv (so ResolveContext.Env() can return it). For
// that arrow to be one-way, internal/root must NOT import
// internal/prompts. promptdata holds everything root previously
// reached into prompts for — pure data and formatting helpers with
// no dependency on the resolver.
//
// # What lives here
//
//   - Role discovery / metadata (AllRoleIDs, RoleMeta, IsValidRole,
//     ResolveRoleList, AllKnownRoleIDs, RoleFlagUsage)
//   - Frontmatter parsing (ParsePromptFrontmatter)
//   - Embedded-defaults installation (WriteOrgDefaults, DiffOrgDefaults)
//   - Project-info formatting (ProjectInfoParams, FormatProjectInfo)
//   - Auto-roles marker (AutoRolesMarker)
//
// # What stays in internal/prompts
//
// Prompt / PromptFile / PromptText / RawTextPrompt / ResolveContext /
// PromptDynamic / NewDispatcher / ProjectInfoDynamic —
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
// or has a role prompt (prompts/report/<id>.prompt.md) under one of the
// layer dirs (project and config layers).
func IsValidRole(id string, configRoles map[string]string, layerDirs []string) bool {
	for _, known := range AllRoleIDs {
		if known == id {
			return true
		}
	}
	if _, ok := configRoles[id]; ok {
		return true
	}
	for _, dir := range layerDirs {
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
func ResolveRoleList(ids []string, configRoles map[string]string, layerDirs []string) ([]string, error) {
	allKnown := AllKnownRoleIDs(configRoles, layerDirs)
	var result []string
	for _, id := range ids {
		if id == "all" {
			return enabledRoleIDs(configRoles, allKnown), nil
		}
		if !IsValidRole(id, configRoles, layerDirs) {
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

// AllKnownRoleIDs returns the sorted union of embedded defaults,
// config-defined, and layer-defined role IDs (prompts/report/<id>.prompt.md
// under any of layerDirs: the project and every config layer).
func AllKnownRoleIDs(configRoles map[string]string, layerDirs []string) []string {
	seen := make(map[string]bool, len(AllRoleIDs)+len(configRoles))
	for _, id := range AllRoleIDs {
		seen[id] = true
	}
	for id := range configRoles {
		seen[id] = true
	}
	for _, dir := range layerDirs {
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
