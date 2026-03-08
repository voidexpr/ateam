package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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

	fmt.Printf("    Name: %s\n", env.ProjectName)

	if env.GitRepoDir != "" {
		fmt.Printf("     Git: %s (%s)\n", env.RelPath(env.GitRepoDir), tildeHome(env.GitRepoDir))
	}
	if env.Config != nil && env.Config.Git.RemoteOriginURL != "" {
		fmt.Printf("  Remote: %s\n", env.Config.Git.RemoteOriginURL)
	}

	if env.Config != nil {
		agents := env.Config.EnabledAgents()
		if len(agents) > 0 {
			fmt.Printf("  Agents: %s\n", strings.Join(agents, ", "))

			fmt.Println()
			fmt.Printf("  %-25s %-20s %s\n", "AGENT", "LAST", "PATH")
			for _, agentID := range agents {
				reportPath := filepath.Join(env.ProjectDir, "agents", agentID, prompts.FullReportFile)
				printReportRow(reportPath, agentID, cwd)
			}
		}
	}

	reviewPath := filepath.Join(env.ProjectDir, "supervisor", "review.md")
	if fi, err := os.Stat(reviewPath); err == nil {
		relPath, _ := filepath.Rel(cwd, reviewPath)
		fmt.Printf("  %-25s %-20s %s\n", "review", formatDateAge(fi.ModTime()), relPath)
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

func printReportRow(path, label, cwd string) {
	fi, err := os.Stat(path)
	if err != nil {
		fmt.Printf("  %-25s -\n", label)
		return
	}
	relPath, _ := filepath.Rel(cwd, path)
	fmt.Printf("  %-25s %-20s %s\n", label, formatDateAge(fi.ModTime()), relPath)
}

func formatDateAge(t time.Time) string {
	date := t.Format("01/02")
	age := time.Since(t)
	switch {
	case age < time.Minute:
		return date + " (just now)"
	case age < time.Hour:
		return fmt.Sprintf("%s (%dm ago)", date, int(age.Minutes()))
	case age < 24*time.Hour:
		return fmt.Sprintf("%s (%dh ago)", date, int(age.Hours()))
	default:
		days := int(age.Hours()) / 24
		return fmt.Sprintf("%s (%dd ago)", date, days)
	}
}
