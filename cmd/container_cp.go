package cmd

import (
	"fmt"

	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runtime"
	"github.com/spf13/cobra"
)

var (
	containerCpProfile   string
	containerCpContainer string
	containerCpDryRun    bool
)

var containerCpCmd = &cobra.Command{
	Use:   "container-cp",
	Short: "Copy ateam binary into a Docker container",
	Long: `Copy the ateam linux binary into a running Docker container via docker cp.

Example:
  ateam container-cp --container-name my-app-dev
  ateam container-cp --profile my-app`,
	Args: cobra.NoArgs,
	RunE: runContainerCp,
}

func init() {
	containerCpCmd.Flags().StringVar(&containerCpProfile, "profile", "", "profile to read container name from")
	containerCpCmd.Flags().StringVar(&containerCpContainer, "container-name", "", "target container name (overrides profile)")
	containerCpCmd.Flags().BoolVar(&containerCpDryRun, "dry-run", false, "show what would be copied without executing")
}

func runContainerCp(cmd *cobra.Command, args []string) error {
	containerName := containerCpContainer

	if containerName == "" && containerCpProfile != "" {
		env, _ := root.Lookup("", "")
		var projectDir, orgDir string
		if env != nil {
			projectDir = env.ProjectDir
			orgDir = env.OrgDir
		}
		rtCfg, err := runtime.Load(projectDir, orgDir)
		if err != nil {
			return fmt.Errorf("cannot load runtime.hcl: %w", err)
		}
		_, _, cc, err := rtCfg.ResolveProfile(containerCpProfile)
		if err != nil {
			return err
		}
		if cc == nil || cc.DockerContainer == "" {
			return fmt.Errorf("profile %q has no docker_container configured", containerCpProfile)
		}
		containerName = cc.DockerContainer
	}

	if containerName == "" {
		return fmt.Errorf("specify --container-name or --profile")
	}

	containerName, err := resolveContainerName(containerName)
	if err != nil {
		return err
	}

	env, _ := root.Lookup("", "")
	orgDir := ""
	if env != nil {
		orgDir = env.OrgDir
	}

	if containerCpDryRun {
		binary := findLinuxBinary(orgDir)
		if binary == "" {
			return fmt.Errorf("no linux ateam binary found (run 'make companion' to build one)")
		}
		fmt.Printf("[dry-run] %s → %s:%s\n", binary, containerName, ateamContainerBinPath)
		return nil
	}

	return copyAteamBinary(containerName, orgDir)
}
