package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/ateam/internal/display"
	"github.com/ateam/internal/root"
)

// assembleRoleReportV1 builds a single role's report prompt via the v1
// assembler. Mirrors prompts.AssembleRolePrompt but composes through the
// new pipeline (anchor walk + Vars + Assemble) instead of the hardcoded
// 4-level fallback in internal/prompts.
//
// Composition produced for "report/<roleID>":
//
//	root_pre        {{project.info}}
//	dir_pre:report  intro / source location / maturity / merging-old / role-id
//	role_main       <roleID>.prompt.md         (the role body)
//	role_post       user-authored <roleID>.post.*.md fragments
//	dir_post:report format / guidelines / output validation / write-to-OUTPUT_FILE
//	(manual)        # Previous Report (if not skipped, when shared/report/<roleID>/report.md exists)
//	(manual)        # Additional Instructions   (--extra-prompt CLI value)
//
// Previous report inclusion mirrors the legacy "no prior" sentinel so the
// agent's "merge old report" workflow gets the same signal in either branch.
//
// roleLabel feeds the {{project.info}} block (typically "role <roleID>");
// pass "" to suppress the project info section entirely — matches the
// legacy `--no-project-info` flag's behavior.
func assembleRoleReportV1(env *root.ResolvedEnv, roleID, roleLabel, extraPrompt string, skipPreviousReport bool) (string, error) {
	promptPath := "report/" + roleID

	a := env.Assembler()
	vars := env.BuildAssemblerVars(promptPath, roleLabel, "report")
	res, err := a.Assemble(promptPath, vars, nil)
	if err != nil {
		return "", err
	}
	prompt := res.Prompt

	if !skipPreviousReport {
		prompt += "\n\n---\n\n" + previousReportBlock(env, roleID)
	}
	if extraPrompt != "" {
		prompt += "\n\n---\n\n# Additional Instructions\n\n" + extraPrompt
	}
	return prompt, nil
}

// assembleRoleCodeV1 is the role-templated counterpart for "code" actions —
// `ateam prompt --role X --action code` (and any future per-role code
// command). No previous-report block: code prompts never include the
// prior report, since the source of truth for "what changed" is the git
// history of the patch the role will land.
//
// roleLabel feeds {{project.info}}; pass "" to suppress.
func assembleRoleCodeV1(env *root.ResolvedEnv, roleID, roleLabel, extraPrompt string) (string, error) {
	promptPath := "code/" + roleID
	a := env.Assembler()
	vars := env.BuildAssemblerVars(promptPath, roleLabel, "code")
	res, err := a.Assemble(promptPath, vars, nil)
	if err != nil {
		return "", err
	}
	prompt := res.Prompt
	if extraPrompt != "" {
		prompt += "\n\n---\n\n# Additional Instructions\n\n" + extraPrompt
	}
	return prompt, nil
}

// previousReportBlock returns the inline "# Previous Report" section (or
// "no prior report" sentinel) for roleID, formatted the same way the legacy
// path did. Source path is env.RoleReportPath which dual-reads v1
// (shared/report/<role>/report.md) and legacy (roles/<role>/report.md).
func previousReportBlock(env *root.ResolvedEnv, roleID string) string {
	path := env.RoleReportPath(roleID)
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return "# Prior Report Status\n\nNo prior report exists for this role. This is a fresh cycle — disregard any \"merge prior findings\" guidance in the base prompt and produce a complete standalone report. Do not search `.ateam/` for one; it isn't there."
	}
	info, err := os.Stat(path)
	if err != nil {
		return "# Prior Report Status\n\nNo prior report exists for this role. This is a fresh cycle — disregard any \"merge prior findings\" guidance in the base prompt and produce a complete standalone report. Do not search `.ateam/` for one; it isn't there."
	}
	age := time.Since(info.ModTime())
	header := fmt.Sprintf("# Previous Report\n\nWhat follows is the previous report that was generated (and possibly updated with the tasks completed) on %s (%s ago). It might be outdated but it will give you some context of what has been done.\n\n",
		info.ModTime().Format(display.TimestampFormat), formatAgeShort(age))
	return header + string(data)
}

// formatAgeShort condenses a Duration to "Xd" / "Xh" / "Xm" / "just now" —
// mirrors the legacy internal/prompts.formatAge function (which is
// unexported there) so this package doesn't take a new dependency on the
// internal helper.
func formatAgeShort(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", h)
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day"
		}
		return fmt.Sprintf("%d days", days)
	}
}
