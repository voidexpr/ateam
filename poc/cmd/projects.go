package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

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

	start := filepath.Dir(orgDir)

	type projectInfo struct {
		name      string
		path      string
		sourceDir string
		gitRepo   string
		gitRemote string
	}

	var projects []projectInfo

	_ = filepath.WalkDir(start, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && d.Name() == root.OrgDirName {
			return filepath.SkipDir
		}
		if d.IsDir() && d.Name() == root.ProjectDirName {
			cfg, loadErr := config.Load(path)
			if loadErr != nil {
				return filepath.SkipDir
			}
			projPath := filepath.Dir(path)
			relPath, _ := filepath.Rel(start, projPath)
			if relPath == "" {
				relPath = "."
			}
			projects = append(projects, projectInfo{
				name:      cfg.Project.Name,
				path:      relPath,
				sourceDir: cfg.Project.Source,
				gitRepo:   cfg.Git.Repo,
				gitRemote: cfg.Git.RemoteOriginURL,
			})
			return filepath.SkipDir
		}
		return nil
	})

	if len(projects) == 0 {
		fmt.Println("No projects found.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "PROJECT\tPATH\tSOURCE DIR\tGIT REPO DIR\tGIT REMOTE")
	for _, p := range projects {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", p.name, p.path, p.sourceDir, p.gitRepo, p.gitRemote)
	}
	w.Flush()

	return nil
}
