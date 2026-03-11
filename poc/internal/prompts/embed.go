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
	RelPath string // e.g. "defaults/agents/security/report_prompt.md"
	Status  string // "changed", "missing"
}

//go:embed defaults/agents/*/report_prompt.md defaults/agents/*/code_prompt.md defaults/report_base_prompt.md defaults/code_base_prompt.md defaults/supervisor/review_prompt.md defaults/supervisor/report_commissioning_prompt.md defaults/supervisor/code_management_prompt.md defaults/ateam_claude_sandbox_extra_settings.json
var defaultsFS embed.FS

// AllAgentIDs is the sorted list of agent IDs discovered from embedded prompt files.
var AllAgentIDs = discoverAgentIDs()

// DefaultDisabledAgents are agents disabled by default for new projects.
var DefaultDisabledAgents = map[string]bool{
	"automation":              true,
	"basic_project_structure": true,
	"critic_engineering":      true,
	"critic_project":          true,
	"database_config":         true,
	"refactor_architecture":   true,
	"shortcut_taker":          true,
	"testing_full":            true,
}

// DefaultEnabledAgents returns the subset of AllAgentIDs not in DefaultDisabledAgents.
func DefaultEnabledAgents() []string {
	var enabled []string
	for _, id := range AllAgentIDs {
		if !DefaultDisabledAgents[id] {
			enabled = append(enabled, id)
		}
	}
	return enabled
}

func discoverAgentIDs() []string {
	entries, err := fs.ReadDir(defaultsFS, "defaults/agents")
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

// IsValidAgent returns true if id is a built-in agent or exists in configAgents.
func IsValidAgent(id string, configAgents map[string]string) bool {
	for _, known := range AllAgentIDs {
		if known == id {
			return true
		}
	}
	if _, ok := configAgents[id]; ok {
		return true
	}
	return false
}

// ResolveAgentList expands "all" and validates agent IDs.
// configAgents provides additional valid agent IDs from the project config.
// When "all" is used and configAgents is non-nil, only enabled agents are returned.
func ResolveAgentList(ids []string, configAgents map[string]string) ([]string, error) {
	allKnown := AllKnownAgentIDs(configAgents)
	var result []string
	for _, id := range ids {
		if id == "all" {
			return enabledAgentIDs(configAgents, allKnown), nil
		}
		if !IsValidAgent(id, configAgents) {
			return nil, fmt.Errorf("unknown agent: %s\nValid agents: %s", id, strings.Join(allKnown, ", "))
		}
		result = append(result, id)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("no agents specified")
	}
	return result, nil
}

// enabledAgentIDs filters allKnown to only agents that are enabled (or not
// explicitly disabled) in configAgents. When configAgents is nil, all are returned.
func enabledAgentIDs(configAgents map[string]string, allKnown []string) []string {
	if configAgents == nil {
		return allKnown
	}
	var enabled []string
	for _, id := range allKnown {
		if configAgents[id] != "disabled" {
			enabled = append(enabled, id)
		}
	}
	return enabled
}

// AllKnownAgentIDs returns the sorted union of embedded and config-defined agent IDs.
func AllKnownAgentIDs(configAgents map[string]string) []string {
	seen := make(map[string]bool, len(AllAgentIDs)+len(configAgents))
	for _, id := range AllAgentIDs {
		seen[id] = true
	}
	for id := range configAgents {
		seen[id] = true
	}
	all := make([]string, 0, len(seen))
	for id := range seen {
		all = append(all, id)
	}
	sort.Strings(all)
	return all
}

// AgentFlagUsage returns a help string listing built-in agent IDs for use in flag descriptions.
func AgentFlagUsage() string {
	return "comma-separated agent list, or 'all'. Built-in: " + strings.Join(AllAgentIDs, ", ")
}

func readEmbedded(name string) string {
	data, err := defaultsFS.ReadFile(name)
	if err != nil {
		panic(fmt.Sprintf("missing embedded prompt file: %s", name))
	}
	return string(data)
}

func DefaultAgentPrompt(agentID string) string {
	return readEmbedded(fmt.Sprintf("defaults/agents/%s/report_prompt.md", agentID))
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
	for _, id := range AllAgentIDs {
		files = append(files, embeddedFile{
			filepath.Join("defaults", "agents", id, ReportPromptFile),
			DefaultAgentPrompt(id),
		})
		// Include per-agent code_prompt.md if present in the embedded FS.
		codePath := fmt.Sprintf("defaults/agents/%s/%s", id, CodePromptFile)
		if data, err := defaultsFS.ReadFile(codePath); err == nil {
			files = append(files, embeddedFile{
				filepath.Join("defaults", "agents", id, CodePromptFile),
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
