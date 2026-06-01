package cmd

import "github.com/spf13/cobra"

// Standardized usage strings for the ad-hoc prompt-wrap flags every
// prompt-taking cmd shares. Wording is identical so `ateam <cmd> --help`
// describes the same flag the same way regardless of which cmd you ran.
//
// Wrap order (the durable contract):
//
//	anchors → dir-level _pre/_post fragments → role-level pre/post →
//	--pre-prompt (outermost head) / --post-prompt (outermost tail).
//
// --extra-prompt lands AFTER the assembled body under an
// "# Additional Instructions" heading and BEFORE --post-prompt, matching
// the historical exec shape; documented here so the placement isn't
// surprise across cmds.
const (
	UsageExtraPrompt = "additional instructions appended after the main prompt under an \"Additional Instructions\" heading (text or @filepath)"
	UsagePrePrompt   = "text wrapped at the very front of the assembled prompt, before anchor-discovered content (text or @filepath)"
	UsagePostPrompt  = "text wrapped at the very end of the assembled prompt, after every other section (text or @filepath)"
)

// addPromptWrapFlags registers --extra-prompt / --pre-prompt /
// --post-prompt on cmd with the shared usage strings. Use from any
// cmd that takes a prompt; CommonExecFlags-backed cmds get this for
// free through registerCommonExecFlags.
func addPromptWrapFlags(cmd *cobra.Command, extra, pre, post *string) {
	cmd.Flags().StringVar(extra, "extra-prompt", "", UsageExtraPrompt)
	cmd.Flags().StringVar(pre, "pre-prompt", "", UsagePrePrompt)
	cmd.Flags().StringVar(post, "post-prompt", "", UsagePostPrompt)
}
