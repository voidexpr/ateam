package cmd

import (
	"fmt"
	"strings"

	"github.com/ateam/internal/prompts/assembler"
	"github.com/ateam/internal/root"
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

// assembleAction renders a top-level singleton prompt by action name —
// `ateam prompt --action <action>` resolves <action>.prompt.md across the
// anchor chain (project / org / embedded). Wraps with --pre-prompt and
// --post-prompt the same way as role / supervisor paths.
//
// roleLabel feeds {{dynamic.project_info}}; pass "" to suppress.
func assembleAction(env *root.ResolvedEnv, action, roleLabel, prePrompt, postPrompt string) (string, error) {
	a := env.Assembler()
	engine := env.BuildEngine(roleLabel, action)
	vars := env.BuildAssemblerVars(action, roleLabel, action)
	opts := &assembler.AssembleOptions{PrePrompt: prePrompt}
	res, err := a.Assemble(action, vars, engine, opts)
	if err != nil {
		return "", err
	}
	prompt := res.Prompt
	post, err := renderCLIWrapper(engine, vars, postPrompt)
	if err != nil {
		return "", err
	}
	if post != "" {
		prompt += "\n\n---\n\n" + post
	}
	return prompt, nil
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
	engine := env.BuildEngine(roleLabel, "code")
	vars := env.BuildAssemblerVars("code_management", roleLabel, "code")
	opts := &assembler.AssembleOptions{
		ReplaceRoleMain: customPrompt,
		PrePrompt:       prePrompt,
	}
	res, err := a.Assemble("code_management", vars, engine, opts)
	if err != nil {
		return "", err
	}
	prompt := res.Prompt + "\n\n---\n\n# Review\n\n" + reviewContent
	post, err := renderCLIWrapper(engine, vars, postPrompt)
	if err != nil {
		return "", err
	}
	if post != "" {
		prompt += "\n\n---\n\n" + post
	}
	return prompt, nil
}
