package prompts

import (
	"github.com/ateam/internal/promptdata"
	"github.com/ateam/internal/root"
)

// ProjectInfoDynamic returns the dynamic that emits the project-info
// block for the given (roleLabel, action). Mode-agnostic — project
// info reads git + config which are always available, so operators
// see real project context in any inspection (`ateam prompt --action
// X` runs in ModePreview but still wants this dynamic to produce real
// data). Empty roleLabel returns "" (matches the legacy
// --no-project-info contract).
//
// Spec step 9: moved out of *root.ResolvedEnv as a free function
// taking env, so internal/root no longer imports internal/prompts.
func ProjectInfoDynamic(env *root.ResolvedEnv, roleLabel, action string) PromptDynamicFunction {
	return func(_ ResolveContext, _ ...string) (string, error) {
		if roleLabel == "" {
			return "", nil
		}
		return promptdata.FormatProjectInfo(env.NewProjectInfoParams(roleLabel, action)), nil
	}
}
