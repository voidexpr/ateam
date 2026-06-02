package cmd

import "github.com/spf13/cobra"

// Standardized usage strings for the ad-hoc prompt-wrap flags every
// prompt-taking cmd shares. Wording is identical so `ateam <cmd> --help`
// describes the same flag the same way regardless of which cmd you ran.
//
// Wrap order (the durable contract):
//
//	--pre-prompt (outermost head) → anchors → dir-level _pre/_post
//	fragments → role-level pre/post → body → --post-prompt (outermost tail).
//
// --extra-prompt is gone in this version. The auto-prepended
// "# Additional Instructions" heading + inner-of-post position were the
// only things it did differently from --post-prompt; users who want that
// shape write `--post-prompt "# Additional Instructions\n\n…"`
// themselves.
const (
	UsagePrePrompt  = "text wrapped at the very front of the assembled prompt, before anchor-discovered content (text or @filepath)"
	UsagePostPrompt = "text wrapped at the very end of the assembled prompt, after every other section (text or @filepath)"
)

// addPromptWrapFlags registers --pre-prompt / --post-prompt on cmd with
// the shared usage strings. Use from any cmd that takes a prompt;
// CommonExecFlags-backed cmds get this for free through
// registerCommonExecFlags.
func addPromptWrapFlags(cmd *cobra.Command, pre, post *string) {
	cmd.Flags().StringVar(pre, "pre-prompt", "", UsagePrePrompt)
	cmd.Flags().StringVar(post, "post-prompt", "", UsagePostPrompt)
}
