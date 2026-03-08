package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ateam-poc/internal/config"
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

	orgRoot := filepath.Dir(orgDir)

	orgCfg, err := config.LoadOrgConfig(orgDir)
	if err != nil {
		return err
	}

	type projectRow struct {
		name, path, uuid, gitRepo, gitRemote string
	}

	var rows []projectRow

	for uuid, relPath := range orgCfg.Projects {
		projectDir := filepath.Join(orgRoot, relPath, root.ProjectDirName)
		cfg, loadErr := config.Load(projectDir)
		if loadErr != nil {
			continue
		}
		rows = append(rows, projectRow{
			name:      cfg.Project.Name,
			path:      relPath,
			uuid:      uuid,
			gitRepo:   cfg.Git.Repo,
			gitRemote: cfg.Git.RemoteOriginURL,
		})
	}

	if len(rows) == 0 {
		fmt.Println("No projects found.")
		return nil
	}

	w := newTable()
	fmt.Fprintln(w, "NAME\tPATH\tUUID\tGIT REPO\tGIT REMOTE")
	for _, r := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", r.name, r.path, r.uuid, r.gitRepo, r.gitRemote)
	}
	w.Flush()

	return nil
}
