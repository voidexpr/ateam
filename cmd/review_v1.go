package cmd

import (
	"fmt"
	"strings"

	"github.com/ateam/internal/display"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
)

// assembleReviewV1 builds the supervisor review prompt via the v1 assembler
// instead of the legacy prompts.AssembleReviewPrompt path. The composed
// output mirrors the legacy structure so existing user-authored
// review.post.extra.md fragments keep landing in the same position:
//
//	_pre.context.md          {{project.info}}
//	review.prompt.md         supervisor body
//	review.post.*.md         user-authored extras (composed via assembler)
//	<reports block>          manifest + bundled role reports (appended)
//	<extraPrompt>            --extra-prompt CLI value (appended)
//
// Reports are appended manually rather than via a fragment file because the
// assembler's per-slot composition order is anchor-first (embedded →
// project), which would put project-authored extras AFTER an embedded
// reports fragment — the opposite of the legacy ordering. Appending keeps
// extras → reports → CLI, matching legacy bytes.
//
// `--prompt` (customPrompt) is NOT handled here — that branch still uses
// the legacy path; the caller in runReview decides which to use.
//
// Returns the same ReviewEmptyError as the legacy path when the selector's
// filters eliminate every report, so the cmd-level error handler stays
// unchanged.
func assembleReviewV1(env *root.ResolvedEnv, selector prompts.ReviewSelector, extraPrompt string) (string, error) {
	all, err := prompts.DiscoverReports(env.ProjectDir)
	if err != nil {
		return "", err
	}
	if len(all) == 0 {
		return "", fmt.Errorf("no report files found under %s — run 'ateam report' first", env.SharedDir())
	}
	reports, funnel := selector.Filter(all, env.Config.Roles)
	if len(reports) == 0 {
		return "", &prompts.ReviewEmptyError{Funnel: funnel}
	}

	a := env.Assembler()
	vars := env.BuildAssemblerVars("review", "the supervisor", "review")
	res, err := a.Assemble("review", vars, nil)
	if err != nil {
		return "", err
	}

	prompt := res.Prompt
	if block := formatReportsBlock(reports); block != "" {
		prompt += "\n\n---\n\n" + block
	}
	if extraPrompt != "" {
		prompt += "\n\n---\n\n# Additional Instructions\n\n" + extraPrompt
	}
	return prompt, nil
}

// formatReportsBlock renders the manifest table + bundled report contents in
// the same shape AssembleReviewPrompt produced. Lives here so the new
// pipeline can keep that block as a single {{role.reports}} variable; the
// legacy path keeps its own inlined version.
func formatReportsBlock(reports []prompts.RoleReport) string {
	if len(reports) == 0 {
		return ""
	}
	var manifestLines []string
	var contents []string
	for _, r := range reports {
		manifestLines = append(manifestLines,
			fmt.Sprintf("| %s | %s |", r.RoleID, r.ModTime.Format(display.TimestampFormat)))
		contents = append(contents,
			fmt.Sprintf("# Role Report: %s\n\n%s", r.RoleID, r.Content))
	}
	manifest := "# Reports Under Review\n\n| Role | Generated |\n|------|----------|\n" +
		strings.Join(manifestLines, "\n")
	return manifest + "\n\n---\n\n# Role Reports\n\n" + strings.Join(contents, "\n\n---\n\n")
}
