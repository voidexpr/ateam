package cmd

import (
	"fmt"
	"strings"

	"github.com/ateam/internal/prompts/assembler"
	"github.com/ateam/internal/root"
)

// renderCLIWrapper renders a CLI-supplied wrapper string (the held-back
// --post-prompt) through the same template engine the assembler uses for
// anchor content, so `{{project.name}}` etc. resolve exactly as they do when
// the assembler owns the wrap. Returns "" for whitespace-only input (matching
// the assembler's empty-section drop). Callers that append --post-prompt
// manually (because it must land after report/review blocks) use this so the
// held wrapper isn't emitted as an unresolved raw string.
func renderCLIWrapper(a *assembler.Assembler, vars assembler.Vars, text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", nil
	}
	rendered, err := assembler.NewEngine(a, 0).Render(text, vars)
	if err != nil {
		return "", fmt.Errorf("rendering --post-prompt: %w", err)
	}
	if strings.TrimSpace(rendered) == "" {
		return "", nil
	}
	return rendered, nil
}

// assembleCodeManagementV1 builds the supervisor's code-management prompt:
// the assembler's `code_management` composition (with optional CLI
// overrides), then Review, then --post-prompt.
// Shared by cmd/code.go's runCode and cmd/prompt.go's supervisor preview.
//
// Sub-run flag propagation (batch, profile, model, ...) used to live in a
// trailing "# Sub-Run Flags" block this function appended; v1 inlines those
// values via {{exec.batch}} / {{exec.profile}} / {{exec.model}} placeholders
// directly in the prompt body, so each `ateam exec` example renders with the
// values already baked in. The runner fills the placeholders at exec time;
// `ateam prompt --batch X` bakes them in for previews.
//
// roleLabel feeds {{project.info}}; pass "" to suppress. customPrompt
// (--prompt) replaces the supervisor body via ReplaceRoleMain; framing
// fragments still compose. prePrompt rides through the assembler;
// postPrompt is held until after the review block so it stays the outermost
// tail wrapper.
func assembleCodeManagementV1(env *root.ResolvedEnv, roleLabel, reviewContent, customPrompt, prePrompt, postPrompt string) (string, error) {
	a := env.Assembler()
	vars := env.BuildAssemblerVars("code_management", roleLabel, "code")
	opts := &assembler.AssembleOptions{
		ReplaceRoleMain: customPrompt,
		PrePrompt:       prePrompt,
	}
	res, err := a.Assemble("code_management", vars, nil, opts)
	if err != nil {
		return "", err
	}
	prompt := res.Prompt + "\n\n---\n\n# Review\n\n" + reviewContent
	post, err := renderCLIWrapper(a, vars, postPrompt)
	if err != nil {
		return "", err
	}
	if post != "" {
		prompt += "\n\n---\n\n" + post
	}
	return prompt, nil
}
