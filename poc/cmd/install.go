package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ateam-poc/internal/prompts"
	"github.com/ateam-poc/internal/root"
	"github.com/spf13/cobra"
)

var installUpdatePrompts bool

var installCmd = &cobra.Command{
	Use:   "install [PATH]",
	Short: "Create a .ateam/ directory with default prompts",
	Long: `Create a .ateam/ directory at PATH (defaults to $HOME) with default
prompt files for all agents and the supervisor.

Example:
  ateam install              # creates ~/.ateam/
  ateam install .            # creates .ateam/ in current directory
  ateam install --update-prompts  # rewrite default prompts without touching projects/`,
	Args: cobra.MaximumNArgs(1),
	RunE: runInstall,
}

func init() {
	installCmd.Flags().BoolVar(&installUpdatePrompts, "update-prompts", false, "rewrite default prompts (does not touch projects/ or custom overrides)")
}

func runInstall(cmd *cobra.Command, args []string) error {
	var parentDir string
	if len(args) > 0 {
		var err error
		parentDir, err = filepath.Abs(args[0])
		if err != nil {
			return fmt.Errorf("cannot resolve path: %w", err)
		}
	} else {
		var err error
		parentDir, err = os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("cannot determine home directory: %w", err)
		}
	}

	ateamRoot := filepath.Join(parentDir, ".ateam")

	if installUpdatePrompts {
		if _, err := os.Stat(ateamRoot); os.IsNotExist(err) {
			return fmt.Errorf(".ateam/ does not exist at %s — run 'ateam install' first", parentDir)
		}
		if err := prompts.WriteRootDefaults(ateamRoot, true); err != nil {
			return err
		}
		fmt.Printf("Updated default prompts in %s\n", ateamRoot)
		return nil
	}

	if _, err := os.Stat(ateamRoot); err == nil {
		return fmt.Errorf(".ateam/ already exists at %s (use --update-prompts to rewrite defaults)", parentDir)
	}

	ateamRoot, err := root.Install(parentDir)
	if err != nil {
		return err
	}

	fmt.Printf("Created %s\n", ateamRoot)
	fmt.Printf("Default prompts for all agents and supervisor are ready.\n")
	return nil
}
