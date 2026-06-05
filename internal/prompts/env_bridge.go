package prompts

import (
	"github.com/ateam/internal/promptdata"
	"github.com/ateam/internal/prompts/assembler"
	"github.com/ateam/internal/root"
)

// BuildEngine returns an assembler engine wired with the default prompt
// dynamics — currently just dynamic.project_info, the replacement for
// the retired {{project.info}} static variable. The dispatch context
// renders in ModeReal: cmd-layer assembly happens during a live
// invocation, so dynamics evaluate against actual data (not preview
// sentinels).
//
// roleLabel + action seed the project_info dynamic; pass "" for
// roleLabel to suppress the block (matching the old --no-project-info
// contract).
//
// Spec step 9: moved out of *root.ResolvedEnv so internal/root no
// longer imports internal/prompts. The prompts package owns the
// dispatch wiring; root owns the env it operates on.
func BuildEngine(env *root.ResolvedEnv, roleLabel, action string) *assembler.Engine {
	dyn := PromptDynamic{
		"project_info": ProjectInfoDynamic(env, roleLabel, action),
	}
	ctx := &liveCtx{env: env, mode: ModeReal, dynamics: dyn}
	return assembler.NewEngine(env.Assembler(), 0).
		WithDispatcher(NewDispatcher(dyn, ctx))
}

// ProjectInfoDynamic returns the dynamic that emits the project-info
// block for the given (roleLabel, action). Mode-agnostic — project info
// reads git + config which are always available, so operators see real
// project context in any inspection (`ateam prompt --action X` runs in
// ModePreview but still wants this dynamic to produce real data). Empty
// roleLabel returns "" (matches the legacy --no-project-info contract).
func ProjectInfoDynamic(env *root.ResolvedEnv, roleLabel, action string) PromptDynamicFunction {
	return func(_ ResolveContext, _ ...string) (string, error) {
		if roleLabel == "" {
			return "", nil
		}
		return promptdata.FormatProjectInfo(env.NewProjectInfoParams(roleLabel, action)), nil
	}
}

// liveCtx is a tiny ResolveContext used internally by BuildEngine.
// Carries just env + mode + dynamics; Vars stays nil (the engine reads
// vars from the Render() argument, not from ctx).
type liveCtx struct {
	env      *root.ResolvedEnv
	mode     ResolveMode
	dynamics PromptDynamic
}

func (c *liveCtx) Env() *root.ResolvedEnv  { return c.env }
func (c *liveCtx) Vars() Vars              { return nil }
func (c *liveCtx) Mode() ResolveMode       { return c.mode }
func (c *liveCtx) Dynamics() PromptDynamic { return c.dynamics }
