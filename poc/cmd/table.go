package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/ateam-poc/internal/agent"
	"github.com/ateam-poc/internal/root"
	"github.com/ateam-poc/internal/runner"
	"github.com/ateam-poc/internal/runtime"
	"github.com/spf13/cobra"
)

func newTable() *tabwriter.Writer {
	return tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
}

func relPath(cwd, path string) string {
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

// newRunner creates a Runner using the resolved profile from runtime.hcl.
func newRunner(env *root.ResolvedEnv, profileName string) (*runner.Runner, error) {
	rtCfg, err := runtime.Load(env.ProjectDir, env.OrgDir)
	if err != nil {
		return nil, fmt.Errorf("cannot load runtime.hcl: %w", err)
	}

	_, ac, _, err := rtCfg.ResolveProfile(profileName)
	if err != nil {
		return nil, err
	}

	ag := buildAgent(ac)

	return &runner.Runner{
		Agent:          ag,
		LogFile:        env.RunnerLogPath(),
		ProjectDir:     env.ProjectDir,
		OrgDir:         env.OrgDir,
		ExtraWriteDirs: []string{env.OrgDir},
	}, nil
}

// newRunnerDefault creates a Runner using the default profile.
func newRunnerDefault(env *root.ResolvedEnv) (*runner.Runner, error) {
	profileName := env.Config.ResolveProfile("", "")
	return newRunner(env, profileName)
}

// buildAgent constructs an agent.Agent from config.
func buildAgent(ac *runtime.AgentConfig) agent.Agent {
	if ac.Type == "builtin" {
		return &agent.MockAgent{}
	}
	cmd := ac.Command
	if cmd == "" {
		cmd = ac.Name
	}
	ca := &agent.ClaudeAgent{
		Command: cmd,
		Args:    ac.Args,
		Model:   ac.Model,
	}
	return ca
}

const cheaperModelName = "sonnet"

func addCheaperModelFlag(cmd *cobra.Command, dst *bool) {
	cmd.Flags().BoolVar(dst, "cheaper-model", false, "use a cheaper model ("+cheaperModelName+")")
}

func applyCheaperModel(r *runner.Runner, cheaper bool) {
	if cheaper {
		r.ExtraArgs = append(r.ExtraArgs, "--model", cheaperModelName)
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
