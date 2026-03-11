package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/ateam-poc/internal/root"
	"github.com/spf13/cobra"
)

var installCmd = &cobra.Command{
	Use:   "install [PATH]",
	Short: "Create a .ateamorg/ directory with default prompts",
	Long: `Create a .ateamorg/ directory at PATH (defaults to ".") with default
prompt files for all roles and the supervisor.

Example:
  ateam install              # creates .ateamorg/ in current directory
  ateam install /path/to/org # creates .ateamorg/ at the given path`,
	Args: cobra.MaximumNArgs(1),
	RunE: runInstall,
}

func runInstall(cmd *cobra.Command, args []string) error {
	parentDir := "."
	if len(args) > 0 {
		parentDir = args[0]
	}

	absDir, err := filepath.Abs(parentDir)
	if err != nil {
		return fmt.Errorf("cannot resolve path: %w", err)
	}

	orgDir, err := root.InstallOrg(absDir)
	if err != nil {
		return err
	}

	fmt.Printf("Created %s\n", orgDir)
	fmt.Printf("Default prompts for all roles and supervisor are ready.\n")
	return nil
}
