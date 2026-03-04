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
	Long: `Print .ateam location, project status, and latest report/review timestamps.

This command is read-only — it never creates or modifies anything.`,
	Args: cobra.NoArgs,
	RunE: runEnv,
}

func runEnv(cmd *cobra.Command, args []string) error {
	info, err := root.Lookup()
	if err != nil {
		return err
	}

	cwd, _ := os.Getwd()
	relFromCwd, _ := filepath.Rel(cwd, info.AteamRoot)
	fmt.Printf("ateam root:  %s  (from cwd: %s)\n", info.AteamRoot, relFromCwd)

	if info.InsideAteam {
		fmt.Printf("execute:     in .ateam project\n")
	} else {
		fmt.Printf("execute:     in source project\n")
	}

	ateamParent := filepath.Dir(info.AteamRoot)
	if info.SourceGit != "" {
		rel, _ := filepath.Rel(ateamParent, info.SourceGit)
		fmt.Printf("source git:  %s\n", rel)
	}

	if info.ProjectDir == "" {
		fmt.Printf("project:     (not initialized)\n")
		return nil
	}

	fmt.Printf("project:     %s\n", info.ProjectRelPath)
	relProjDir, _ := filepath.Rel(info.AteamRoot, info.ProjectDir)
	fmt.Printf("project dir: %s\n", relProjDir)
	fmt.Printf("agents:      %s\n", strings.Join(info.Agents, ", "))

	if len(info.Agents) > 0 {
		fmt.Println()
		fmt.Println("Reports:")
		for _, agentID := range info.Agents {
			reportPath := filepath.Join(info.ProjectDir, "agents", agentID, prompts.FullReportFile)
			printFileAge(reportPath, agentID, info.AteamRoot)
		}
	}

	reviewPath := filepath.Join(info.ProjectDir, "supervisor", "review.md")
	if fi, err := os.Stat(reviewPath); err == nil {
		fmt.Println()
		rel, _ := filepath.Rel(info.AteamRoot, reviewPath)
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
