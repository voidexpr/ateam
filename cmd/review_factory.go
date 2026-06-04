package cmd

import (
	"time"

	"github.com/ateam/internal/flow"
	"github.com/ateam/internal/flow/actions"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
)

// ReviewBundleInput carries everything NewReviewBundle needs to build a
// review PromptBundle. Selector + discovery happen at the verb layer (so
// ReviewEmptyError can surface before any flow setup); this struct is the
// post-selection handoff.
type ReviewBundleInput struct {
	Env        *root.ResolvedEnv
	Reports    []prompts.RoleReport
	PrePrompt  string
	PostPrompt string
	TimeoutMin int
	Verbose    bool
	Force      bool
	Print      bool
	StartedAt  time.Time
	ReviewFile string
}

// reviewReportsDynamic returns the dynamic that renders the reports
// manifest + bundled bodies block. The closure captures env + selector
// so live invocations rediscover the latest reports each Resolve; tests
// inject the report list directly via reviewReportsDynamicForTest.
//
// SPEC INVARIANT (plans/feature_prompt_cmd_bundle_aware.md line 388-399):
// dynamics that depend on generated artifacts return preview sentinels
// in ModePreview rather than reading disk. Verify pass + `ateam prompt
// --action review` see the sentinel; live `ateam review` runs the real
// branch.
func reviewReportsDynamic(env *root.ResolvedEnv, selector prompts.ReviewSelector) prompts.PromptDynamicFunction {
	return func(ctx prompts.ResolveContext, _ ...string) (string, error) {
		if ctx.Mode() == prompts.ModePreview {
			return "{{AT RUNTIME: review reports manifest}}", nil
		}
		all, err := prompts.DiscoverReports(env.ProjectDir)
		if err != nil {
			return "", err
		}
		reports, _ := selector.Filter(all, env.Config.Roles)
		return formatReportsBlock(reports), nil
	}
}

// NewReviewBundle constructs the PromptBundle for `ateam review`. The
// review prompt body lives entirely in defaults/prompts/review.prompt.md;
// the reports manifest is woven in via `{{dynamic.review_reports}}` so
// the live path and preview path share the exact same composition (per
// spec Next-round step 4-5).
func NewReviewBundle(in ReviewBundleInput) *flow.PromptBundle {
	a := in.Env.Assembler()
	vars := in.Env.BuildAssemblerVars("review", "the supervisor", "review")
	selector := prompts.ReviewSelector{} // ateam review always renders the manifest the verb pre-filtered.
	return &flow.PromptBundle{
		Name:   "review",
		Role:   "supervisor",
		Action: runner.ActionReview,
		Prompt: prompts.PromptFile{
			Path:       "review",
			PrePrompt:  in.PrePrompt,
			PostPrompt: in.PostPrompt,
			Assembler:  a,
		},
		BaseVars: vars,
		Dynamics: prompts.PromptDynamic{
			"project_info":   prompts.ProjectInfoDynamic(in.Env, "the supervisor", "review"),
			"review_reports": reviewReportsDynamicForReports(in.Reports, in.Env, selector),
		},
		RunOpts: func(flow.RuntimeEnv) runner.RunOpts {
			return runner.RunOpts{
				RoleID:            "supervisor",
				Action:            runner.ActionReview,
				OutputKind:        runner.OutputKindReview,
				CanonicalDestFile: in.ReviewFile,
				WorkDir:           in.Env.WorkDir,
				TimeoutMin:        in.TimeoutMin,
				Verbose:           in.Verbose,
				StartedAt:         in.StartedAt,
				QuietExecID:       true,
			}
		},
		PreExec: []flow.Action{
			actions.CheckConcurrentRuns{If: !in.Force, Action: runner.ActionReview},
		},
		PostExec: []flow.Action{
			actions.PrintArtifactPath{Label: "Review", Path: in.ReviewFile},
			actions.PrintArtifactBody{If: in.Print, Path: in.ReviewFile},
		},
	}
}

// reviewReportsDynamicForReports returns a dynamic that yields the
// supplied reports verbatim in ModeReal — the verb layer has already
// discovered and filtered (ReviewEmptyError surfaces there), so the
// dynamic must NOT rediscover. ModePreview still gates on the spec
// sentinel.
//
// env + selector are accepted so an unpopulated Reports input
// (`ateam prompt --action review` from the factory map, no verb-layer
// discovery) can fall back to a live discovery against env. In ModeReal
// with empty Reports + non-nil env, this performs the discovery; in
// ModeReal with populated Reports, returns them as-is.
func reviewReportsDynamicForReports(reports []prompts.RoleReport, env *root.ResolvedEnv, selector prompts.ReviewSelector) prompts.PromptDynamicFunction {
	return func(ctx prompts.ResolveContext, _ ...string) (string, error) {
		if ctx.Mode() == prompts.ModePreview {
			return "{{AT RUNTIME: review reports manifest}}", nil
		}
		if len(reports) > 0 {
			return formatReportsBlock(reports), nil
		}
		if env == nil {
			return formatReportsBlock(nil), nil
		}
		all, err := prompts.DiscoverReports(env.ProjectDir)
		if err != nil {
			return "", err
		}
		filtered, _ := selector.Filter(all, env.Config.Roles)
		return formatReportsBlock(filtered), nil
	}
}
