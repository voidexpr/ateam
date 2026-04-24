package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PromptSource describes a single input that contributes to a prompt.
type PromptSource struct {
	Path    string    // absolute file path, or "" for CLI/generated content
	Label   string    // display label for non-file sources (e.g. "CLI: --extra-prompt")
	ModTime time.Time // zero for non-file sources
	Content string    // raw content for token estimation
}

// DisplayPath returns a shortened path for display.
// Paths under .ateamorg/ or .ateam/ are made relative starting at that directory.
// Non-file sources return their Label.
func (s PromptSource) DisplayPath() string {
	if s.Path == "" {
		return s.Label
	}
	if i := strings.Index(s.Path, "/.ateamorg/"); i >= 0 {
		return s.Path[i+1:]
	}
	if i := strings.Index(s.Path, "/.ateam/"); i >= 0 {
		return s.Path[i+1:]
	}
	return s.Path
}

// EstimateTokens approximates token count using ~4 characters per token.
func EstimateTokens(s string) int {
	if len(s) == 0 {
		return 0
	}
	return (len(s) + 3) / 4
}

// TraceRolePromptSources returns the sources that contribute to a role report prompt.
func TraceRolePromptSources(orgDir, projectDir, roleID, sourceDir, extraPrompt string, pinfo ProjectInfoParams, skipPreviousReport bool) []PromptSource {
	return traceRoleAction(orgDir, projectDir, roleID, sourceDir, extraPrompt, pinfo,
		ReportBasePromptFile, ReportPromptFile, ReportExtraPromptFile, skipPreviousReport)
}

// TraceRoleCodePromptSources returns the sources that contribute to a role code prompt.
func TraceRoleCodePromptSources(orgDir, projectDir, roleID, sourceDir, extraPrompt string, pinfo ProjectInfoParams) []PromptSource {
	return traceRoleAction(orgDir, projectDir, roleID, sourceDir, extraPrompt, pinfo,
		CodeBasePromptFile, CodePromptFile, CodeExtraPromptFile, true)
}

func traceRoleAction(orgDir, projectDir, roleID, sourceDir, extraPrompt string, pinfo ProjectInfoParams, baseFile, roleFile, extraFile string, skipPreviousReport bool) []PromptSource {
	var sources []PromptSource

	if info := FormatProjectInfo(pinfo); info != "" {
		sources = append(sources, PromptSource{Label: "Built-in: project-info", Content: info})
	}

	if s := traceFileOr3Level(
		filepath.Join(projectDir, "roles", roleID, roleFile),
		filepath.Join(orgDir, "roles", roleID, roleFile),
		filepath.Join(orgDir, "defaults", "roles", roleID, roleFile),
	); s != nil {
		sources = append(sources, *s)
	}

	if s := traceFileOr3Level(
		filepath.Join(projectDir, baseFile),
		filepath.Join(orgDir, baseFile),
		filepath.Join(orgDir, "defaults", baseFile),
	); s != nil {
		sources = append(sources, *s)
	}

	sources = append(sources, traceExistingFiles([]string{
		filepath.Join(orgDir, extraFile),
		filepath.Join(orgDir, "roles", roleID, extraFile),
		filepath.Join(projectDir, extraFile),
		filepath.Join(projectDir, "roles", roleID, extraFile),
	})...)

	if !skipPreviousReport {
		sources = append(sources, traceFile(filepath.Join(projectDir, "roles", roleID, ReportFile))...)
	}

	if extraPrompt != "" {
		sources = append(sources, PromptSource{Label: "CLI: --extra-prompt", Content: extraPrompt})
	}

	return sources
}

// TraceReviewPromptSources returns the sources that contribute to a supervisor review prompt.
func TraceReviewPromptSources(orgDir, projectDir string, pinfo ProjectInfoParams, extraPrompt string) []PromptSource {
	var sources []PromptSource

	if info := FormatProjectInfo(pinfo); info != "" {
		sources = append(sources, PromptSource{Label: "Built-in: project-info", Content: info})
	}

	if s := traceFileOr3Level(
		filepath.Join(projectDir, "supervisor", ReviewPromptFile),
		filepath.Join(orgDir, "supervisor", ReviewPromptFile),
		filepath.Join(orgDir, "defaults", "supervisor", ReviewPromptFile),
	); s != nil {
		sources = append(sources, *s)
	}

	sources = append(sources, traceExistingFiles([]string{
		filepath.Join(orgDir, "supervisor", ReviewExtraPromptFile),
		filepath.Join(projectDir, "supervisor", ReviewExtraPromptFile),
	})...)

	reports, err := DiscoverReports(projectDir)
	if err == nil {
		for _, r := range reports {
			sources = append(sources, PromptSource{Path: r.Path, ModTime: r.ModTime, Content: r.Content})
		}
	}

	if extraPrompt != "" {
		sources = append(sources, PromptSource{Label: "CLI: --extra-prompt", Content: extraPrompt})
	}

	return sources
}

// TraceCodeManagementPromptSources returns the sources that contribute to a supervisor code prompt.
func TraceCodeManagementPromptSources(orgDir, projectDir string, pinfo ProjectInfoParams, reviewPath, extraPrompt string) []PromptSource {
	var sources []PromptSource

	if info := FormatProjectInfo(pinfo); info != "" {
		sources = append(sources, PromptSource{Label: "Built-in: project-info", Content: info})
	}

	if s := traceFileOr3Level(
		filepath.Join(projectDir, "supervisor", CodeManagementPromptFile),
		filepath.Join(orgDir, "supervisor", CodeManagementPromptFile),
		filepath.Join(orgDir, "defaults", "supervisor", CodeManagementPromptFile),
	); s != nil {
		sources = append(sources, *s)
	}

	sources = append(sources, traceExistingFiles([]string{
		filepath.Join(orgDir, "supervisor", CodeManagementExtraPromptFile),
		filepath.Join(projectDir, "supervisor", CodeManagementExtraPromptFile),
	})...)

	sources = append(sources, traceFile(reviewPath)...)

	if extraPrompt != "" {
		sources = append(sources, PromptSource{Label: "CLI: --extra-prompt", Content: extraPrompt})
	}

	return sources
}

// traceFileOr3Level tries paths in order and returns a PromptSource for the first existing non-empty file.
func traceFileOr3Level(paths ...string) *PromptSource {
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		content := strings.TrimSpace(string(data))
		if content == "" {
			continue
		}
		return &PromptSource{Path: p, ModTime: info.ModTime(), Content: content}
	}
	return nil
}

// traceExistingFiles returns PromptSource entries for each path that exists and is non-empty.
func traceExistingFiles(paths []string) []PromptSource {
	var sources []PromptSource
	for _, p := range paths {
		sources = append(sources, traceFile(p)...)
	}
	return sources
}

// traceFile returns a single-element slice with the PromptSource if the file exists and is non-empty.
func traceFile(path string) []PromptSource {
	if path == "" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return nil
	}
	return []PromptSource{{Path: path, ModTime: info.ModTime(), Content: content}}
}
