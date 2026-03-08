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

var envAbsolute bool

var envCmd = &cobra.Command{
	Use:   "env",
	Short: "Show the current ATeam environment",
	Long: `Print organization, project status, and latest report/review timestamps.

This command is read-only — it never creates or modifies anything.

Use --absolute to show fully resolved paths instead of relative ones.`,
	Args: cobra.NoArgs,
	RunE: runEnv,
}

func init() {
	envCmd.Flags().BoolVar(&envAbsolute, "absolute", false, "show absolute paths instead of relative")
}

func runEnv(cmd *cobra.Command, args []string) error {
	env, err := root.Lookup()
	if err != nil {
		return err
	}

	displayPath := env.RelPath
	if envAbsolute {
		displayPath = func(p string) string { return p }
	}

	fmt.Printf("     Org: %s\n", displayPath(env.OrgRoot()))

	if env.ProjectDir == "" {
		fmt.Printf(" Project: (not initialized)\n")
		return nil
	}

	fmt.Printf(" Project: %s\n", env.ProjectName)

	if env.SourceDir != "" {
		fmt.Printf("  Source: %s\n", displayPath(env.SourceDir))
	}
	if env.GitRepoDir != "" {
		fmt.Printf("     Git: %s\n", displayPath(env.GitRepoDir))
	}
	if env.Config != nil && env.Config.Git.RemoteOriginURL != "" {
		fmt.Printf("  Remote: %s\n", env.Config.Git.RemoteOriginURL)
	}

	if env.Config != nil {
		agents := env.Config.EnabledAgents()
		if len(agents) > 0 {
			fmt.Printf("  Agents: %s\n", strings.Join(agents, ", "))

			fmt.Println()
			fmt.Println("Reports:")
			for _, agentID := range agents {
				reportPath := filepath.Join(env.ProjectDir, "agents", agentID, prompts.FullReportFile)
				printFileAge(reportPath, agentID, env.ProjectDir)
			}
		}
	}

	reviewPath := filepath.Join(env.ProjectDir, "supervisor", "review.md")
	if fi, err := os.Stat(reviewPath); err == nil {
		fmt.Println()
		rel, _ := filepath.Rel(env.ProjectDir, reviewPath)
		fmt.Printf("Review:  %s  (%s)\n", rel, formatAge(fi.ModTime()))
	}

	return nil
}

func printFileAge(path, label, relativeTo string) {
	fi, err := os.Stat(path)
	if err != nil {
		fmt.Printf("  %-25s (no report)\n", label)
		return
	}
	rel, _ := filepath.Rel(relativeTo, path)
	fmt.Printf("  %-25s %s  %s\n", label, formatAge(fi.ModTime()), rel)
}

func formatAge(t time.Time) string {
	age := time.Since(t)
	switch {
	case age < time.Minute:
		return "just now"
	case age < time.Hour:
		return fmt.Sprintf("%dm ago", int(age.Minutes()))
	case age < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(age.Hours()))
	default:
		return t.Format("2006-01-02 15:04")
	}
}
