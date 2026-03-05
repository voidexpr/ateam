package prompts

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ateam-poc/internal/agents"
)

// PromptDiff describes a prompt file that differs from the embedded default.
type PromptDiff struct {
	RelPath string // e.g. "defaults/agents/security/report_prompt.md"
	Status  string // "changed", "missing"
}

//go:embed defaults/agents/*/report_prompt.md defaults/report_instructions.md defaults/supervisor/review_prompt.md
var defaultsFS embed.FS

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

func DefaultReportInstructions() string {
	return readEmbedded("defaults/report_instructions.md")
}

func DefaultSupervisorPrompt() string {
	return readEmbedded("defaults/supervisor/review_prompt.md")
}

type embeddedFile struct {
	rel     string
	content string
}

// embeddedFiles returns all default files as relPath -> content pairs.
func embeddedFiles() []embeddedFile {
	var files []embeddedFile
	for _, id := range agents.AllAgentIDs {
		files = append(files, embeddedFile{
			filepath.Join("defaults", "agents", id, ReportPromptFile),
			DefaultAgentPrompt(id),
		})
	}
	files = append(files, embeddedFile{
		filepath.Join("defaults", ReportInstructionsFile),
		DefaultReportInstructions(),
	})
	files = append(files, embeddedFile{
		filepath.Join("defaults", "supervisor", ReviewPromptFile),
		DefaultSupervisorPrompt(),
	})
	return files
}

// DiffRootDefaults compares on-disk prompt files against embedded defaults
// and returns a list of files that differ.
func DiffRootDefaults(ateamRoot string) []PromptDiff {
	var diffs []PromptDiff
	for _, f := range embeddedFiles() {
		diskPath := filepath.Join(ateamRoot, f.rel)
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

// WriteRootDefaults writes default prompt files to .ateam/defaults/.
// If overwrite is true, existing files are replaced; otherwise they are skipped.
func WriteRootDefaults(ateamRoot string, overwrite bool) error {
	write := WriteIfNotExists
	if overwrite {
		write = func(path, content string) error {
			return os.WriteFile(path, []byte(content), 0644)
		}
	}

	for _, f := range embeddedFiles() {
		diskPath := filepath.Join(ateamRoot, f.rel)
		if err := os.MkdirAll(filepath.Dir(diskPath), 0755); err != nil {
			return fmt.Errorf("cannot create directory for %s: %w", f.rel, err)
		}
		if err := write(diskPath, f.content); err != nil {
			return err
		}
	}
	return nil
}
