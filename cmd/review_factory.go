package cmd

import (
	"time"

	"github.com/ateam/internal/flow"
	"github.com/ateam/internal/flow/actions"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/prompts/assembler"
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

// reviewPrompt wraps the standard PromptFile composition with the reports
// manifest + bundled role reports block, then the outermost --post-prompt
// tail. Matches the legacy assembleReview ordering byte-for-byte:
//
//	[pre-prompt] / pre fragments / role main / post fragments
//	---
//	reports manifest + bundled bodies
//	---
//	rendered --post-prompt
//
// PostPrompt sits OUTSIDE the assembler (and outside the reports block) on
// purpose — operators expect it to be the absolute outermost tail.
type reviewPrompt struct {
	file       *prompts.PromptFile
	reports    []prompts.RoleReport
	postPrompt string
	engine     *assembler.Engine
	vars       assembler.Vars
}

func (r *reviewPrompt) Resolve(ctx prompts.ResolveContext) (string, error) {
	body, err := r.file.Resolve(ctx)
	if err != nil {
		return "", err
	}
	out := body
	if block := formatReportsBlock(r.reports); block != "" {
		out += "\n\n---\n\n" + block
	}
	rendered, err := renderCLIWrapper(r.engine, r.vars, r.postPrompt)
	if err != nil {
		return "", err
	}
	if rendered != "" {
		out += "\n\n---\n\n" + rendered
	}
	return out, nil
}

func (r *reviewPrompt) Inspect(ctx prompts.ResolveContext) ([]prompts.Section, error) {
	return r.file.Inspect(ctx)
}

// NewReviewBundle constructs the PromptBundle for `ateam review`. The
// returned bundle uses the new Prompt-based resolution path (no Render
// closure); flow walks Prepare → Prompt.Resolve → ExecutePrepared.
func NewReviewBundle(in ReviewBundleInput) *flow.PromptBundle {
	a := in.Env.Assembler()
	engine := in.Env.BuildEngine("the supervisor", "review")
	vars := in.Env.BuildAssemblerVars("review", "the supervisor", "review")
	pf := &prompts.PromptFile{
		Path:      "review",
		PrePrompt: in.PrePrompt,
		Assembler: a,
		Vars:      vars,
	}
	rp := &reviewPrompt{
		file:       pf,
		reports:    in.Reports,
		postPrompt: in.PostPrompt,
		engine:     engine,
		vars:       vars,
	}
	return &flow.PromptBundle{
		Name:   "review",
		Role:   "supervisor",
		Action: runner.ActionReview,
		Prompt: rp,
		Dynamics: prompts.PromptDynamic{
			"project_info": in.Env.ProjectInfoDynamic("the supervisor", "review"),
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
