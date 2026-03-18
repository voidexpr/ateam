package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ateam/internal/config"
	"github.com/ateam/internal/root"
	"github.com/spf13/cobra"
)

var projectRenameCmd = &cobra.Command{
	Use:   "project-rename",
	Short: "Update state after moving a project directory",
	Long: `Update ateam state after a project directory has been moved within the org.

Rewrites the project_id in the call database, updates stream_file paths,
and renames the state directory under .ateamorg/projects/.

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
	} else {
		orgDir, err = root.FindOrg(cwd)
	}
	if err != nil {
		return err
	}

	oldID := config.PathToProjectID(renameOldPath)
	newID := config.PathToProjectID(renameNewPath)
	if oldID == newID {
		fmt.Println("Old and new paths map to the same project ID; nothing to do.")
		return nil
	}

	db := openCallDB(orgDir)
	if db == nil {
		return fmt.Errorf("cannot open call database")
	}
	defer db.Close()

	// Check for running agents
	running, err := db.FindRunning(oldID, "")
	if err != nil {
		return fmt.Errorf("checking running agents: %w", err)
	}
	var alive []string
	for _, r := range running {
		if r.PID > 0 && isProcessAlive(r.PID) {
			alive = append(alive, fmt.Sprintf("  %s/%s (PID %d)", r.Action, r.Role, r.PID))
		}
	}
	if len(alive) > 0 {
		return fmt.Errorf("agents are currently running for this project; stop them first:\n%s",
			strings.Join(alive, "\n"))
	}

	// Warn if new path doesn't have .ateam/
	orgRoot := filepath.Dir(orgDir)
	newAteamDir := filepath.Join(orgRoot, renameNewPath, root.ProjectDirName)
	if info, err := os.Stat(newAteamDir); err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "Warning: %s does not exist; metadata will be updated anyway\n", newAteamDir)
	}

	// Rename state dir
	oldStateDir := filepath.Join(orgDir, "projects", oldID)
	newStateDir := filepath.Join(orgDir, "projects", newID)
	stateDirMsg := ""

	if _, err := os.Stat(oldStateDir); err == nil {
		if _, err := os.Stat(newStateDir); err == nil {
			stateDirMsg = fmt.Sprintf("State dir: skipped rename (%s already exists)", newStateDir)
		} else {
			if err := os.Rename(oldStateDir, newStateDir); err != nil {
				return fmt.Errorf("renaming state dir: %w", err)
			}
			stateDirMsg = fmt.Sprintf("State dir: renamed %s → %s", oldID, newID)
		}
	} else {
		stateDirMsg = "State dir: nothing to rename (old dir not found)"
	}

	// Update DB
	n, err := db.RenameProject(oldID, newID)
	if err != nil {
		return fmt.Errorf("updating database: %w", err)
	}

	fmt.Println(stateDirMsg)
	fmt.Printf("Database: %d row(s) updated (%s → %s)\n", n, oldID, newID)
	return nil
}
