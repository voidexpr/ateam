package prompts

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PromptDiff describes a prompt file that differs from the embedded default.
type PromptDiff struct {
	RelPath string // e.g. "defaults/roles/security/report_prompt.md"
	Status  string // "changed", "missing"
}

//go:embed defaults/roles/*/report_prompt.md defaults/roles/*/code_prompt.md defaults/report_base_prompt.md defaults/code_base_prompt.md defaults/supervisor/review_prompt.md defaults/supervisor/report_commissioning_prompt.md defaults/supervisor/code_management_prompt.md defaults/ateam_claude_sandbox_extra_settings.json
var defaultsFS embed.FS

// AllRoleIDs is the sorted list of role IDs discovered from embedded prompt files.
var AllRoleIDs = discoverRoleIDs()

func discoverRoleIDs() []string {
	entries, err := fs.ReadDir(defaultsFS, "defaults/roles")
	if err != nil {
		return nil
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	sort.Strings(ids)
	return ids
}

// IsValidRole returns true if id is a built-in role or exists in configRoles.
func IsValidRole(id string, configRoles map[string]string) bool {
	for _, known := range AllRoleIDs {
		if known == id {
			return true
		}
	}
	if _, ok := configRoles[id]; ok {
		return true
	}
	return false
}

// ResolveRoleList expands "all" and validates role IDs.
// configRoles provides additional valid role IDs from the project config.
// When "all" is used and configRoles is non-nil, only enabled roles are returned.
func ResolveRoleList(ids []string, configRoles map[string]string) ([]string, error) {
	allKnown := AllKnownRoleIDs(configRoles)
	var result []string
	for _, id := range ids {
		if id == "all" {
			return enabledRoleIDs(configRoles, allKnown), nil
		}
		if !IsValidRole(id, configRoles) {
			return nil, fmt.Errorf("unknown role: %s\nValid roles: %s", id, strings.Join(allKnown, ", "))
		}
		result = append(result, id)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("no roles specified")
	}
	return result, nil
}

// enabledRoleIDs filters allKnown to only roles that are enabled in configRoles.
// Uses allowlist logic (same as config.IsRoleEnabled): only "on" or "enabled" statuses
// are considered enabled. Roles not present in configRoles default to enabled so that
// custom roles and embedded roles without an explicit config entry are included.
// When configRoles is nil, all are returned.
func enabledRoleIDs(configRoles map[string]string, allKnown []string) []string {
	if configRoles == nil {
		return allKnown
	}
	var enabled []string
	for _, id := range allKnown {
		status, inConfig := configRoles[id]
		if !inConfig || status == "on" || status == "enabled" {
			enabled = append(enabled, id)
		}
	}
	return enabled
}

// AllKnownRoleIDs returns the sorted union of embedded and config-defined role IDs.
func AllKnownRoleIDs(configRoles map[string]string) []string {
	seen := make(map[string]bool, len(AllRoleIDs)+len(configRoles))
	for _, id := range AllRoleIDs {
		seen[id] = true
	}
	for id := range configRoles {
		seen[id] = true
	}
	all := make([]string, 0, len(seen))
	for id := range seen {
		all = append(all, id)
	}
	sort.Strings(all)
	return all
}

// RoleFlagUsage returns a help string listing built-in role IDs for use in flag descriptions.
func RoleFlagUsage() string {
	return "comma-separated role list, or 'all'. Built-in: " + strings.Join(AllRoleIDs, ", ")
}

func readEmbedded(name string) string {
	data, err := defaultsFS.ReadFile(name)
	if err != nil {
		panic(fmt.Sprintf("missing embedded prompt file: %s", name))
	}
	return string(data)
}

func DefaultRolePrompt(roleID string) string {
	return readEmbedded(fmt.Sprintf("defaults/roles/%s/report_prompt.md", roleID))
}

func DefaultReportBasePrompt() string {
	return readEmbedded("defaults/report_base_prompt.md")
}

func DefaultCodeBasePrompt() string {
	return readEmbedded("defaults/code_base_prompt.md")
}

func DefaultSupervisorReviewPrompt() string {
	return readEmbedded("defaults/supervisor/review_prompt.md")
}

func DefaultSupervisorCommissioningPrompt() string {
	return readEmbedded("defaults/supervisor/report_commissioning_prompt.md")
}

func DefaultSupervisorCodeManagementPrompt() string {
	return readEmbedded("defaults/supervisor/code_management_prompt.md")
}

func DefaultSandboxSettings() string {
	return readEmbedded("defaults/" + SandboxSettingsFile)
}

type embeddedFile struct {
	rel     string
	content string
}

// embeddedFiles returns all default files as relPath -> content pairs.
func embeddedFiles() []embeddedFile {
	var files []embeddedFile
	for _, id := range AllRoleIDs {
		files = append(files, embeddedFile{
			filepath.Join("defaults", "roles", id, ReportPromptFile),
			DefaultRolePrompt(id),
		})
		// Include per-role code_prompt.md if present in the embedded FS.
		codePath := fmt.Sprintf("defaults/roles/%s/%s", id, CodePromptFile)
		if data, err := defaultsFS.ReadFile(codePath); err == nil {
			files = append(files, embeddedFile{
				filepath.Join("defaults", "roles", id, CodePromptFile),
				string(data),
			})
		}
	}
	files = append(files, embeddedFile{
		filepath.Join("defaults", ReportBasePromptFile),
		DefaultReportBasePrompt(),
	})
	files = append(files, embeddedFile{
		filepath.Join("defaults", CodeBasePromptFile),
		DefaultCodeBasePrompt(),
	})
	files = append(files, embeddedFile{
		filepath.Join("defaults", "supervisor", ReviewPromptFile),
		DefaultSupervisorReviewPrompt(),
	})
	files = append(files, embeddedFile{
		filepath.Join("defaults", "supervisor", ReportCommissioningPromptFile),
		DefaultSupervisorCommissioningPrompt(),
	})
	files = append(files, embeddedFile{
		filepath.Join("defaults", "supervisor", CodeManagementPromptFile),
		DefaultSupervisorCodeManagementPrompt(),
	})
	files = append(files, embeddedFile{
		filepath.Join("defaults", SandboxSettingsFile),
		DefaultSandboxSettings(),
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
	return nil
}
