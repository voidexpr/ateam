package cmd

import (
	"fmt"
	"strings"

	"github.com/ateam/internal/root"
)

// SubRunFlags carries the inputs `ateam code` injects into the supervisor
// prompt as the "# Sub-Run Flags" block. Each flag the supervisor must pass
// to every `ateam exec` sub-run is rendered as a bullet, matching the
// historical inline format in cmd/code.go.
//
// All fields are optional except Batch and ProjectDir, which the supervisor
// always needs (batch groups sub-execs for cost tracking; project pins the
// .ateam directory across remote-mode invocations). Empty strings on the
// other fields omit their bullet from the rendered block.
type SubRunFlags struct {
	Batch          string
	ProjectDir     string // passed through shellQuoteSingle; pass already-quoted or trust the value
	Agent          string // mutually exclusive with Profile per the live CLI
	Profile        string
	Model          string
	Effort         string
	MaxBudgetUSD   string
	MaxBudgetBatch string
}

// Render produces the "# Sub-Run Flags" markdown block. Returns "" when no
// flags would be emitted (defensive — every caller today sets at least Batch
// + ProjectDir, but the helper is conservative).
func (s SubRunFlags) Render() string {
	var b strings.Builder
	b.WriteString("# Sub-Run Flags\n\nYou MUST pass the following flags to every `ateam exec` command you execute:\n")
	wrote := false
	if s.Batch != "" {
		fmt.Fprintf(&b, "- `--batch %s` (groups all sub-execs for cost tracking)\n", s.Batch)
		wrote = true
	}
	if s.ProjectDir != "" {
		fmt.Fprintf(&b, "- `--project %s`\n", s.ProjectDir)
		wrote = true
	}
	if s.Agent != "" {
		fmt.Fprintf(&b, "- `--agent %s`\n", s.Agent)
		wrote = true
	} else if s.Profile != "" {
		fmt.Fprintf(&b, "- `--profile %s`\n", s.Profile)
		wrote = true
	}
	if s.Model != "" {
		fmt.Fprintf(&b, "- `--model %s`\n", s.Model)
		wrote = true
	}
	if s.Effort != "" {
		fmt.Fprintf(&b, "- `--effort %s`\n", s.Effort)
		wrote = true
	}
	if s.MaxBudgetUSD != "" {
		fmt.Fprintf(&b, "- `--max-budget-usd %s`\n", s.MaxBudgetUSD)
		wrote = true
	}
	if s.MaxBudgetBatch != "" {
		fmt.Fprintf(&b, "- `--max-budget-usd-batch %s`\n", s.MaxBudgetBatch)
		wrote = true
	}
	if !wrote {
		return ""
	}
	return strings.TrimRight(b.String(), "\n")
}

// assembleCodeManagementV1 builds the supervisor's code-management prompt:
// the assembler's `code_management` composition, then Review, then optional
// Sub-Run Flags, then optional --extra-prompt. Shared by cmd/code.go's
// runCode (passes a real SubRunFlags from CodeOptions) and cmd/prompt.go's
// supervisor preview (passes a placeholder SubRunFlags so the preview shows
// the same structure the live run sends).
//
// roleLabel feeds {{project.info}}; pass "" to suppress.
func assembleCodeManagementV1(env *root.ResolvedEnv, roleLabel, reviewContent string, flags SubRunFlags, extraPrompt string) (string, error) {
	a := env.Assembler()
	vars := env.BuildAssemblerVars("code_management", roleLabel, "code")
	res, err := a.Assemble("code_management", vars, nil)
	if err != nil {
		return "", err
	}
	prompt := res.Prompt + "\n\n---\n\n# Review\n\n" + reviewContent
	if extraPrompt != "" {
		prompt += "\n\n---\n\n# Additional Instructions\n\n" + extraPrompt
	}
	// Sub-Run Flags appear AFTER extraPrompt so the last thing the supervisor
	// reads is the bullet list of flags it must pass to `ateam exec` — same
	// ordering the pre-refactor inline assembly used.
	if block := flags.Render(); block != "" {
		prompt += "\n\n" + block
	}
	return prompt, nil
}
