package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ateam/internal/flow"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/prompts/assembler"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
)

// RunnerSpec is the cmd-layer adapter from a command's flag set to the
// runner resolution chain. Each agent-running cmd (exec, parallel,
// report, etc.) bundles its per-flag values into this shape and calls
// buildRunner; the helper handles project vs scratch dispatch, the
// "default" profile fallback for scratch mode, RunnerOverrides
// application, and source-dir writability.
//
// Fields mirror what resolveRunner / resolveRunnerMinimal /
// applyRunnerOverrides consume — see RunnerOverrides for the
// model/effort/container/budget overrides.
type RunnerSpec struct {
	Profile         string
	Agent           string
	Action          string
	Role            string // forwarded to project-mode resolveRunner for per-role profile resolution; empty for parallel
	DockerAutoSetup bool
	Overrides       RunnerOverrides
}

// buildRunner resolves an *AgentExecutor for the given env+spec. On
// success it returns a ready-to-use runner with overrides applied and
// the source dir marked writable. On error returns (nil, err) — the
// caller decides whether to short-circuit (parallel/report) or surface
// the error as a warning while continuing (exec --dry-run).
//
// Scratch mode (no project context) falls back to profile="default"
// when neither --profile nor --agent was set; matches the historical
// behavior of every cmd that supports scratch operation.
func buildRunner(env *root.ResolvedEnv, spec RunnerSpec) (*runner.AgentExecutor, error) {
	hasProject := env.HasProject()

	var (
		r   *runner.AgentExecutor
		err error
	)
	if hasProject {
		r, err = resolveRunner(env, spec.Profile, spec.Agent, spec.Action, spec.Role, spec.DockerAutoSetup)
	} else {
		profile := spec.Profile
		if profile == "" && spec.Agent == "" {
			profile = "default"
		}
		r, err = resolveRunnerMinimal(env.OrgDir, profile, spec.Agent)
	}
	if err != nil {
		return nil, err
	}
	if err := applyRunnerOverrides(r, env, spec.Overrides, spec.Action); err != nil {
		return nil, err
	}
	setSourceWritable(r)
	r.ShimDir = root.EnsureCLIShim(env)
	return r, nil
}

// buildArgPrompt picks the Prompt implementation for an
// `ateam exec`-style CLI argument. --pre-prompt / --post-prompt are
// stamped onto the resulting Prompt's PrePrompt/PostPrompt fields and
// reach the agent as RAW text on every impl (spec implementer note 2).
// Single dispatch rule (spec lines 471-478):
//
//   - --raw set                                → RawTextPrompt
//   - `@PATH` where PATH is a filesystem-shape
//     .prompt.md reference                     → PromptFile with an
//     explicit TempAnchor rooted at PATH's parent dir; sibling
//     `<basename>.pre.*.md` and dir-level `_pre.*.md` fragments
//     compose around the body
//   - otherwise (literal text, `@PATH` not ending in `.prompt.md`,
//     `@-` stdin)                              → PromptText
//
// The PromptFile branch does NOT pre-read the file — PromptFile reads
// it through the assembler at Resolve time. The other branches
// pre-read via prompts.ResolveValue so the body is known up front.
func buildArgPrompt(env *root.ResolvedEnv, arg, prePrompt, postPrompt string, raw bool) (prompts.Prompt, error) {
	if !raw && strings.HasPrefix(arg, "@") && !strings.HasPrefix(arg, "@-") {
		cleanPath := strings.TrimPrefix(arg, "@")
		if assembler.IsFilesystemPath(cleanPath) {
			parentDir := filepath.Dir(cleanPath)
			if parentDir == "" {
				parentDir = "."
			}
			pf := prompts.PromptFile{
				Path:       cleanPath,
				PrePrompt:  prePrompt,
				PostPrompt: postPrompt,
			}
			if env != nil {
				pf.Assembler = assembler.NewTempAnchor(parentDir, env.Assembler())
			}
			return pf, nil
		}
	}
	body, err := prompts.ResolveValue(arg)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve prompt: %w", err)
	}
	if raw {
		return prompts.RawTextPrompt{Text: body, PrePrompt: prePrompt, PostPrompt: postPrompt}, nil
	}
	return prompts.PromptText{Text: body, PrePrompt: prePrompt, PostPrompt: postPrompt}, nil
}

// staticBundle constructs a PromptBundle around an already-composed
// prompt body. The caller picks the Prompt implementation:
//
//   - prompts.PromptText{Text: …}: variable substitution and dynamics
//     run against rt.Vars()/rt.Dynamics(). Spec step 10 makes this the
//     default for `ateam exec` — operators piping pre-assembled prompts
//     can still reference {{exec.output_dir}} / {{prompt.name}} etc.
//   - prompts.RawTextPrompt{Text: …}: bytes-through, no engine. Used by
//     `ateam exec --raw` and by sub-step bundles that already finished
//     their own expansion.
//
// env seeds BaseVars/Dynamics so non-exec namespaces ({{prompt.name}},
// {{project.name}}, {{git.branch}}, {{env.X}}, {{dynamic.project_info}})
// resolve for non-raw bodies — same substitution surface factory bundles
// expose. RawTextPrompt bypasses expansion regardless.
//
// Runner-level substitution (args / container fields / canonical-dest
// path) still applies — that lives inside ExecutePrepared and runs
// against runner.TemplateVars, not against ctx.Vars().
func staticBundle(name, role, action string, prompt prompts.Prompt, opts runner.RunOpts, env *root.ResolvedEnv) flow.PromptBundle {
	b := flow.PromptBundle{
		Name:   name,
		Role:   role,
		Action: action,
		Prompt: prompt,
		RunOpts: func(flow.RuntimeEnv) runner.RunOpts {
			return opts
		},
	}
	if env != nil {
		b.BaseVars = env.BuildAssemblerVars(name, role, action)
		b.Dynamics = prompts.PromptDynamic{
			"project_info": prompts.ProjectInfoDynamic(env, role, action),
		}
	}
	return b
}
