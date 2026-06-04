package cmd

import (
	"fmt"
	"strings"

	"github.com/ateam/internal/prompts/assembler"
)

// renderCLIWrapper renders a CLI-supplied wrapper string (the held-back
// --post-prompt) through the assembler engine — `{{project.name}}` and
// other directives resolve exactly as they do when the assembler owns the
// wrap. Returns "" for whitespace-only input (matching the assembler's
// empty-section drop). Callers that append --post-prompt manually (because
// it must land after report/review blocks) use this so the held wrapper
// isn't emitted as an unresolved raw string.
//
// Pass the same engine the caller used for its main Assemble call so
// dynamics + dispatcher state stay consistent across the body and the
// trailing wrapper.
func renderCLIWrapper(engine *assembler.Engine, vars assembler.Vars, text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", nil
	}
	rendered, err := engine.Render(text, vars)
	if err != nil {
		return "", fmt.Errorf("rendering --post-prompt: %w", err)
	}
	if strings.TrimSpace(rendered) == "" {
		return "", nil
	}
	return rendered, nil
}

// SPEC INVARIANT (Next-round step 6): assembleAction is gone.
// `ateam prompt --action <action>` for unknown actions routes through
// NewSingleSupervisorBundle.

// SPEC INVARIANT (Next-round step 6): assembleCodeManagementV1 is
// gone. The code-management body composes via NewCodeBundle's
// PromptFile with {{dynamic.code_mgmt_review}} weaving the review
// content in.
