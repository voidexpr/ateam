package cmd

import (
	"os"
	"time"

	"github.com/ateam/internal/flow"
	"github.com/ateam/internal/flow/actions"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
)

// CodeBundleInput carries everything NewCodeBundle needs. ReviewContent
// is the operator-supplied override (`--review @file` / `--review TEXT`);
// when empty, the dynamic reads env.ReviewPath at Resolve time. The
// missing-review pre-flight check lives at the verb layer so `ateam code`
// can surface errNoReview before any flow setup.
type CodeBundleInput struct {
	Env           *root.ResolvedEnv
	ReviewContent string // operator override; empty = dynamic reads from disk
	PrePrompt     string
	PostPrompt    string
	Batch         string
	TimeoutMin    int
	Verbose       bool
	Force         bool
	Print         bool
	StartedAt     time.Time
	SharedDir     string
	SupervisorDir string
	CanonicalDest string // "{{shared}}/code/{{exec.id}}" template — resolved at run time via rt.OutputDir
}

// codeMgmtReviewDynamic returns the dynamic that emits the review block
// the code-management supervisor consumes. Spec line 365-371 lists this
// as one of the "depends on generated artifacts" dynamics — it reads
// shared/review.md produced by an earlier `ateam review` run and
// returns the spec sentinel in ModePreview rather than reading disk.
//
//   - ReviewContent != "": operator override wins in ModeReal (the
//     content is wrapped as `# Review\n\n<content>` to match the legacy
//     assembleCodeManagementV1 output byte-for-byte).
//   - ReviewContent == "" + ModeReal: read env.ReviewPath. If missing,
//     return errNoReview's message via a real error — surfaces as a
//     resolution failure that flow.execute marks the row failed for.
//   - ModePreview: return the spec sentinel.
func codeMgmtReviewDynamic(env *root.ResolvedEnv, reviewContent string) prompts.PromptDynamicFunction {
	return func(ctx prompts.ResolveContext, _ ...string) (string, error) {
		if ctx.Mode() == prompts.ModePreview {
			return "{{AT RUNTIME: code-management review block}}", nil
		}
		body := reviewContent
		if body == "" {
			data, err := os.ReadFile(env.ReviewPath())
			if err != nil {
				return "", errNoReview(env.ReviewPath())
			}
			body = string(data)
		}
		return "# Review\n\n" + body, nil
	}
}

// NewCodeBundle constructs the PromptBundle for `ateam code`. The
// supervisor body comes from defaults/prompts/code_management.prompt.md;
// the review content is woven in via {{dynamic.code_mgmt_review}}
// (spec Next-round step 4-5).
func NewCodeBundle(in CodeBundleInput) *flow.PromptBundle {
	return &flow.PromptBundle{
		Name:   "code",
		Role:   "supervisor",
		Action: runner.ActionCode,
		Prompt: prompts.PromptFile{
			Path:       "code_management",
			PrePrompt:  in.PrePrompt,
			PostPrompt: in.PostPrompt,
		},
		BaseVars: in.Env.BuildAssemblerVars("code_management", "the supervisor", "code"),
		Dynamics: prompts.PromptDynamic{
			"project_info":     prompts.ProjectInfoDynamic(in.Env, "the supervisor", "code"),
			"code_mgmt_review": codeMgmtReviewDynamic(in.Env, in.ReviewContent),
		},
		RunOpts: func(flow.RuntimeEnv) runner.RunOpts {
			return runner.RunOpts{
				RoleID:           "supervisor",
				Action:           runner.ActionCode,
				OutputKind:       runner.OutputKindExecutionReport,
				CanonicalDestDir: in.CanonicalDest,
				WorkDir:          in.Env.WorkDir,
				TimeoutMin:       in.TimeoutMin,
				Verbose:          in.Verbose,
				Batch:            in.Batch,
				StartedAt:        in.StartedAt,
				QuietExecID:      true,
			}
		},
		PreExec: []flow.Action{
			actions.CheckConcurrentRuns{If: !in.Force, Action: runner.ActionCode},
		},
		PostExec: []flow.Action{
			printCodeSessionAction{
				SharedDir:     in.SharedDir,
				SupervisorDir: in.SupervisorDir,
				Print:         in.Print,
			},
		},
	}
}
