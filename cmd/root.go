package cmd

import (
	"fmt"

	"github.com/ateam/internal/container"
	"github.com/spf13/cobra"
)

var (
	orgFlag              string
	projectFlag          string
	workDirFlag          string
	sandboxDetectionFlag string
	dockerDetectionFlag  string
)

var rootCmd = &cobra.Command{
	Use:           "ateam",
	Version:       Version,
	Short:         "ATeam — background roles for code quality",
	Long:          "ATeam manages specialized roles that analyze your codebase and produce actionable reports.",
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		// Apply detection flags only when the user set them
		// explicitly. Otherwise the value comes from runtime.hcl (via
		// runtime.Load) or falls back to the built-in default.
		if cmd.Flags().Changed("sandbox-detection") {
			v, err := parseBoolFlag("sandbox-detection", sandboxDetectionFlag)
			if err != nil {
				return err
			}
			container.SetSandboxDetection(v)
		}
		if cmd.Flags().Changed("docker-detection") {
			v, err := parseBoolFlag("docker-detection", dockerDetectionFlag)
			if err != nil {
				return err
			}
			container.SetDockerDetection(v)
		}
		return nil
	},
}

func parseBoolFlag(name, value string) (bool, error) {
	switch value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("--%s requires 'true' or 'false', got %q", name, value)
	}
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&orgFlag, "org", "o", "", "path to org folder (.ateamorg/) or its parent — overrides cwd-based discovery")
	rootCmd.PersistentFlags().StringVarP(&projectFlag, "project", "p", "", "path to project folder (.ateam/) or its parent. Agent runs at the project root when cwd is inside it (git-like) or in cwd when --project points elsewhere.")
	rootCmd.PersistentFlags().StringVar(&workDirFlag, "work-dir", "", "agent working directory (overrides the project-aware default)")
	rootCmd.PersistentFlags().StringVar(&sandboxDetectionFlag, "sandbox-detection", "", "true|false: auto-detect when ateam is running inside any outer non-container sandbox (signal-driven: macOS Seatbelt probe, Linux /proc heuristics, cooperative env vars FENCE_SANDBOX/FIREJAIL_NAME/container=…) and treat it as a container so the agent's inner sandbox is skipped. Built-in default false because the signals can have false positives that silently drop defense in depth — opt in when knowingly running under an outer sandbox. Empty = use runtime.hcl or built-in default.")
	rootCmd.PersistentFlags().StringVar(&dockerDetectionFlag, "docker-detection", "", "true|false: auto-detect /.dockerenv and /run/.containerenv and treat ateam as already inside a container. Built-in default true (markers are dead-reliable). Empty = use runtime.hcl or built-in default.")

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
	rootCmd.AddCommand(runAllCmd)
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
