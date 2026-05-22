package cmd

import (
	"github.com/spf13/cobra"
)

var (
	orgFlag     string
	projectFlag string
	workDirFlag string
)

var rootCmd = &cobra.Command{
	Use:           "ateam",
	Version:       Version,
	Short:         "ATeam — background roles for code quality",
	Long:          "ATeam manages specialized roles that analyze your codebase and produce actionable reports.",
	SilenceErrors: true,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		cmd.SilenceUsage = true
	},
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&orgFlag, "org", "o", "", "path to org folder (.ateamorg/) or its parent — overrides cwd-based discovery")
	rootCmd.PersistentFlags().StringVarP(&projectFlag, "project", "p", "", "path to project folder (.ateam/) or its parent. Agent runs at the project root when cwd is inside it (git-like) or in cwd when --project points elsewhere.")
	rootCmd.PersistentFlags().StringVar(&workDirFlag, "work-dir", "", "agent working directory (overrides the project-aware default)")

	rootCmd.AddCommand(envCmd)
	rootCmd.AddCommand(installCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(reportCmd)
	rootCmd.AddCommand(reviewCmd)
	rootCmd.AddCommand(verifyCmd)
	rootCmd.AddCommand(updateCmd)
	rootCmd.AddCommand(projectsCmd)
	rootCmd.AddCommand(rolesCmd)
	rootCmd.AddCommand(execCmd)
	rootCmd.AddCommand(promptCmd)
	rootCmd.AddCommand(codeCmd)
	rootCmd.AddCommand(catCmd)
	rootCmd.AddCommand(tailCmd)
	rootCmd.AddCommand(allCmd)
	rootCmd.AddCommand(psCmd)
	rootCmd.AddCommand(costCmd)
	rootCmd.AddCommand(projectRenameCmd)
	rootCmd.AddCommand(agentConfigCmd)
	rootCmd.AddCommand(claudeCmd)
	rootCmd.AddCommand(containerCpCmd)
	rootCmd.AddCommand(parallelCmd)
	rootCmd.AddCommand(serveCmd)
	rootCmd.AddCommand(exportCmd)
	rootCmd.AddCommand(secretCmd)
	rootCmd.AddCommand(autoSetupCmd)
	rootCmd.AddCommand(inspectCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(projectInfoCmd)
}
