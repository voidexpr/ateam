package cmd

import (
	"time"

	"github.com/ateam/internal/flow"
	"github.com/ateam/internal/flow/actions"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
)

// VerifyBundleInput carries everything NewVerifyBundle needs. CanonicalDest
// is the on-disk path for the verification report; defaults to env.VerifyPath
// when empty so the live verb stays one-line.
type VerifyBundleInput struct {
	Env           *root.ResolvedEnv
	PrePrompt     string
	PostPrompt    string
	TimeoutMin    int
	Verbose       bool
	Force         bool
	Print         bool
	StartedAt     time.Time
	CanonicalDest string // file path; empty → env.VerifyPath()
}

// NewVerifyBundle constructs the PromptBundle for `ateam verify`. Spec
// Next-round step 6: verify rides the same factory pattern as review /
// code_management — body comes from code_verify.prompt.md, no special
// dynamics besides project_info.
func NewVerifyBundle(in VerifyBundleInput) *flow.PromptBundle {
	a := in.Env.Assembler()
	vars := in.Env.BuildAssemblerVars("code_verify", "the supervisor", "verify")
	dest := in.CanonicalDest
	if dest == "" {
		dest = in.Env.VerifyPath()
	}
	return &flow.PromptBundle{
		Name:   "verify",
		Role:   "supervisor",
		Action: runner.ActionVerify,
		Prompt: prompts.PromptFile{
			Path:       "code_verify",
			PrePrompt:  in.PrePrompt,
			PostPrompt: in.PostPrompt,
			Assembler:  a,
		},
		BaseVars: vars,
		Dynamics: prompts.PromptDynamic{
			"project_info": prompts.ProjectInfoDynamic(in.Env, "the supervisor", "verify"),
		},
		RunOpts: func(flow.RuntimeEnv) runner.RunOpts {
			return runner.RunOpts{
				RoleID:            "supervisor",
				Action:            runner.ActionVerify,
				OutputKind:        runner.OutputKindVerify,
				CanonicalDestFile: dest,
				WorkDir:           in.Env.WorkDir,
				TimeoutMin:        in.TimeoutMin,
				Verbose:           in.Verbose,
				StartedAt:         in.StartedAt,
				QuietExecID:       true,
			}
		},
		PreExec: []flow.Action{
			actions.CheckConcurrentRuns{If: !in.Force, Action: runner.ActionVerify},
		},
		PostExec: []flow.Action{
			actions.PrintArtifactPath{Label: "Verification report", Path: dest},
			actions.PrintArtifactBody{If: in.Print, Path: dest},
		},
	}
}
