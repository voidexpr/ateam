package cmd

import (
	"fmt"
	"os"

	"github.com/ateam-poc/internal/prompts"
	"github.com/ateam-poc/internal/root"
	"github.com/spf13/cobra"
)

var BuildTime = "unknown"

var (
	updateQuiet bool
	updateDiff  bool
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update default prompts to the version embedded in this binary",
	Long: `Compare on-disk prompts with the defaults embedded in the ateam binary
and overwrite any that differ. Shows which files changed.

Example:
  ateam update
  ateam update --diff
  ateam update --quiet`,
	RunE: runUpdate,
}

func init() {
	updateCmd.Flags().BoolVarP(&updateQuiet, "quiet", "q", false, "suppress diff output")
	updateCmd.Flags().BoolVar(&updateDiff, "diff", false, "show diffs (default unless --quiet)")
}

func runUpdate(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("cannot get working directory: %w", err)
	}

	orgDir, err := root.FindOrg(cwd)
	if err != nil {
		return err
	}

	fmt.Printf("Binary built: %s\n\n", BuildTime)

	diffs := prompts.DiffOrgDefaults(orgDir)
	if len(diffs) == 0 {
		fmt.Println("All prompts are up to date.")
		return nil
	}

	showDiff := !updateQuiet || updateDiff
	if showDiff {
		fmt.Printf("Found %d prompt(s) to update:\n", len(diffs))
		for _, d := range diffs {
			fmt.Printf("  %-55s %s\n", d.RelPath, d.Status)
		}
		fmt.Println()
	}

	if err := prompts.WriteOrgDefaults(orgDir, true); err != nil {
		return err
	}

	fmt.Printf("Updated %d prompt(s) in %s\n", len(diffs), orgDir)
	return nil
}
