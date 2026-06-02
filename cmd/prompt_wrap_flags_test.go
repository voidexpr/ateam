package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestPromptWrapFlagHelpUniform asserts that every prompt-taking cmd
// describes --pre-prompt / --post-prompt identically (modulo a
// per-cmd suffix where the flag's scope legitimately differs, like
// parallel's "applied to each task's prompt"). Future drift across
// cmds fails this test.
//
// --extra-prompt no longer exists — it was dropped; users compose its
// effect via `--post-prompt "# Additional Instructions\n\n..."`. Its
// absence from every cmd is asserted by TestNoExtraPromptFlag below.
func TestPromptWrapFlagHelpUniform(t *testing.T) {
	type wrapFlagCase struct {
		cmd        *cobra.Command
		preSuffix  string // trailing per-cmd qualifier appended to UsagePrePrompt
		postSuffix string // same for UsagePostPrompt
	}
	cases := []wrapFlagCase{
		{cmd: execCmd},
		{cmd: parallelCmd,
			preSuffix:  " (applied to each task's prompt)",
			postSuffix: " (applied to each task's prompt)"},
		{cmd: reportCmd},
		{cmd: reviewCmd},
		{cmd: verifyCmd},
		{cmd: codeCmd},
		{cmd: autoSetupCmd},
		{cmd: runAllCmd},
		{cmd: inspectCmd},
		{cmd: promptCmd},
	}

	for _, c := range cases {
		t.Run(c.cmd.Name(), func(t *testing.T) {
			pre := c.cmd.Flags().Lookup("pre-prompt")
			if pre == nil {
				t.Fatalf("%s: missing --pre-prompt flag", c.cmd.Name())
			}
			if pre.Usage != UsagePrePrompt+c.preSuffix {
				t.Errorf("%s --pre-prompt usage drift:\n  got:  %q\n  want: %q",
					c.cmd.Name(), pre.Usage, UsagePrePrompt+c.preSuffix)
			}

			post := c.cmd.Flags().Lookup("post-prompt")
			if post == nil {
				t.Fatalf("%s: missing --post-prompt flag", c.cmd.Name())
			}
			if post.Usage != UsagePostPrompt+c.postSuffix {
				t.Errorf("%s --post-prompt usage drift:\n  got:  %q\n  want: %q",
					c.cmd.Name(), post.Usage, UsagePostPrompt+c.postSuffix)
			}
		})
	}
}

// TestNoExtraPromptFlag asserts that --extra-prompt is gone from every
// prompt-taking cmd. Backstop against accidental re-introduction; we
// dropped the flag deliberately (the auto-heading + inner-of-post
// position were the only thing it did differently from --post-prompt,
// and users write `# Additional Instructions\n\n...` themselves now).
func TestNoExtraPromptFlag(t *testing.T) {
	for _, c := range []*cobra.Command{
		execCmd, parallelCmd, reportCmd, reviewCmd, verifyCmd, codeCmd,
		autoSetupCmd, runAllCmd, inspectCmd, promptCmd,
	} {
		if f := c.Flags().Lookup("extra-prompt"); f != nil {
			t.Errorf("%s still registers --extra-prompt; flag was dropped", c.Name())
		}
	}
}

// TestParallelDeprecatedCommonPromptFlags asserts that --common-prompt-first /
// --common-prompt-last are still accepted by `ateam parallel`, marked
// deprecated, and back the same vars as the canonical --pre-prompt /
// --post-prompt. Backward-compat: existing user scripts pinned to the
// old names keep working until the next deprecation cycle.
func TestParallelDeprecatedCommonPromptFlags(t *testing.T) {
	for _, name := range []string{"common-prompt-first", "common-prompt-last"} {
		f := parallelCmd.Flags().Lookup(name)
		if f == nil {
			t.Fatalf("parallel: missing legacy alias --%s", name)
		}
		if f.Deprecated == "" {
			t.Errorf("parallel --%s should be marked deprecated; got empty Deprecated string", name)
		}
		if !strings.Contains(f.Deprecated, "--pre-prompt") && !strings.Contains(f.Deprecated, "--post-prompt") {
			t.Errorf("parallel --%s deprecation message %q should mention canonical flag", name, f.Deprecated)
		}
	}
}
