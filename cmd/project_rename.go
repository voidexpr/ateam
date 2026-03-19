package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ateam/internal/config"
	"github.com/ateam/internal/root"
	"github.com/spf13/cobra"
)

var projectRenameCmd = &cobra.Command{
	Use:   "project-rename",
	Short: "Update state after moving a project directory",
	Long: `Update ateam state after a project directory has been moved within the org.

Since state.sqlite is per-project (inside .ateam/), no DB updates are needed.
This command only renames the legacy state directory under .ateamorg/projects/
if one exists.

Paths are relative to the org root (parent of .ateamorg/).

Example:
  ateam project-rename --old services/api --new backends/api`,
	Args: cobra.NoArgs,
	RunE: runProjectRename,
}

var (
	renameOldPath string
	renameNewPath string
)

func init() {
	projectRenameCmd.Flags().StringVar(&renameOldPath, "old", "", "old project path (relative to org root)")
	projectRenameCmd.Flags().StringVar(&renameNewPath, "new", "", "new project path (relative to org root)")
	projectRenameCmd.MarkFlagRequired("old")
	projectRenameCmd.MarkFlagRequired("new")
}

func runProjectRename(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("cannot get working directory: %w", err)
	}

	var orgDir string
	if orgFlag != "" {
		orgDir, err = root.FindOrg(orgFlag)
		if err != nil {
			return err
		}
	} else {
		orgDir, _ = root.FindOrg(cwd)
	}

	oldID := config.PathToProjectID(renameOldPath)
	newID := config.PathToProjectID(renameNewPath)
	if oldID == newID {
		fmt.Println("Old and new paths map to the same project ID; nothing to do.")
		return nil
	}

	// Warn if new path doesn't have .ateam/
	if orgDir != "" {
		orgRoot := filepath.Dir(orgDir)
		newAteamDir := filepath.Join(orgRoot, renameNewPath, root.ProjectDirName)
		if info, err := os.Stat(newAteamDir); err != nil || !info.IsDir() {
			fmt.Fprintf(os.Stderr, "Warning: %s does not exist; metadata will be updated anyway\n", newAteamDir)
		}
	}

	// Clean up legacy state dir if present
	if orgDir != "" {
		oldStateDir := filepath.Join(orgDir, "projects", oldID)
		if _, err := os.Stat(oldStateDir); err == nil {
			newStateDir := filepath.Join(orgDir, "projects", newID)
			if _, err := os.Stat(newStateDir); err == nil {
				fmt.Printf("Legacy state dir: skipped rename (%s already exists)\n", newStateDir)
			} else {
				if err := os.Rename(oldStateDir, newStateDir); err != nil {
					return fmt.Errorf("renaming legacy state dir: %w", err)
				}
				fmt.Printf("Legacy state dir: renamed %s -> %s\n", oldID, newID)
			}
		}
	}

	fmt.Println("Per-project state.sqlite requires no path updates.")
	return nil
}
