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
	Short: "Re-register or rename a project in the org",
	Long: `Re-register a project with its org, or rename its state after a directory move.

Without --old/--new flags, re-registers the current project at its current
location. Use this after moving a project directory to update the org link.

With --old and --new flags, renames the legacy state directory under
.ateamorg/projects/.

Examples:
  ateam project-rename                                  # re-register current project
  ateam project-rename --old services/api --new backends/api  # rename after move`,
	Args: cobra.NoArgs,
	RunE: runProjectRename,
}

var (
	renameOldPath string
	renameNewPath string
	renameDryRun  bool
)

func init() {
	projectRenameCmd.Flags().StringVar(&renameOldPath, "old", "", "old project path (relative to org root)")
	projectRenameCmd.Flags().StringVar(&renameNewPath, "new", "", "new project path (relative to org root)")
	projectRenameCmd.Flags().BoolVar(&renameDryRun, "dry-run", false, "show what would be done without executing")
}

func runProjectRename(cmd *cobra.Command, args []string) error {
	// No flags: re-register current project at its current location.
	if renameOldPath == "" && renameNewPath == "" {
		return runProjectReregister()
	}

	if renameOldPath == "" || renameNewPath == "" {
		return fmt.Errorf("both --old and --new are required for explicit rename")
	}

	return runProjectExplicitRename()
}

// runProjectReregister re-registers the current project with its org.
func runProjectReregister() error {
	env, err := root.Lookup(orgFlag, projectFlag)
	if err != nil {
		return err
	}
	if env.OrgDir == "" {
		return fmt.Errorf("no .ateamorg/ found — cannot register project")
	}
	if env.SourceDir == "" {
		return fmt.Errorf("no project found in current directory")
	}

	projectID := env.ProjectID()
	if projectID == "" {
		return fmt.Errorf("cannot compute project ID")
	}

	prefix := ""
	if renameDryRun {
		prefix = "[dry-run] "
	}

	fmt.Printf("  Project:  %s\n", env.ProjectName)
	fmt.Printf("  Path:     %s\n", env.RelPath(env.SourceDir))
	fmt.Printf("  Org:      %s\n", env.OrgDir)
	fmt.Printf("  ID:       %s\n", projectID)
	fmt.Println()

	stateDir := filepath.Join(env.OrgDir, "projects", projectID)
	if _, err := os.Stat(stateDir); err == nil {
		fmt.Printf("%sProject %q is already registered at %s\n", prefix, env.ProjectName, env.RelPath(env.SourceDir))
		return nil
	}

	fmt.Printf("%sCreate %s\n", prefix, stateDir)

	// Show stale registrations that would be cleaned up
	stale := findStaleRegistrations(env)
	for _, s := range stale {
		fmt.Printf("%sRemove stale registration: %s\n", prefix, s)
	}

	if renameDryRun {
		return nil
	}

	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return fmt.Errorf("creating state directory: %w", err)
	}

	fmt.Printf("Registered project %q at %s\n", env.ProjectName, env.RelPath(env.SourceDir))

	for _, s := range stale {
		staleDir := filepath.Join(env.OrgDir, "projects", config.PathToProjectID(s))
		if err := os.RemoveAll(staleDir); err == nil {
			fmt.Printf("  Removed stale registration: %s\n", s)
		}
	}

	return nil
}

// findStaleRegistrations returns relative paths of org registrations whose
// project directories no longer exist.
func findStaleRegistrations(env *root.ResolvedEnv) []string {
	projectsDir := filepath.Join(env.OrgDir, "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil
	}
	orgRoot := env.OrgRoot()
	var stale []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		relPath := config.ProjectIDToPath(e.Name())
		projDir := filepath.Join(orgRoot, relPath, root.ProjectDirName)
		if _, err := os.Stat(projDir); os.IsNotExist(err) {
			stale = append(stale, relPath)
		}
	}
	return stale
}

func runProjectExplicitRename() error {
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

	prefix := ""
	if renameDryRun {
		prefix = "[dry-run] "
	}

	fmt.Printf("  Old path: %s (ID: %s)\n", renameOldPath, oldID)
	fmt.Printf("  New path: %s (ID: %s)\n", renameNewPath, newID)
	if orgDir != "" {
		fmt.Printf("  Org:      %s\n", orgDir)
	}
	fmt.Println()

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
				fmt.Printf("%sSkip rename: %s already exists\n", prefix, newStateDir)
			} else {
				fmt.Printf("%sRename %s → %s\n", prefix, oldStateDir, newStateDir)
				if !renameDryRun {
					if err := os.Rename(oldStateDir, newStateDir); err != nil {
						return fmt.Errorf("renaming legacy state dir: %w", err)
					}
				}
			}
		} else {
			fmt.Printf("  No existing state dir at %s\n", oldStateDir)
		}
	}

	if !renameDryRun {
		fmt.Println("Per-project state.sqlite requires no path updates.")
	}
	return nil
}
