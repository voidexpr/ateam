package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runtime"
	"github.com/spf13/cobra"
)

var projectsCmd = &cobra.Command{
	Use:   "projects",
	Short: "List the projects registered under the user's ateam directory",
	Long: `List every project 'ateam init' registered (~/.ateam/state/projects.hcl)
with its path and whether its .ateam/ still exists there.

Example:
  ateam projects`,
	Args: cobra.NoArgs,
	RunE: runProjects,
}

func runProjects(cmd *cobra.Command, args []string) error {
	homeDir := homeFlag
	if homeDir == "" {
		homeDir = runtime.HomeDir()
	}
	entries, err := root.LoadRegistry(homeDir)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Println("No projects registered. Run 'ateam init' in a project.")
		return nil
	}

	w := newTable()
	fmt.Fprintln(w, "NAME\tPATH\tSTATUS")
	for _, e := range entries {
		status := "ok"
		projDir := filepath.Join(e.Path, root.DirName)
		if _, err := os.Stat(projDir); err != nil {
			status = "missing (moved or deleted; 'ateam project-rename' from the new location fixes it)"
		} else if n := runtime.ProjectName(projDir); n == "" {
			status = "no project block in " + root.ConfigFile
		} else if n != e.Name {
			status = fmt.Sprintf("named %q in %s", n, root.ConfigFile)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", e.Name, tildeHome(e.Path), status)
	}
	w.Flush()
	return nil
}
