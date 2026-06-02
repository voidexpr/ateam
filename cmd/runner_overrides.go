package cmd

import (
	"fmt"
	"strings"

	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/spf13/cobra"
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
func applyRunnerOverrides(r *runner.AgentExecutor, env *root.ResolvedEnv, o RunnerOverrides, action string) error {
	if err := applyContainerName(r, env, o.ContainerName); err != nil {
		return err
	}
	applyModelOverrides(r, o.CheaperModel, o.Model)
	applyEffort(r, o.Effort)
	return applyMaxBudgetUSD(r, o.MaxBudgetUSD, action)
}

// CommonExecFlags bundles the flag fields shared by code/report/review/
// verify. Embed anonymously in each command's Options struct so callers
// can access fields directly (e.g. opts.Profile) and adding a new shared
// flag means one struct change instead of editing every per-command
// Options.
type CommonExecFlags struct {
	PrePrompt       string
	PostPrompt      string
	Timeout         int
	CheaperModel    bool
	Profile         string
	Agent           string
	Verbose         bool
	DockerAutoSetup bool
	ContainerName   string
	Model           string
	Effort          string
	MaxBudgetUSD    string
}

// commonFlagUsage carries the per-command usage strings for the flags
// registered by registerCommonExecFlags whose wording legitimately
// varies between cmds (timeout scope, model scope, budget scope).
// The two prompt-wrap flags (--pre-prompt, --post-prompt) are NOT here
// — those use the shared constants from prompt_wrap_flags.go so every
// cmd describes them identically.
//
// CustomProfile / CustomAgent: when both empty, --profile and --agent are
// registered via the shared addProfileFlags helper (used by report, review,
// verify). When set, --profile and --agent are registered with the supplied
// usage strings and marked mutually exclusive (the code command needs its
// own sub-run-oriented help text).
type commonFlagUsage struct {
	Timeout       string
	Model         string
	Effort        string
	MaxBudgetUSD  string
	CustomProfile string
	CustomAgent   string
}

// registerCommonExecFlags registers the 13 CommonExecFlags fields as cobra
// flags on cmd. Callers register --max-budget-usd-batch (when applicable)
// and command-specific flags (--force, --dry-run, --print, etc.) separately;
// MaxBudgetBatch is intentionally not part of CommonExecFlags because not
// every command exposes it.
func registerCommonExecFlags(cmd *cobra.Command, f *CommonExecFlags, usage commonFlagUsage) {
	addPromptWrapFlags(cmd, &f.PrePrompt, &f.PostPrompt)
	cmd.Flags().IntVar(&f.Timeout, "timeout", 0, usage.Timeout)
	addCheaperModelFlag(cmd, &f.CheaperModel)
	if usage.CustomProfile != "" || usage.CustomAgent != "" {
		cmd.Flags().StringVar(&f.Profile, "profile", "", usage.CustomProfile)
		cmd.Flags().StringVar(&f.Agent, "agent", "", usage.CustomAgent)
		cmd.MarkFlagsMutuallyExclusive("profile", "agent")
	} else {
		addProfileFlags(cmd, &f.Profile, &f.Agent)
	}
	cmd.Flags().StringVar(&f.Model, "model", "", usage.Model)
	cmd.Flags().StringVar(&f.Effort, "effort", "", usage.Effort)
	addVerboseFlag(cmd, &f.Verbose)
	addDockerAutoSetupFlag(cmd, &f.DockerAutoSetup)
	addContainerNameFlag(cmd, &f.ContainerName)
	cmd.Flags().StringVar(&f.MaxBudgetUSD, "max-budget-usd", "", usage.MaxBudgetUSD)
}
