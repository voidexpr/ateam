package cmd

import (
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"

	"github.com/ateam/internal/flow"
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
	return r, nil
}

// staticBundle constructs a PromptBundle whose Render and RunOpts
// closures return fixed values. Replaces the per-cmd ad-hoc closure
// pairs that every agent-running cmd writes for a one-shot run.
//
// Use when the prompt and RunOpts are fully determined at cmd-layer
// composition time. Bundles that need per-env overrides (e.g. a Pre
// hook that mutates RuntimeEnv) should declare PromptBundle inline.
func staticBundle(name, role, action, prompt string, opts runner.RunOpts) flow.PromptBundle {
	return flow.PromptBundle{
		Name:   name,
		Role:   role,
		Action: action,
		Render: func(flow.RuntimeEnv) (string, error) { return prompt, nil },
		RunOpts: func(flow.RuntimeEnv) runner.RunOpts {
			return opts
		},
	}
}
