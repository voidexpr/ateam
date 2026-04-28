package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ateam/internal/eval"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/spf13/cobra"
)

var (
	evalRole            string
	evalBaseRoles       []string
	evalCandRoles       []string
	evalPromptArg       string
	evalBaseArg         string
	evalDirs            []string
	evalReview          bool
	evalReviewBaseArg   string
	evalReviewCandArg   string
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
	evalForce           bool
)

var evalCmd = &cobra.Command{
	Use:   "eval",
	Short: "Compare two prompts for a role (cost + LLM judge)",
	Long: `Run one or more roles per side (base and candidate) against the same codebase
and compare the results: cost, tokens, duration, plus an LLM judge that scores
each side 0.00-1.00 on coverage, accuracy, actionability, and conciseness.

Roles:
  --role X                     shorthand: both sides run [X]
  --base-roles A,B / --candidate-roles C,D   different role sets per side
                               (use to compare N reports vs M, e.g. consolidating)

Prompt overrides (only valid when the matching side has exactly one role):
  --prompt @file               candidate report prompt
  --base   @file               base report prompt (default: on-disk)

Review:
  --review                     also run supervisor review per side after the
                               role reports; the judge then compares reviews
                               instead of reports
  --review-base-prompt @file   override the base side's review prompt
  --review-candidate-prompt @file   override the candidate side's review prompt

Modes:
  Sequential (default): runs in the current project directory, one after the other.
  --dirs DIR1,DIR2:     user provides two initialized ateam project dirs.
  --git-worktree:       ateam auto-creates two detached git worktrees and copies
                        the parent's .ateam/ minus state into each. Parallel.

The previous-report context is always skipped so both runs start fresh.

Agent selection:
  --profile/--agent/--model apply to both sides unless overridden by
  --base-* / --candidate-*. Judge uses --judge-* (falling back to shared).

Examples:
  ateam eval --role security --prompt @candidate.md
  ateam eval --role security --prompt @candidate.md --git-worktree
  ateam eval --base-roles code.small,code.module --candidate-roles code.consolidated --review
  ateam eval --role security --review --review-candidate-prompt @new_review.md`,
	RunE: runEval,
}

func init() {
	evalCmd.Flags().StringVar(&evalRole, "role", "", "role to evaluate on both sides (shorthand for --base-roles X --candidate-roles X)")
	evalCmd.Flags().StringSliceVar(&evalBaseRoles, "base-roles", nil, "comma-separated roles for the base side (defaults to --role)")
	evalCmd.Flags().StringSliceVar(&evalCandRoles, "candidate-roles", nil, "comma-separated roles for the candidate side (defaults to --role)")
	evalCmd.Flags().StringVar(&evalPromptArg, "prompt", "", "candidate report prompt text or @filepath (requires single candidate role)")
	evalCmd.Flags().StringVar(&evalBaseArg, "base", "", "base report prompt text or @filepath (default: current on-disk prompt; requires single base role)")
	evalCmd.Flags().BoolVar(&evalReview, "review", false, "also run supervisor review per side; judge compares reviews instead of reports")
	evalCmd.Flags().StringVar(&evalReviewBaseArg, "review-base-prompt", "", "override base review prompt (text or @filepath; implies --review)")
	evalCmd.Flags().StringVar(&evalReviewCandArg, "review-candidate-prompt", "", "override candidate review prompt (text or @filepath; implies --review)")
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
	addForceFlag(evalCmd, &evalForce)
}

func runEval(cmd *cobra.Command, args []string) error {
	baseRoles, candRoles, err := resolveEvalRoles()
	if err != nil {
		return err
	}

	candidatePrompt, err := prompts.ResolveOptional(evalPromptArg)
	if err != nil {
		return fmt.Errorf("cannot resolve candidate prompt: %w", err)
	}
	basePrompt, err := prompts.ResolveOptional(evalBaseArg)
	if err != nil {
		return fmt.Errorf("cannot resolve base prompt: %w", err)
	}
	if candidatePrompt != "" && len(candRoles) != 1 {
		return fmt.Errorf("--prompt requires exactly one candidate role; got %d", len(candRoles))
	}
	if basePrompt != "" && len(baseRoles) != 1 {
		return fmt.Errorf("--base requires exactly one base role; got %d", len(baseRoles))
	}

	reviewBasePrompt, err := prompts.ResolveOptional(evalReviewBaseArg)
	if err != nil {
		return fmt.Errorf("cannot resolve --review-base-prompt: %w", err)
	}
	reviewCandPrompt, err := prompts.ResolveOptional(evalReviewCandArg)
	if err != nil {
		return fmt.Errorf("cannot resolve --review-candidate-prompt: %w", err)
	}
	doReview := evalReview || reviewBasePrompt != "" || reviewCandPrompt != ""

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

	if err := validateRoles(baseRoles, baseEnv); err != nil {
		return fmt.Errorf("base side: %w", err)
	}
	if parallel {
		if err := validateRoles(candRoles, candEnv); err != nil {
			return fmt.Errorf("candidate side: %w", err)
		}
	} else if err := validateRoles(candRoles, baseEnv); err != nil {
		return fmt.Errorf("candidate side: %w", err)
	}

	// Pick a representative role for runner profile resolution: when a side
	// has multiple roles, profile may be role-specific in config; we use the
	// first role's profile for the whole side. Workable for v1.
	baseRunnerRole := baseRoles[0]
	candRunnerRole := candRoles[0]

	baseRunner, err := buildEvalRunner(evalRunnerSpec{
		env: baseEnv, action: runner.ActionReport, roleID: baseRunnerRole,
		scopeProfile: evalBaseProfile, scopeAgent: evalBaseAgent, scopeModel: evalBaseModel,
		sharedProfile: evalProfile, sharedAgent: evalAgent, sharedModel: evalModel,
	})
	if err != nil {
		return fmt.Errorf("base runner: %w", err)
	}
	candRunner, err := buildEvalRunner(evalRunnerSpec{
		env: candEnv, action: runner.ActionReport, roleID: candRunnerRole,
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

	if !evalForce {
		if err := checkConcurrentRunsEnv(baseDB, baseEnv, runner.ActionReport, baseRoles); err != nil {
			return err
		}
	}

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
		Label: eval.SideBase, Roles: rolesToRoleRuns(baseRoles, basePrompt),
		Runner: baseRunner, Env: baseEnv,
		RunReview: doReview, ReviewPromptText: reviewBasePrompt,
	}
	cand := eval.Variant{
		Label: eval.SideCandidate, Roles: rolesToRoleRuns(candRoles, candidatePrompt),
		Runner: candRunner, Env: candEnv,
		RunReview: doReview, ReviewPromptText: reviewCandPrompt,
	}
	if parallel {
		base.Dir = baseEnv.ProjectDir
		cand.Dir = candEnv.ProjectDir
	}

	subject := evalSubject(baseRoles, candRoles, doReview)
	fmt.Printf("Running eval: %s (%s mode, %dm timeout)...\n\n",
		subject, modeLabel(parallel), timeout)

	baseResult, candResult, runErr := eval.RunEval(ctx, base, cand, timeout, evalVerbose)
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
			kind := eval.KindReport
			if doReview {
				kind = eval.KindReview
			}
			judge, err = eval.RunJudge(ctx, judgeRunner, baseEnv, eval.JudgeInput{
				Subject:         subject,
				Kind:            kind,
				BaseReport:      baseResult.Report,
				CandidateReport: candResult.Report,
			}, evalJudgeTimeout, evalVerbose)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: judge failed: %v\n", err)
				judge = nil
			}
			fmt.Println()
		}
	}

	eval.PrintComparison(os.Stdout, subject, baseResult, candResult, judge)
	return nil
}

// resolveEvalRoles applies the precedence: --base-roles / --candidate-roles
// override --role; --role is the default for whichever side hasn't specified
// its own list.
func resolveEvalRoles() (base, cand []string, err error) {
	base = evalBaseRoles
	cand = evalCandRoles
	if len(base) == 0 && evalRole != "" {
		base = []string{evalRole}
	}
	if len(cand) == 0 && evalRole != "" {
		cand = []string{evalRole}
	}
	if len(base) == 0 || len(cand) == 0 {
		return nil, nil, fmt.Errorf("specify --role, or both --base-roles and --candidate-roles")
	}
	return base, cand, nil
}

func validateRoles(roleIDs []string, env *root.ResolvedEnv) error {
	for _, r := range roleIDs {
		if !prompts.IsValidRole(r, env.Config.Roles, env.ProjectDir, env.OrgDir) {
			return fmt.Errorf("unknown role %q in %s", r, env.ProjectDir)
		}
	}
	return nil
}

// rolesToRoleRuns assigns the prompt override to the single role on a side, if
// applicable. promptText is empty unless the caller validated len(roleIDs)==1.
func rolesToRoleRuns(roleIDs []string, promptText string) []eval.RoleRun {
	out := make([]eval.RoleRun, len(roleIDs))
	for i, id := range roleIDs {
		out[i] = eval.RoleRun{RoleID: id}
	}
	if promptText != "" && len(out) == 1 {
		out[0].PromptText = promptText
	}
	return out
}

// evalSubject builds a short label for prompts and display.
func evalSubject(baseRoles, candRoles []string, review bool) string {
	prefix := "report"
	if review {
		prefix = "review"
	}
	if slices.Equal(baseRoles, candRoles) {
		return prefix + " " + joinRoles(baseRoles)
	}
	return prefix + " " + joinRoles(baseRoles) + " vs " + joinRoles(candRoles)
}

func joinRoles(roles []string) string {
	if len(roles) == 1 {
		return roles[0]
	}
	return "[" + strings.Join(roles, ", ") + "]"
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
