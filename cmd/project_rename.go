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
	Use:   "project-rename [--name NEW_NAME]",
	Short: "Re-register the current project, or rename it",
	Long: `Without --name, re-registers the current project at its current location
in the user's registry (~/.ateam/state/projects.hcl). Use this after moving
a project directory.

With --name, renames the project: updates the project block in
.ateam/ateam.hcl, the rows in the project's state database (project_id is
the name), and the registry entry.

Examples:
  ateam project-rename                  # re-register after a move
  ateam project-rename --name backend   # rename`,
	Args: cobra.NoArgs,
	RunE: runProjectRename,
}

var (
	renameNewName string
	renameDryRun  bool
)

func init() {
	projectRenameCmd.Flags().StringVar(&renameNewName, "name", "", "new project name")
	projectRenameCmd.Flags().BoolVar(&renameDryRun, "dry-run", false, "show what would be done without executing")
}

func runProjectRename(cmd *cobra.Command, args []string) error {
	env, err := resolveEnv()
	if err != nil {
		return err
	}
	prefix := ""
	if renameDryRun {
		prefix = "[dry-run] "
	}

	if renameNewName == "" {
		return reregisterProject(env, prefix)
	}
	return renameProject(env, renameNewName, prefix)
}

func reregisterProject(env *root.ResolvedEnv, prefix string) error {
	entry, ok := root.LookupRegistry(env.HomeDir, env.ProjectName)
	fmt.Printf("  Project: %s\n  Path:    %s\n", env.ProjectName, tildeHome(env.SourceDir))
	if ok && entry.Path == env.SourceDir {
		fmt.Printf("%sAlready registered at this path.\n", prefix)
		return nil
	}
	if ok {
		fmt.Printf("%sUpdate registry entry: %s → %s\n", prefix, tildeHome(entry.Path), tildeHome(env.SourceDir))
	} else {
		fmt.Printf("%sRegister project %q\n", prefix, env.ProjectName)
	}
	if renameDryRun {
		return nil
	}
	entry.Name = env.ProjectName
	entry.Path = env.SourceDir
	if entry.KeychainKey == "" {
		entry.KeychainKey = env.KeychainKey
	}
	if entry.KeychainKey == "" {
		// Never registered (e.g. migrated by hand): give project-scoped
		// keychain secrets a stable account prefix.
		entry.KeychainKey = root.NewKeychainKey(env.ProjectName, env.ProjectDir)
	}
	return root.Register(env.HomeDir, entry)
}

func renameProject(env *root.ResolvedEnv, newName, prefix string) error {
	if err := config.ValidateProjectName(newName); err != nil {
		return err
	}
	if newName == env.ProjectName {
		fmt.Println("The project already has that name; nothing to do.")
		return nil
	}
	if other, ok := root.LookupRegistry(env.HomeDir, newName); ok && other.Path != env.SourceDir {
		return fmt.Errorf("project %q already exists at %s", newName, other.Path)
	}

	configPath := filepath.Join(env.ProjectDir, root.ConfigFile)
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	oldLine := fmt.Sprintf("name = %q", env.ProjectName)
	if !strings.Contains(string(data), oldLine) {
		return fmt.Errorf("cannot find `%s` in %s; edit the project block by hand", oldLine, configPath)
	}

	fmt.Printf("  %s: %s → %s\n", tildeHome(configPath), env.ProjectName, newName)
	fmt.Printf("  state.sqlite: project_id %q → %q\n", env.ProjectName, newName)
	fmt.Printf("  registry: %q → %q\n", env.ProjectName, newName)
	if renameDryRun {
		return nil
	}

	updated := strings.Replace(string(data), oldLine, fmt.Sprintf("name = %q", newName), 1)
	if err := os.WriteFile(configPath, []byte(updated), 0644); err != nil {
		return err
	}
	if db, err := requireStateDB(env); err == nil {
		defer db.Close()
		if _, err := db.RenameProject(env.ProjectName, newName); err != nil {
			return fmt.Errorf("renaming project rows: %w", err)
		}
	}
	entry, _ := root.LookupRegistry(env.HomeDir, env.ProjectName)
	if err := root.Unregister(env.HomeDir, env.ProjectName); err != nil {
		return err
	}
	entry.Name = newName
	entry.Path = env.SourceDir
	return root.Register(env.HomeDir, entry)
}
