package cmd

import (
	"github.com/ateam/internal/flow"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
)

// SingleSupervisorBundleInput parameterizes a generic single-prompt
// supervisor bundle — used for actions that need no special dynamics
// beyond project_info: auto_setup, anchor-walk fallback actions.
type SingleSupervisorBundleInput struct {
	Env        *root.ResolvedEnv
	Path       string // <name>.prompt.md to assemble
	RoleLabel  string // feeds {{dynamic.project_info}}; "" to suppress
	Action     string // assembler context only; not a runner.Action
	PrePrompt  string
	PostPrompt string
}

// NewSingleSupervisorBundle constructs a generic singleton supervisor
// PromptBundle. The bundle has no RunOpts / PreExec / PostExec — callers
// that need to execute it must layer those on. Spec Next-round step 6:
// auto-setup + the unknown-action fallback both use this so there is no
// parallel assembler path.
func NewSingleSupervisorBundle(in SingleSupervisorBundleInput) *flow.PromptBundle {
	a := in.Env.Assembler()
	vars := in.Env.BuildAssemblerVars(in.Path, in.RoleLabel, in.Action)
	dyn := prompts.PromptDynamic{}
	if in.RoleLabel != "" {
		dyn["project_info"] = prompts.ProjectInfoDynamic(in.Env, in.RoleLabel, in.Action)
	}
	return &flow.PromptBundle{
		Name:   in.Path,
		Role:   "supervisor",
		Action: in.Action,
		Prompt: prompts.PromptFile{
			Path:       in.Path,
			PrePrompt:  in.PrePrompt,
			PostPrompt: in.PostPrompt,
			Assembler:  a,
		},
		BaseVars: vars,
		Dynamics: dyn,
	}
}
