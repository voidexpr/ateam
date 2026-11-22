package cmd

import (
	"github.com/spf13/cobra"
)

var (
	runFlags   launchFlags
	shellFlags launchFlags
	chatFlags  launchFlags
)

var runCmd = &cobra.Command{
	Use:   "run [flags] [-- COMMAND [ARG]...]",
	Short: "Run a command under a profile's sandbox",
	Long: `Run a command with the file and network access of the composed profile.

The command is the profile's saved 'command' with the arguments after --
appended, or, when the profile saves none, the command given after --.
--command replaces the saved command for this launch.

A profile whose rules its container cannot enforce is refused.

Example:
  ateam run --profile sandbox/nono,dev/go -- make test
  ateam run --profile build -- -run TestFoo
  ateam run --profile build --command "make lint"
  ateam run --profile sandbox/nono --dry-run -- true`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runLaunch(&runFlags, modeRun, args)
	},
}

var shellCmd = &cobra.Command{
	Use:   "shell [flags] [-- ARG]...",
	Short: "Open your shell under a profile's sandbox",
	Long: `Open $SHELL with the file and network access of the composed profile:
what an agent started with the same profile could reach.

Example:
  ateam shell --profile sandbox/nono,dev/go`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runLaunch(&shellFlags, modeShell, args)
	},
}

var chatCmd = &cobra.Command{
	Use:   "chat [flags] [-- AGENT_ARG...]",
	Short: "Start an attended agent session under a profile",
	Long: `Start the profile's agent (the default agent when it names none) in this
terminal, with your own agent configuration. Arguments after -- go to the
agent.

Under a sandbox backend the agent/<type> profile is added (the agent's own
files and API hosts), and Claude's own sandbox is turned off for the launch.

Example:
  ateam chat --profile sandbox/nono,dev/go
  ateam chat --profile myproject -- --continue`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runLaunch(&chatFlags, modeChat, args)
	},
}

func init() {
	for cmd, flags := range map[*cobra.Command]*launchFlags{runCmd: &runFlags, shellCmd: &shellFlags, chatCmd: &chatFlags} {
		addLaunchFlags(cmd, flags)
		cmd.Flags().BoolVar(&flags.dryRun, "dry-run", false, "print the rules and the command line, run nothing")
		// Everything after the first argument belongs to the command.
		cmd.Flags().SetInterspersed(false)
		rootCmd.AddCommand(cmd)
	}
	runCmd.Flags().StringVar(&runFlags.command, "command", "", "command to run instead of the profile's saved one (split on spaces)")
}
