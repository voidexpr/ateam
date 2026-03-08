package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/ateam-poc/internal/root"
	"github.com/spf13/cobra"
)

var projectsCmd = &cobra.Command{
	Use:   "projects",
	Short: "List all projects under the current organization",
	Long: `Walk from the organization parent directory looking for .ateam/config.toml
files and print a summary table.

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

	orgParent := filepath.Dir(orgDir)

	type projectRow struct {
		name, path, source, gitRepo, gitRemote string
	}

	var rows []projectRow

	_ = root.WalkProjects(orgDir, func(p root.ProjectInfo) error {
		relPath, _ := filepath.Rel(orgParent, filepath.Dir(p.Dir))
		if relPath == "" {
			relPath = "."
		}
		rows = append(rows, projectRow{
			name:      p.Config.Project.Name,
			path:      relPath,
			source:    p.Config.Project.Source,
			gitRepo:   p.Config.Git.Repo,
			gitRemote: p.Config.Git.RemoteOriginURL,
		})
		return nil
	})

	if len(rows) == 0 {
		fmt.Println("No projects found.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "PROJECT\tPATH\tSOURCE DIR\tGIT REPO DIR\tGIT REMOTE")
	for _, r := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", r.name, r.path, r.source, r.gitRepo, r.gitRemote)
	}
	w.Flush()

	return nil
}
