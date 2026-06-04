package root

import (
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/prompts/assembler"
)

// BuildEngine returns an assembler engine wired with the default prompt
// dynamics — currently just dynamic.project_info, the replacement for the
// retired {{project.info}} static variable. The dispatch context renders
// in ModeReal: cmd-layer assembly happens during a live invocation, so
// dynamics evaluate against actual data (not preview sentinels).
//
// roleLabel + action seed the project_info dynamic; pass "" for roleLabel
// to suppress the block (matching the old --no-project-info contract).
func (e *ResolvedEnv) BuildEngine(roleLabel, action string) *assembler.Engine {
	dyn := prompts.PromptDynamic{
		"project_info": e.ProjectInfoDynamic(roleLabel, action),
	}
	ctx := &liveCtx{mode: prompts.ModeReal, dynamics: dyn}
	return assembler.NewEngine(e.Assembler(), 0).
		WithDispatcher(prompts.NewDispatcher(dyn, ctx))
}

// ProjectInfoDynamic returns the dynamic that emits the project-info
// block for the given (roleLabel, action). Mode-agnostic — project info
// reads git + config which are always available, so operators see real
// project context in any inspection (`ateam prompt --action X` runs in
// ModePreview but still wants this dynamic to produce real data). Empty
// roleLabel returns "" (matches the legacy --no-project-info contract).
func (e *ResolvedEnv) ProjectInfoDynamic(roleLabel, action string) prompts.PromptDynamicFunction {
	return func(_ prompts.ResolveContext, args ...string) (string, error) {
		if roleLabel == "" {
			return "", nil
		}
		return prompts.FormatProjectInfo(e.NewProjectInfoParams(roleLabel, action)), nil
	}
}

// NewInspectionContext returns a ResolveContext for the
// --paths / --inline-paths inspection path. Spec line 552-557:
// ModePreview so exec.* renders to the AT RUNTIME sentinel (no exec_id
// allocated yet) and dynamics that depend on generated artifacts emit
// preview sentinels. project_info is mode-agnostic (always returns real
// data) so inspection still shows the project context.
func (e *ResolvedEnv) NewInspectionContext(roleLabel, action string) prompts.ResolveContext {
	return &liveCtx{
		mode: prompts.ModePreview,
		dynamics: prompts.PromptDynamic{
			"project_info": e.ProjectInfoDynamic(roleLabel, action),
		},
	}
}

// liveCtx is a tiny ResolveContext used at cmd-layer assembly time, where
// there's no flow.Runtime yet. Carries just the mode + dynamics the
// dispatcher needs; Vars stays nil (the engine reads vars from the
// Render() argument, not from ctx).
type liveCtx struct {
	mode     prompts.ResolveMode
	dynamics prompts.PromptDynamic
}

func (c *liveCtx) Vars() assembler.Vars            { return nil }
func (c *liveCtx) Mode() prompts.ResolveMode       { return c.mode }
func (c *liveCtx) Dynamics() prompts.PromptDynamic { return c.dynamics }
