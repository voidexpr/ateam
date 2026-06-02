package runner

import (
	"path/filepath"

	"github.com/ateam/internal/prompts"
)

// PromptFilenameFor returns the canonical archived prompt-filename for an
// action's runs (e.g. "report" → "report_prompt.md"). Empty for actions
// that have no per-run archived prompt file.
func PromptFilenameFor(action string) string {
	switch action {
	case ActionReport:
		return "report_prompt.md"
	case ActionReview:
		return "review_prompt.md"
	case ActionCode:
		return "code_management_prompt.md"
	case ActionVerify:
		return prompts.CodeVerifyPromptFile
	case ActionExec:
		return "run_prompt.md"
	}
	return ""
}

// OutputFilenameFor returns the canonical archived output-filename for an
// action's runs (e.g. "report" → "report.md"). Empty for actions that don't
// produce a per-run output file.
func OutputFilenameFor(action string) string {
	switch action {
	case ActionReport:
		return "report.md"
	case ActionReview:
		return "review.md"
	case ActionVerify:
		return "verify.md"
	}
	return ""
}

// HistoryDirFor returns the project-relative path of the history directory
// where archived runs of action are stored for the given role.
func HistoryDirFor(action, role string) string {
	switch action {
	case ActionReport, ActionExec:
		return filepath.Join("roles", role, "history")
	default:
		return filepath.Join("supervisor", "history")
	}
}
