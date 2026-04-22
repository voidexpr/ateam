package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ateam/internal/eval"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/spf13/cobra"
)

var (
	evalRole            string
	evalPromptArg       string
	evalBaseArg         string
	evalDirs            []string
	evalTimeout         int
	evalVerbose         bool
	evalNoJudge         bool
	evalJudgeTimeout    int
	evalProfile         string
	evalAgent           string
	evalModel           string
	evalBaseProfile     string
	evalBaseAgent       string
	evalBaseModel       string
	evalCandProfile     string
	evalCandAgent       string
	evalCandModel       string
	evalJudgeProfile    string
	evalJudgeAgent      string
	evalJudgeModel      string
	evalDockerAutoSetup bool
	evalGitWorktree     bool
	evalGitWorktreeBase string
)

var evalCmd = &cobra.Command{
	Use:   "eval",
	Short: "Compare two prompts for a role (cost + LLM judge)",
	Long: `Run a role twice with two prompts (base and candidate) against the same
codebase and compare the results: cost, tokens, duration, plus an LLM judge
that scores each report 0.00-1.00 on coverage, accuracy, actionability, and
conciseness.

Modes:
  Sequential (default): runs in the current project directory, one after the other.
  --dirs DIR1,DIR2:     user provides two initialized ateam project dirs (e.g. two
                        git worktrees, two clones, or different projects). Parallel.
  --git-worktree:       ateam auto-creates two detached git worktrees (under
                        /tmp/ateam-worktree/<project> by default) and copies the
                        parent's .ateam/ minus state into each. Parallel. Errors
                        if the source repo has uncommitted changes.

The previous-report context is always skipped so both runs start from the same state.

Agent selection:
  --profile/--agent/--model apply to both sides unless overridden by
  --base-profile/--base-agent/--base-model or
  --candidate-profile/--candidate-agent/--candidate-model.
  The judge uses --judge-profile/--judge-agent/--judge-model (falling back to
  --profile/--agent/--model, then config default).

Examples:
  ateam eval --role security --prompt @candidate.md
  ateam eval --role security --prompt @candidate.md --base @old.md --model sonnet
  ateam eval --role security --prompt @candidate.md --dirs . ../eval-worktree
  ateam eval --role security --prompt @candidate.md --git-worktree
  ateam eval --role security --prompt @candidate.md --candidate-model haiku --judge-model sonnet`,
	RunE: runEval,
}

func init() {
	evalCmd.Flags().StringVar(&evalRole, "role", "", "role to evaluate (required)")
	evalCmd.Flags().StringVar(&evalPromptArg, "prompt", "", "candidate prompt text or @filepath (required)")
	evalCmd.Flags().StringVar(&evalBaseArg, "base", "", "base prompt text or @filepath (default: current on-disk prompt)")
	evalCmd.Flags().StringSliceVar(&evalDirs, "dirs", nil, "two project dirs for parallel mode: DIR_BASE,DIR_CANDIDATE")
	evalCmd.Flags().BoolVar(&evalGitWorktree, "git-worktree", false, "auto-create detached git worktrees for parallel eval (see --git-worktree-base)")
	evalCmd.Flags().StringVar(&evalGitWorktreeBase, "git-worktree-base", "", "base dir for auto-created worktrees (default: /tmp/ateam-worktree/<project>)")
	evalCmd.Flags().IntVar(&evalTimeout, "timeout", 0, "timeout in minutes per run (0 = config default)")
	evalCmd.Flags().IntVar(&evalJudgeTimeout, "judge-timeout", 10, "timeout in minutes for the judge run")
	evalCmd.Flags().BoolVar(&evalNoJudge, "no-judge", false, "skip the LLM judge step (cost comparison only)")
	evalCmd.Flags().BoolVar(&evalVerbose, "verbose", false, "print agent and container commands")

	evalCmd.Flags().StringVar(&evalProfile, "profile", "", "runtime profile for both sides (overridden by --base-profile / --candidate-profile)")
	evalCmd.Flags().StringVar(&evalAgent, "agent", "", "agent for both sides (mutually exclusive with --profile)")
	evalCmd.Flags().StringVar(&evalModel, "model", "", "model override for both sides")

	evalCmd.Flags().StringVar(&evalBaseProfile, "base-profile", "", "profile for the base side only")
	evalCmd.Flags().StringVar(&evalBaseAgent, "base-agent", "", "agent for the base side only")
	evalCmd.Flags().StringVar(&evalBaseModel, "base-model", "", "model for the base side only")

	evalCmd.Flags().StringVar(&evalCandProfile, "candidate-profile", "", "profile for the candidate side only")
	evalCmd.Flags().StringVar(&evalCandAgent, "candidate-agent", "", "agent for the candidate side only")
	evalCmd.Flags().StringVar(&evalCandModel, "candidate-model", "", "model for the candidate side only")

	evalCmd.Flags().StringVar(&evalJudgeProfile, "judge-profile", "", "profile for the judge (default: --profile)")
	evalCmd.Flags().StringVar(&evalJudgeAgent, "judge-agent", "", "agent for the judge (default: --agent)")
	evalCmd.Flags().StringVar(&evalJudgeModel, "judge-model", "", "model for the judge (default: --model)")

	evalCmd.MarkFlagsMutuallyExclusive("profile", "agent")
	evalCmd.MarkFlagsMutuallyExclusive("base-profile", "base-agent")
	evalCmd.MarkFlagsMutuallyExclusive("candidate-profile", "candidate-agent")
	evalCmd.MarkFlagsMutuallyExclusive("judge-profile", "judge-agent")
	evalCmd.MarkFlagsMutuallyExclusive("dirs", "git-worktree")

	addDockerAutoSetupFlag(evalCmd, &evalDockerAutoSetup)

	_ = evalCmd.MarkFlagRequired("role")
	_ = evalCmd.MarkFlagRequired("prompt")
}

func runEval(cmd *cobra.Command, args []string) error {
	candidatePrompt, err := prompts.ResolveValue(evalPromptArg)
	if err != nil {
		return fmt.Errorf("cannot resolve candidate prompt: %w", err)
	}
	basePrompt, err := prompts.ResolveOptional(evalBaseArg)
	if err != nil {
		return fmt.Errorf("cannot resolve base prompt: %w", err)
	}

	parallel := len(evalDirs) > 0 || evalGitWorktree
	if len(evalDirs) > 0 && len(evalDirs) != 2 {
		return fmt.Errorf("--dirs requires exactly two paths")
	}

	var baseEnv, candEnv *root.ResolvedEnv
	if evalGitWorktree {
		sourceEnv, err := root.Resolve(orgFlag, projectFlag)
		if err != nil {
			return err
		}
		if sourceEnv.ProjectDir == "" || sourceEnv.Config == nil {
			return fmt.Errorf("--git-worktree requires an ateam project in the current directory")
		}
		fmt.Println("Setting up git worktrees...")
		baseEnv, candEnv, err = eval.SetupWorktrees(sourceEnv, evalGitWorktreeBase)
		if err != nil {
			return err
		}
		fmt.Printf("  base:      %s\n  candidate: %s\n\n", baseEnv.SourceDir, candEnv.SourceDir)
	} else {
		baseEnv, candEnv, err = resolveEvalEnvs(evalDirs)
		if err != nil {
			return err
		}
	}

	if !prompts.IsValidRole(evalRole, baseEnv.Config.Roles, baseEnv.ProjectDir, baseEnv.OrgDir) {
		return fmt.Errorf("unknown role %q in %s", evalRole, baseEnv.ProjectDir)
	}
	if parallel && !prompts.IsValidRole(evalRole, candEnv.Config.Roles, candEnv.ProjectDir, candEnv.OrgDir) {
		return fmt.Errorf("unknown role %q in %s", evalRole, candEnv.ProjectDir)
	}

	baseRunner, err := buildEvalRunner(evalRunnerSpec{
		env: baseEnv, action: runner.ActionReport, roleID: evalRole,
		scopeProfile: evalBaseProfile, scopeAgent: evalBaseAgent, scopeModel: evalBaseModel,
		sharedProfile: evalProfile, sharedAgent: evalAgent, sharedModel: evalModel,
	})
	if err != nil {
		return fmt.Errorf("base runner: %w", err)
	}
	candRunner, err := buildEvalRunner(evalRunnerSpec{
		env: candEnv, action: runner.ActionReport, roleID: evalRole,
		scopeProfile: evalCandProfile, scopeAgent: evalCandAgent, scopeModel: evalCandModel,
		sharedProfile: evalProfile, sharedAgent: evalAgent, sharedModel: evalModel,
	})
	if err != nil {
		return fmt.Errorf("candidate runner: %w", err)
	}

	baseDB, err := openProjectDB(baseEnv)
	if err != nil {
		return err
	}
	defer baseDB.Close()
	baseRunner.CallDB = baseDB

	candDB := baseDB
	if parallel {
		candDB, err = openProjectDB(candEnv)
		if err != nil {
			return err
		}
		defer candDB.Close()
	}
	candRunner.CallDB = candDB

	timeout := evalTimeout
	if timeout == 0 {
		timeout = baseEnv.Config.Report.EffectiveTimeout(0)
	}

	ctx, stop := cmdContext()
	defer stop()

	base := eval.Variant{
		Label: eval.SideBase, PromptText: basePrompt, Runner: baseRunner, Env: baseEnv,
	}
	cand := eval.Variant{
		Label: eval.SideCandidate, PromptText: candidatePrompt, Runner: candRunner, Env: candEnv,
	}
	if parallel {
		base.Dir = baseEnv.ProjectDir
		cand.Dir = candEnv.ProjectDir
	}

	fmt.Printf("Running eval for role %q (%s mode, %dm timeout)...\n\n",
		evalRole, modeLabel(parallel), timeout)

	baseResult, candResult, runErr := eval.RunEval(ctx, evalRole, base, cand, timeout, evalVerbose)
	if runErr != nil {
		if baseResult == nil || candResult == nil {
			return runErr
		}
		fmt.Fprintf(os.Stderr, "Warning: %v\n\n", runErr)
	}

	var judge *eval.JudgeResult
	if !evalNoJudge && baseResult != nil && candResult != nil && baseResult.Report != "" && candResult.Report != "" {
		judgeRunner, err := buildEvalRunner(evalRunnerSpec{
			env: baseEnv, action: runner.ActionRun,
			scopeProfile: evalJudgeProfile, scopeAgent: evalJudgeAgent, scopeModel: evalJudgeModel,
			sharedProfile: evalProfile, sharedAgent: evalAgent, sharedModel: evalModel,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: cannot build judge runner (%v); skipping judge\n", err)
		} else {
			judgeRunner.CallDB = baseDB
			fmt.Println("Running judge...")
			judge, err = eval.RunJudge(ctx, judgeRunner, baseEnv, evalRole, baseResult.Report, candResult.Report, evalJudgeTimeout, evalVerbose)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: judge failed: %v\n", err)
				judge = nil
			}
			fmt.Println()
		}
	}

	eval.PrintComparison(os.Stdout, evalRole, baseResult, candResult, judge)
	return nil
}

// resolveEvalEnvs returns (baseEnv, candEnv). If dirs is empty, both point to
// the current project resolved from the cwd. If dirs is set, each is resolved
// from its directory.
func resolveEvalEnvs(dirs []string) (*root.ResolvedEnv, *root.ResolvedEnv, error) {
	if len(dirs) == 0 {
		env, err := root.Resolve(orgFlag, projectFlag)
		if err != nil {
			return nil, nil, err
		}
		return env, env, nil
	}
	baseDir, err := filepath.Abs(dirs[0])
	if err != nil {
		return nil, nil, fmt.Errorf("--dirs[0]: %w", err)
	}
	candDir, err := filepath.Abs(dirs[1])
	if err != nil {
		return nil, nil, fmt.Errorf("--dirs[1]: %w", err)
	}
	baseEnv, err := root.LookupFrom(baseDir)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve %s: %w", baseDir, err)
	}
	if baseEnv.ProjectDir == "" || baseEnv.Config == nil {
		return nil, nil, fmt.Errorf("%s is not an ateam project (no .ateam/)", baseDir)
	}
	candEnv, err := root.LookupFrom(candDir)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve %s: %w", candDir, err)
	}
	if candEnv.ProjectDir == "" || candEnv.Config == nil {
		return nil, nil, fmt.Errorf("%s is not an ateam project (no .ateam/)", candDir)
	}
	return baseEnv, candEnv, nil
}

// evalRunnerSpec groups per-scope (base/candidate/judge) runner options with
// fallback to shared flags.
type evalRunnerSpec struct {
	env                        *root.ResolvedEnv
	action                     string
	roleID                     string
	scopeProfile, scopeAgent   string
	sharedProfile, sharedAgent string
	scopeModel, sharedModel    string
}

// buildEvalRunner resolves a Runner using scope-specific flags, falling back
// to shared flags when unset, then applies any model override.
func buildEvalRunner(spec evalRunnerSpec) (*runner.Runner, error) {
	profile, agent := spec.scopeProfile, spec.scopeAgent
	if profile == "" && agent == "" {
		profile, agent = spec.sharedProfile, spec.sharedAgent
	}
	r, err := resolveRunner(spec.env, profile, agent, spec.action, spec.roleID, evalDockerAutoSetup)
	if err != nil {
		return nil, err
	}
	model := spec.scopeModel
	if model == "" {
		model = spec.sharedModel
	}
	if model != "" {
		r.Agent.SetModel(model)
	}
	return r, nil
}

func modeLabel(parallel bool) string {
	if parallel {
		return "parallel"
	}
	return "sequential"
}
