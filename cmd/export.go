package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ateam/internal/config"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/web"
	"github.com/spf13/cobra"
)

var (
	exportOutput       string
	exportProjectName  string
	exportAteamProject string
)

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export project reports as a self-contained HTML file",
	Long: `Generate a single HTML file containing reports, review, and code execution report.

The file has three tabs (Overview, Review, Code) with anchor-based navigation,
so you can link directly to a section (e.g. ateam.html#review).

Example:
  ateam export
  ateam export --output report.html
  ateam export --project "My Project"
  ateam export --ateam-project /path/to/.ateam`,
	Args: cobra.NoArgs,
	RunE: runExport,
}

func init() {
	exportCmd.Flags().StringVar(&exportOutput, "output", "", "output file path (default: .ateam/ateam.html)")
	exportCmd.Flags().StringVar(&exportProjectName, "project", "", "display name to use instead of the project name from config")
	exportCmd.Flags().StringVar(&exportAteamProject, "ateam-project", "", "path to a .ateam/ project directory to export")
}

func runExport(cmd *cobra.Command, args []string) error {
	var env *root.ResolvedEnv
	var err error

	if exportAteamProject != "" {
		var absPath string
		absPath, err = filepath.Abs(exportAteamProject)
		if err != nil {
			return fmt.Errorf("resolving --ateam-project path: %w", err)
		}
		cfg, _ := config.Load(absPath)
		name := filepath.Base(filepath.Dir(absPath))
		if cfg != nil && cfg.Project.Name != "" {
			name = cfg.Project.Name
		}
		env = &root.ResolvedEnv{
			ProjectDir:  absPath,
			ProjectName: name,
			SourceDir:   filepath.Dir(absPath),
		}
	} else {
		env, err = root.Resolve(orgFlag, projectFlag)
		if err != nil {
			return err
		}
	}

	if env.ProjectDir == "" {
		return fmt.Errorf("export requires a project context -- use -p flag or run from a project directory")
	}

	srv, err := web.New(env)
	if err != nil {
		return err
	}
	defer srv.Close()

	opts := web.ExportOptions{}
	if exportProjectName != "" {
		opts.ProjectName = exportProjectName
	}

	html, err := srv.ExportHTML(opts)
	if err != nil {
		return err
	}

	outputPath := exportOutput
	if outputPath == "" {
		outputPath = filepath.Join(env.ProjectDir, "ateam.html")
	} else if !filepath.IsAbs(outputPath) && !strings.Contains(outputPath, string(filepath.Separator)) {
		outputPath = filepath.Join(env.ProjectDir, outputPath)
	}

	if err := os.WriteFile(outputPath, []byte(html), 0644); err != nil {
		return fmt.Errorf("writing export file: %w", err)
	}

	abs, _ := filepath.Abs(outputPath)
	fmt.Println(abs)
	return nil
}
