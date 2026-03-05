package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ateam-poc/internal/prompts"
	"github.com/ateam-poc/internal/root"
	"github.com/spf13/cobra"
)

var BuildTime = "unknown"

var updatePromptsSymlink string

var updatePromptsCmd = &cobra.Command{
	Use:   "update-prompts",
	Short: "Update default prompts to the version embedded in this binary",
	Long: `Compare on-disk prompts with the defaults embedded in the ateam binary
and overwrite any that differ. Shows which files changed.

Use --symlink to replace .ateam/defaults with a symlink to PATH.
PATH must contain agents/ and supervisor/ directories.

Example:
  ateam update-prompts
  ateam update-prompts --symlink ~/my-prompts`,
	RunE: runUpdatePrompts,
}

func init() {
	updatePromptsCmd.Flags().StringVar(&updatePromptsSymlink, "symlink", "", "symlink .ateam/defaults to PATH (must contain agents/ and supervisor/)")
}

func runUpdatePrompts(cmd *cobra.Command, args []string) error {
	proj, err := root.Resolve(nil)
	if err != nil {
		return err
	}

	fmt.Printf("Binary built: %s\n\n", BuildTime)

	if updatePromptsSymlink != "" {
		return symlinkDefaults(proj.AteamRoot, updatePromptsSymlink)
	}

	return updatePrompts(proj.AteamRoot)
}

func updatePrompts(ateamRoot string) error {
	diffs := prompts.DiffRootDefaults(ateamRoot)
	if len(diffs) == 0 {
		fmt.Println("All prompts are up to date.")
		return nil
	}

	fmt.Printf("Found %d prompt(s) to update:\n", len(diffs))
	for _, d := range diffs {
		fmt.Printf("  %-55s %s\n", d.RelPath, d.Status)
	}
	fmt.Println()

	if err := prompts.WriteRootDefaults(ateamRoot, true); err != nil {
		return err
	}

	fmt.Println("Prompts updated.")
	return nil
}

func symlinkDefaults(ateamRoot, symlinkPath string) error {
	absPath, err := filepath.Abs(symlinkPath)
	if err != nil {
		return fmt.Errorf("cannot resolve path: %w", err)
	}

	for _, sub := range []string{"agents", "supervisor"} {
		dir := filepath.Join(absPath, sub)
		info, err := os.Stat(dir)
		if err != nil {
			return fmt.Errorf("%s/ not found at %s", sub, absPath)
		}
		if !info.IsDir() {
			return fmt.Errorf("%s is not a directory", dir)
		}
	}

	link := filepath.Join(ateamRoot, "defaults")
	if err := os.RemoveAll(link); err != nil {
		return fmt.Errorf("cannot remove %s: %w", link, err)
	}
	if err := os.Symlink(absPath, link); err != nil {
		return fmt.Errorf("cannot create symlink %s -> %s: %w", link, absPath, err)
	}

	fmt.Printf("  %s -> %s\n", link, absPath)
	fmt.Println("\nSymlink created.")
	return nil
}
