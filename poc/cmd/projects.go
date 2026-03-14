package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ateam-poc/internal/root"
	"github.com/spf13/cobra"
)

var projectsCmd = &cobra.Command{
	Use:   "projects",
	Short: "List all projects under the current organization",
	Long: `List all registered projects under the current organization and print
a summary table.

Example:
  ateam projects`,
	Args: cobra.NoArgs,
	RunE: runProjects,
}

func runProjects(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("cannot get working directory: %w", err)
	}

	orgDir, err := root.FindOrg(cwd)
	if err != nil {
		return err
	}

	orgRoot := filepath.Dir(orgDir)

	type projectRow struct {
		name, path, gitRepo, gitRemote string
	}

	var rows []projectRow

	err = root.WalkProjects(orgDir, func(p root.ProjectInfo) error {
		projPath := filepath.Dir(p.Dir)
		relPath, relErr := filepath.Rel(orgRoot, projPath)
		if relErr != nil {
			relPath = projPath
		}
		rows = append(rows, projectRow{
			name:      p.Config.Project.Name,
			path:      relPath,
			gitRepo:   p.Config.Git.Repo,
			gitRemote: p.Config.Git.RemoteOriginURL,
		})
		return nil
	})
	if err != nil {
		return err
	}

	if len(rows) == 0 {
		fmt.Println("No projects found.")
		return nil
	}

	w := newTable()
	fmt.Fprintln(w, "NAME\tPATH\tGIT REPO\tGIT REMOTE")
	for _, r := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", r.name, r.path, r.gitRepo, r.gitRemote)
	}
	w.Flush()

	return nil
}
