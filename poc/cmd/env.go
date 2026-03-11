package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ateam-poc/internal/prompts"
	"github.com/ateam-poc/internal/root"
	"github.com/spf13/cobra"
)

var envCmd = &cobra.Command{
	Use:   "env",
	Short: "Show the current ATeam environment",
	Long: `Print organization, project status, and latest report/review timestamps.

This command is read-only — it never creates or modifies anything.`,
	Args: cobra.NoArgs,
	RunE: runEnv,
}

func runEnv(cmd *cobra.Command, args []string) error {
	env, err := root.Lookup()
	if err != nil {
		return err
	}

	orgRoot := env.OrgRoot()
	cwd, err := resolvedCwd()
	if err != nil {
		return err
	}

	relOrg, _ := filepath.Rel(cwd, orgRoot)
	fmt.Printf("     Org: %s (%s)\n", relOrg, tildeHome(orgRoot))

	if env.ProjectDir == "" {
		return nil
	}

	fmt.Printf(" Project: %s\n", env.ProjectName)

	if env.StateDir != "" {
		fmt.Printf("   State: %s\n", env.RelPath(env.StateDir))
	}

	if env.GitRepoDir != "" {
		fmt.Printf("     Git: %s (%s)\n", env.RelPath(env.GitRepoDir), tildeHome(env.GitRepoDir))
	}
	if env.Config != nil && env.Config.Git.RemoteOriginURL != "" {
		fmt.Printf("  Remote: %s\n", env.Config.Git.RemoteOriginURL)
	}

	if env.Config != nil {
		roles := env.Config.EnabledRoles()
		if len(roles) > 0 {
			fmt.Printf("   Roles: %s\n", strings.Join(roles, ", "))

			fmt.Println()
			w := newTable()
			fmt.Fprintln(w, "ROLE\tLAST\tPATH")
			for _, roleID := range roles {
				reportPath := filepath.Join(env.ProjectDir, "roles", roleID, prompts.FullReportFile)
				if fi, err := os.Stat(reportPath); err == nil {
					fmt.Fprintf(w, "%s\t%s\t%s\n", roleID, fmtDateAge(fi.ModTime()), relPath(cwd, reportPath))
				} else {
					fmt.Fprintf(w, "%s\t-\t\n", roleID)
				}
			}

			reviewPath := filepath.Join(env.ProjectDir, "supervisor", "review.md")
			if fi, err := os.Stat(reviewPath); err == nil {
				fmt.Fprintf(w, "%s\t%s\t%s\n", "review", fmtDateAge(fi.ModTime()), relPath(cwd, reviewPath))
			}
			w.Flush()
		}
	}

	return nil
}

func tildeHome(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}

