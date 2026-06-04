package cmd

import (
	"fmt"
	"strings"

	"github.com/ateam/internal/display"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/prompts/assembler"
	"github.com/ateam/internal/root"
)

// assembleReview builds the supervisor review prompt via the assembler.
// Composition order:
//
//	[--pre-prompt]            (outermost head)
//	_pre.context.md           {{project.info}}
//	review.prompt.md          supervisor body (or customPrompt via ReplaceRoleMain)
//	review.post.*.md          user-authored extras
//	<reports block>           manifest + bundled role reports (appended)
//	[--post-prompt]           (outermost tail)
//
// Reports are appended manually (not via a fragment file) because the
// assembler's per-slot composition is anchor-first (embedded → project),
// which would put project-authored extras AFTER an embedded reports
// fragment — the opposite of the legacy ordering. Appending keeps
// extras → reports, matching legacy bytes.
//
// roleLabel feeds {{project.info}} ("the supervisor" for live runs); pass
// "" to suppress. customPrompt replaces the supervisor body wholesale via
// the assembler's ReplaceRoleMain option. prePrompt / postPrompt wrap the
// assembled output at the outermost positions.
//
// Returns the same ReviewEmptyError as the assembler-only path when the
// selector's filters eliminate every report.
func assembleReview(env *root.ResolvedEnv, selector prompts.ReviewSelector, roleLabel, customPrompt, prePrompt, postPrompt string) (string, error) {
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
	engine := env.BuildEngine(roleLabel, "review")
	vars := env.BuildAssemblerVars("review", roleLabel, "review")
	// Pre-prompt rides through the assembler (lands before _pre.context.md).
	// Post-prompt is held until after the manually-appended reports block so
	// it stays as the outermost tail wrapper.
	opts := &assembler.AssembleOptions{
		ReplaceRoleMain: customPrompt,
		PrePrompt:       prePrompt,
	}
	res, err := a.Assemble("review", vars, engine, opts)
	if err != nil {
		return "", err
	}

	prompt := res.Prompt
	if block := formatReportsBlock(reports); block != "" {
		prompt += "\n\n---\n\n" + block
	}
	post, err := renderCLIWrapper(engine, vars, postPrompt)
	if err != nil {
		return "", err
	}
	if post != "" {
		prompt += "\n\n---\n\n" + post
	}
	return prompt, nil
}

// assembleSupervisor is the generic single-prompt supervisor assembler:
// builds promptPath via env.Assembler() and wraps with pre/post-prompt.
// Used by verify, exec-debug, auto-roles, auto-setup — singletons that
// have no role reports / manifest of their own. Review has its own
// assembleReview because it needs the reports block woven in.
//
// roleLabel and action go into BuildAssemblerVars so {{project.info}}
// renders identically across cmds.
func assembleSupervisor(env *root.ResolvedEnv, promptPath, roleLabel, action, prePrompt, postPrompt string) (string, error) {
	a := env.Assembler()
	engine := env.BuildEngine(roleLabel, action)
	vars := env.BuildAssemblerVars(promptPath, roleLabel, action)
	opts := &assembler.AssembleOptions{
		PrePrompt:  prePrompt,
		PostPrompt: postPrompt,
	}
	res, err := a.Assemble(promptPath, vars, engine, opts)
	if err != nil {
		return "", err
	}
	return res.Prompt, nil
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
