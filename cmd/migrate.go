package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ateam/internal/root"
	"github.com/spf13/cobra"
)

var migrateDryRun bool

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Apply config migrations to the current project",
	Long: `Run all pending config migrations for the current project.

Migrations:
  - Remove redundant git.repo when it points to the project directory itself

Use --dry-run to preview changes without modifying files.

Example:
  ateam migrate
  ateam migrate --dry-run`,
	Args: cobra.NoArgs,
	RunE: runMigrate,
}

func init() {
	migrateCmd.Flags().BoolVar(&migrateDryRun, "dry-run", false, "preview changes without modifying files")
}

func runMigrate(cmd *cobra.Command, args []string) error {
	env, err := root.Resolve(orgFlag, projectFlag)
	if err != nil {
		return err
	}

	applied := 0

	if n, err := migrateGitRepo(env); err != nil {
		return err
	} else {
		applied += n
	}

	if applied == 0 {
		fmt.Println("Nothing to migrate.")
	}
	return nil
}

// migrateGitRepo removes git.repo from config.toml when it resolves to the
// project directory itself (redundant since that's the default).
func migrateGitRepo(env *root.ResolvedEnv) (int, error) {
	if env.Config == nil || env.Config.Git.Repo == "" {
		return 0, nil
	}

	// Check if the configured repo path matches what git actually reports.
	// If git toplevel IS the project dir, repo is redundant (and possibly wrong).
	gitTopLevel := execGitCmd(env.SourceDir, "rev-parse", "--show-toplevel")
	if gitTopLevel == "" {
		return 0, nil
	}

	if gitTopLevel == env.SourceDir {
		configPath := filepath.Join(env.ProjectDir, "config.toml")
		data, err := os.ReadFile(configPath)
		if err != nil {
			return 0, err
		}

		original := string(data)
		// Remove the repo line, preserving surrounding content
		re := regexp.MustCompile(`(?m)^\s*repo\s*=\s*"[^"]*"\s*\n`)
		updated := re.ReplaceAllString(original, "")

		if updated == original {
			return 0, nil
		}

		// Clean up empty [git] section if only remote_origin_url remains or section is empty
		updated = cleanEmptyGitSection(updated)

		fmt.Printf("migrate: remove redundant git.repo = %q from config.toml\n", env.Config.Git.Repo)
		if migrateDryRun {
			fmt.Println("  (dry-run, no changes made)")
			return 1, nil
		}

		if err := os.WriteFile(configPath, []byte(updated), 0644); err != nil {
			return 0, err
		}
		return 1, nil
	}

	return 0, nil
}

// cleanEmptyGitSection removes a [git] section that has no keys left,
// or removes the section header if only whitespace/comments remain before
// the next section.
func cleanEmptyGitSection(s string) string {
	// If [git] section only has empty lines until the next section or EOF, remove it
	re := regexp.MustCompile(`(?m)^\[git\]\s*\n(\s*\n)*(?:\[|$)`)
	if re.MatchString(s) {
		s = re.ReplaceAllStringFunc(s, func(match string) string {
			// Keep the next section header if present
			if idx := strings.LastIndex(match, "["); idx > 0 {
				return match[idx:]
			}
			return ""
		})
	}
	return s
}
