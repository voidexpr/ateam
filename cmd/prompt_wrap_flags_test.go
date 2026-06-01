package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestPromptWrapFlagHelpUniform asserts that every prompt-taking cmd
// describes --pre-prompt / --post-prompt / --extra-prompt identically.
// The expectation is the shared usage strings from prompt_wrap_flags.go;
// any cmd that re-registers these inline would silently drift, and this
// test catches it.
//
// parallel is excluded from --extra-prompt because it never had an
// equivalent flag (parallel's whole shape is "drive N prompts at once,"
// not "append extras to one prompt"). The pre/post-prompt expectations
// still apply to parallel.
func TestPromptWrapFlagHelpUniform(t *testing.T) {
	type wrapFlagCase struct {
		cmd        *cobra.Command
		skipExtra  bool
		preSuffix  string // trailing per-cmd qualifier appended to UsagePrePrompt
		postSuffix string // same for UsagePostPrompt
	}
	cases := []wrapFlagCase{
		{cmd: execCmd},
		{cmd: parallelCmd, skipExtra: true,
			preSuffix:  " (applied to each task's prompt)",
			postSuffix: " (applied to each task's prompt)"},
		{cmd: reportCmd},
		{cmd: reviewCmd},
		{cmd: verifyCmd},
		{cmd: codeCmd},
		{cmd: autoSetupCmd},
		{cmd: allCmd},
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

			if c.skipExtra {
				return
			}
			extra := c.cmd.Flags().Lookup("extra-prompt")
			if extra == nil {
				t.Fatalf("%s: missing --extra-prompt flag", c.cmd.Name())
			}
			if extra.Usage != UsageExtraPrompt {
				t.Errorf("%s --extra-prompt usage drift:\n  got:  %q\n  want: %q",
					c.cmd.Name(), extra.Usage, UsageExtraPrompt)
			}
		})
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
