package cmd

import (
	"fmt"
	"strings"

	"github.com/ateam/internal/display"
	"github.com/ateam/internal/prompts"
)

// SPEC INVARIANT (plans/feature_prompt_cmd_bundle_aware.md Next-round
// step 5): the old assembleReview function used to compose the review
// prompt via a parallel path that hand-appended the reports manifest.
// It is gone. Both live `ateam review` and `ateam prompt --action
// review` now go through NewReviewBundle → bundle.Prompt.Resolve, with
// the manifest woven in by dynamic.review_reports inside
// defaults/prompts/review.prompt.md.

// SPEC INVARIANT (Next-round step 6): assembleSupervisor is gone.
// Verify rides NewVerifyBundle; auto-setup + the unknown-action
// fallback ride NewSingleSupervisorBundle. Both compose through the
// same PromptFile pipeline as review / code_management.

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
