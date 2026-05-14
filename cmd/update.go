package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runtime"
	"github.com/spf13/cobra"
)

var (
	BuildTime = "unknown"
	Version   = "dev"
	GitCommit = "unknown"
)

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

	fmt.Printf("Binary built: %s\n\n", FormatBuildTime(BuildTime, time.Now()))

	promptDiffs := prompts.DiffOrgDefaults(orgDir)
	runtimeDiffs := runtime.DiffOrgDefaults(orgDir)
	total := len(promptDiffs) + len(runtimeDiffs)
	if total == 0 {
		fmt.Println("All defaults are up to date.")
		return nil
	}

	showDiff := !updateQuiet || updateDiff
	if showDiff {
		fmt.Printf("Found %d file(s) to update:\n", total)
		for _, d := range promptDiffs {
			fmt.Printf("  %-55s %s\n", d.RelPath, d.Status)
		}
		for _, d := range runtimeDiffs {
			fmt.Printf("  %-55s %s\n", d.RelPath, d.Status)
		}
		fmt.Println()
	}

	if err := prompts.WriteOrgDefaults(orgDir, true); err != nil {
		return err
	}
	if err := runtime.WriteOrgDefaults(orgDir, true); err != nil {
		return err
	}

	fmt.Printf("Updated %d file(s) in %s\n", total, orgDir)
	return nil
}
