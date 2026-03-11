package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/ateam-poc/internal/root"
	"github.com/ateam-poc/internal/runner"
	"github.com/spf13/cobra"
)

func newTable() *tabwriter.Writer {
	return tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
}

func relPath(cwd, path string) string {
	// Resolve symlinks so both sides match (env paths are already resolved).
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}
	rel, err := filepath.Rel(cwd, path)
	if err != nil {
		return path
	}
	return rel
}

func fmtCost(cost float64) string {
	if cost <= 0 {
		return ""
	}
	return fmt.Sprintf("$%.2f", cost)
}

func printDone(r runner.RunSummary) {
	costSuffix := ""
	if c := fmtCost(r.Cost); c != "" {
		costSuffix = ", " + c
	}
	fmt.Printf("Done (%s%s)\n\n", runner.FormatDuration(r.Duration), costSuffix)
}

func fmtInt(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf("%d", n)
}

func newClaudeRunner(env *root.ResolvedEnv) *runner.ClaudeRunner {
	return &runner.ClaudeRunner{
		LogFile:        env.RunnerLogPath(),
		ProjectDir:     env.ProjectDir,
		OrgDir:         env.OrgDir,
		ExtraWriteDirs: []string{env.OrgDir},
	}
}

const cheaperModelName = "sonnet"

func addCheaperModelFlag(cmd *cobra.Command, dst *bool) {
	cmd.Flags().BoolVar(dst, "cheaper-model", false, "use a cheaper model ("+cheaperModelName+")")
}

func applyCheaperModel(cr *runner.ClaudeRunner, cheaper bool) {
	if cheaper {
		cr.ExtraArgs = append(cr.ExtraArgs, "--model", cheaperModelName)
	}
}

func fmtDateAge(t time.Time) string {
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
