package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ateam/internal/display"
	"github.com/ateam/internal/flow"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
)

// SPEC INVARIANT (Next-round step 6): assembleRoleReport is gone. Both
// the live `ateam report` verb and `ateam prompt --role X --action
// report` route through NewReportBundle → bundle.Prompt.Resolve, with
// the prior-cycle report woven in by {{dynamic.previous_report}} via
// defaults/prompts/report/_post.previous_report.md.

// assembleRoleCode is the role-templated counterpart for "code" actions —
// `ateam prompt --role X --action code` (and any future per-role code
// command). No previous-report block: code prompts never include the
// prior report, since the source of truth for "what changed" is the git
// history of the patch the role will land.
//
// roleLabel feeds {{project.info}}; pass "" to suppress.
//
// Only roles that ship `code/<roleID>.prompt.md` are previewable; the
// assembler errors with "no role main..." when none exists. We wrap that
// to point users at the (small) set of code-capable roles — preview is
// this function's only consumer, so the guidance is worth the extra
// sentence.
func assembleRoleCode(env *root.ResolvedEnv, roleID, roleLabel, prePrompt, postPrompt string) (string, error) {
	promptPath := "code/" + roleID
	pf := prompts.PromptFile{
		Path:       promptPath,
		PrePrompt:  prePrompt,
		PostPrompt: postPrompt,
	}
	rt := flow.NewRuntime(nil, env, env.WorkDir)
	rt.SetVars(env.BuildAssemblerVars(promptPath, roleLabel, "code"))
	rt.SetDynamics(prompts.PromptDynamic{
		"project_info": prompts.ProjectInfoDynamic(env, roleLabel, "code"),
	})
	out, err := pf.Resolve(rt)
	if err != nil {
		if strings.Contains(err.Error(), "no role main") {
			return "", fmt.Errorf("no code prompt defined for role %q. Role-specific code prompts (code/<role>.prompt.md) are no longer shipped in defaults; use `ateam prompt --action code` to render the generic implementer body, or place a code/%s.prompt.md override under .ateam/prompts/", roleID, roleID)
		}
		return "", err
	}
	return out, nil
}

// previousReportBlock returns the inline "# Previous Report" section (or
// "no prior report" sentinel) for roleID, formatted the same way the legacy
// path did. Source path is env.RoleReportPath — the canonical v1
// shared/report/<role>/<role>.md (auto-migration handles legacy locations).
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
