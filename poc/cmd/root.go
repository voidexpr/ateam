package cmd

import (
	"github.com/spf13/cobra"
)

var (
	orgFlag     string
	projectFlag string
)

var rootCmd = &cobra.Command{
	Use:   "ateam",
	Short: "ATeam — background roles for code quality",
	Long:  "ATeam manages specialized roles that analyze your codebase and produce actionable reports.",
	SilenceErrors: true,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		cmd.SilenceUsage = true
	},
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&orgFlag, "org", "o", "", "organization path override")
	rootCmd.PersistentFlags().StringVarP(&projectFlag, "project", "p", "", "project name override")

	rootCmd.AddCommand(envCmd)
	rootCmd.AddCommand(installCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(reportCmd)
	rootCmd.AddCommand(reviewCmd)
	rootCmd.AddCommand(updateCmd)
	rootCmd.AddCommand(projectsCmd)
	rootCmd.AddCommand(rolesCmd)
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(promptCmd)
	rootCmd.AddCommand(codeCmd)
	rootCmd.AddCommand(logCmd)
	rootCmd.AddCommand(allCmd)
	rootCmd.AddCommand(recentRunsCmd)
	rootCmd.AddCommand(costCmd)
}
