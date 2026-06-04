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

// ProjectInfoDynamic returns the dynamic that emits the project-info block
// for the given (roleLabel, action). Each verb factory builds its own
// instance — the dynamic captures role + action via the closure.
//
//   - ModeReal: invokes prompts.FormatProjectInfo with
//     e.NewProjectInfoParams.
//   - ModePreview: returns a sentinel so verification / --paths renders are
//     deterministic and don't fork git on every walk.
//   - Empty roleLabel: returns "" (matches the legacy --no-project-info
//     contract — the assembler's whitespace-only filter drops the
//     fragment).
func (e *ResolvedEnv) ProjectInfoDynamic(roleLabel, action string) prompts.PromptDynamicFunction {
	return func(ctx prompts.ResolveContext, args ...string) (string, error) {
		if ctx != nil && ctx.Mode() == prompts.ModePreview {
			return "{{AT RUNTIME: project info}}", nil
		}
		if roleLabel == "" {
			return "", nil
		}
		return prompts.FormatProjectInfo(e.NewProjectInfoParams(roleLabel, action)), nil
	}
}

// NewInspectionContext returns a ResolveContext for the
// --paths / --inline-paths inspection path. Mode is ModeReal because
// inspection previews what the live run will actually render — including
// {{dynamic.project_info}} expanded against the current repo state.
// flow.Verify uses ModePreview separately for its safer "would this even
// resolve" pass.
func (e *ResolvedEnv) NewInspectionContext(roleLabel, action string) prompts.ResolveContext {
	return &liveCtx{
		mode: prompts.ModeReal,
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
