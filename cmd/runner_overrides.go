package cmd

import (
	"fmt"
	"strings"

	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
)

// shellQuoteSingle returns s wrapped in POSIX shell single quotes. Single
// quotes inside s are escaped as `'\”`. Use when injecting filesystem paths
// (which may contain spaces or other shell-significant chars) into prompts
// the supervisor templates into shell commands.
func shellQuoteSingle(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// requireGitRepo asserts that env.WorkDir is inside a git repo or worktree.
// Called from the runE of report/code/review/verify/all *after* resolveEnv
// has applied --work-dir and the project-aware promotion policy, so the
// check validates the same path the runner will actually use. PreRunE was
// the wrong layer: it validated the pre-promotion cwd, which could pass for
// a subdir-that-is-its-own-repo while the post-promotion WorkDir landed
// outside any repo.
func requireGitRepo(env *root.ResolvedEnv, action string) error {
	if env.GitRepoDir != "" {
		return nil
	}
	return fmt.Errorf("%s requires the work directory to be inside a git repo or worktree; %q is not in one. Run from inside a repo or pass --work-dir <repo-path>", action, env.WorkDir)
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
