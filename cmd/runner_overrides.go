package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ateam/internal/gitutil"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/spf13/cobra"
)

// requireGitRepoPreRunE is the cobra PreRunE for action commands (report,
// code, review, verify, all) that require their work-dir to be inside a git
// repo or worktree. exec/parallel skip this check by design — they are the
// "run anywhere" commands.
//
// The check resolves --work-dir (or os.Getwd()) and runs gitutil.TopLevel.
// Missing git CLI is treated the same as "not a repo" — both fail closed.
func requireGitRepoPreRunE(cmd *cobra.Command, _ []string) error {
	workDir := workDirFlag
	if workDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("cannot determine current directory: %w", err)
		}
		workDir = cwd
	} else {
		abs, err := filepath.Abs(workDir)
		if err != nil {
			return fmt.Errorf("cannot resolve --work-dir: %w", err)
		}
		workDir = abs
	}
	if gitutil.TopLevel(workDir) == "" {
		return fmt.Errorf("%s requires the work directory to be inside a git repo or worktree; %q is not in one. Run from inside a repo or pass --work-dir <repo-path>", cmd.Name(), workDir)
	}
	return nil
}

// resolveWorkDir reconciles the persistent --work-dir flag with env.WorkDir
// and returns the path to use as the agent's cwd. When flag is set, env.WorkDir
// is overridden (and env.GitRepoDir is re-derived) so downstream consumers see
// a consistent view.
func resolveWorkDir(flag string, env *root.ResolvedEnv) (string, error) {
	if flag != "" {
		if err := env.OverrideWorkDir(flag); err != nil {
			return "", err
		}
	}
	return env.WorkDir, nil
}

// RunnerOverrides bundles every CLI-flag override that flows uniformly into
// runner setup across commands. Apply with applyRunnerOverrides; commands that
// need access to MaxBudgetUSDBatch (for batch precheck) can read it directly.
type RunnerOverrides struct {
	ContainerName     string
	CheaperModel      bool
	Model             string
	Effort            string
	MaxBudgetUSD      string
	MaxBudgetUSDBatch string
}

// applyRunnerOverrides runs the per-flag apply* helpers in the order previously
// duplicated across cmd/{code,exec,parallel,report,review,verify}.go. action is
// forwarded to applyMaxBudgetUSD for action-specific gating.
func applyRunnerOverrides(r *runner.Runner, env *root.ResolvedEnv, o RunnerOverrides, action string) error {
	if err := applyContainerName(r, env, o.ContainerName); err != nil {
		return err
	}
	applyModelOverrides(r, o.CheaperModel, o.Model)
	applyEffort(r, o.Effort)
	return applyMaxBudgetUSD(r, o.MaxBudgetUSD, action)
}
