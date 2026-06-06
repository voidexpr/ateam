package flow

import (
	"fmt"
	"strconv"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/prompts/assembler"
	"github.com/ateam/internal/root"
)

// Runtime is the per-invocation context that prompts and dynamics consume.
// Built by the flow framework around each bundle execution; satisfies
// prompts.ResolveContext.
//
// SPEC INVARIANT (plans/feature_prompt_cmd_bundle_aware.md, Next round
// steps 1-3): the exec.* namespace is resolved by rt.Vars() — the single
// substitution pass for the prompt body. The runner does NOT call
// ResolveTemplateString on the prompt; runner.go::ExecutePrepared only
// substitutes args / container fields where Prompt.Resolve cannot reach.
// If you find a parallel substitution path for the prompt body, the
// invariant has regressed.
//
// Fields fall into three groups:
//
//   - Session-scoped (DB, Env, WorkDir, Dynamics): set once at top-of-run
//     and carried unchanged through every bundle.
//   - Per-bundle prepared-by-Prepare (ExecID, Batch, OutputDir,
//     OutputFile, PromptFile): flow.execute MUST populate these from
//     `prepared` and RunOpts before calling Prompt.Resolve in ModeReal.
//     Reading them in ModeReal with zero values surfaces an error
//     pointing at the missing wire — the resolver refuses to silently
//     emit empty strings into the agent's prompt.
//   - Per-bundle run-config (Timestamp, Agent, Model, SubRunArgs,
//     DebugContext, AutoRolesCommandsOutput): same population
//     contract; some of these are RunOpts-sourced, some assembled by
//     the verb factory. Profile / Effort / MaxBudgetUSD* are
//     intentionally not represented — see the wire-on-demand pattern
//     in plans/feature_prompt_cmd_bundle_aware.md "Future".
//
// Methods (Vars, Mode, Dynamics) carry trailing parens to dodge the Go
// field/method-name collision that the spec called out.
type Runtime struct {
	DB      *calldb.CallDB
	env     *root.ResolvedEnv
	WorkDir string

	vars     prompts.Vars
	mode     prompts.ResolveMode
	dynamics prompts.PromptDynamic

	// Prepared by runner.Prepare.
	ExecID     int64
	Batch      string
	OutputDir  string
	OutputFile string
	PromptFile string

	// Run-config carried in from RunOpts / AgentExecutor.
	Timestamp  string
	Agent      string
	Model      string
	SubRunArgs string

	// Verb-assembled.
	DebugContext            string
	AutoRolesCommandsOutput string

	// Profile / Effort / MaxBudgetUSD / MaxBudgetUSDBatch are
	// deliberately NOT fields on Runtime today — no verb wires them
	// from RunOpts → rt, and no shipped default prompt references
	// them via {{exec.<key>}}. The runner-side TemplateVars
	// (internal/runner/template.go) still substitutes them in
	// runtime.hcl args / container fields; that pass is separate
	// from the prompt-body resolver. See
	// plans/feature_prompt_cmd_bundle_aware.md "Future" section for
	// the wire-on-demand pattern when a real consumer shows up.
}

// NewRuntime builds a Runtime in preview mode with the supplied carriers.
// Per-bundle scalars stay zero until the executor's Prepare step fills them
// for a real execution. The vars / dynamics / mode setters below are how
// flow tunes the runtime as it walks the pipeline.
func NewRuntime(db *calldb.CallDB, env *root.ResolvedEnv, workDir string) *Runtime {
	return &Runtime{
		DB:      db,
		env:     env,
		WorkDir: workDir,
		mode:    prompts.ModePreview,
	}
}

// SetVars rebinds the variable resolver. Mutates the receiver — flow merges
// bundle.Vars on top of a base map per pipeline step, so callers reuse one
// Runtime across the walk.
func (r *Runtime) SetVars(v prompts.Vars) { r.vars = v }

// SetMode rebinds the resolve mode. Verification flips to ModePreview;
// real execution flips to ModeReal between Prepare and ExecutePrepared.
func (r *Runtime) SetMode(m prompts.ResolveMode) { r.mode = m }

// SetDynamics rebinds the dynamics map. Typically set once at top-of-run.
func (r *Runtime) SetDynamics(d prompts.PromptDynamic) { r.dynamics = d }

// Env satisfies prompts.ResolveContext. Returns the env this Runtime
// was built around. Set by NewRuntime / SetEnv; never mutated by
// flow.execute.
func (r *Runtime) Env() *root.ResolvedEnv { return r.env }

// SetEnv rebinds the runtime's env carrier. Used by tests that need to
// inject a synthetic env after NewRuntime; production code passes env
// in at NewRuntime time.
func (r *Runtime) SetEnv(env *root.ResolvedEnv) { r.env = env }

// Vars satisfies prompts.ResolveContext. The returned Vars is the
// runtime-aware resolver: exec.* dispatches against rt fields + Mode;
// every other namespace falls through to the base Vars set via SetVars.
// Spec: this is the single resolver for the prompt body's substitution
// pass (Next round steps 1-3).
func (r *Runtime) Vars() prompts.Vars { return &runtimeVars{rt: r} }

// Mode satisfies prompts.ResolveContext.
func (r *Runtime) Mode() prompts.ResolveMode { return r.mode }

// Dynamics satisfies prompts.ResolveContext.
func (r *Runtime) Dynamics() prompts.PromptDynamic { return r.dynamics }

// compile-time check: Runtime satisfies prompts.ResolveContext.
var _ prompts.ResolveContext = (*Runtime)(nil)

// runtimeVars is the assembler.Vars implementation returned by
// rt.Vars(). It owns the exec.* namespace; other namespaces fall through
// to the base Vars set via rt.SetVars.
//
// SPEC INVARIANT: the substitution decision for exec.* lives HERE and
// nowhere else. BuildAssemblerVars's hardcoded Exec map is shadowed when
// callers go through rt.Vars(); when it dies in step 6 (deletion of the
// legacy cmd-layer assemble helpers), this resolver becomes the only
// path.
type runtimeVars struct{ rt *Runtime }

// Resolve implements assembler.Vars (and therefore prompts.Vars — they
// alias).
func (v *runtimeVars) Resolve(ns, key string) (string, bool, error) {
	if ns == "exec" {
		return v.resolveExec(key)
	}
	if v.rt.vars == nil {
		return "", false, nil
	}
	return v.rt.vars.Resolve(ns, key)
}

// Enumerate implements assembler.Vars. Combines exec.* (sourced from
// rt's typed fields + mode) with whatever the base Vars (BaseVars from
// the bundle) provides for every other namespace. A key whose resolver
// errors (typically a ModeReal field that wasn't wired) is OMITTED
// from the snapshot — squashing to "" would mask the wiring bug for
// diagnostic consumers reading the snapshot. The authoritative
// validation surface remains Resolve(ns, key); Enumerate is the
// best-effort snapshot and a missing key signals "ask Resolve".
func (v *runtimeVars) Enumerate() map[string]map[string]string {
	out := map[string]map[string]string{}
	if v.rt.vars != nil {
		for ns, m := range v.rt.vars.Enumerate() {
			if ns == "exec" {
				// exec is owned by runtimeVars — the bundle's BaseVars
				// should not be shadowing it. Skip whatever it has.
				continue
			}
			out[ns] = m
		}
	}
	execMap := make(map[string]string, len(execClosedSet))
	for key := range execClosedSet {
		val, _, err := v.resolveExec(key)
		if err != nil {
			continue
		}
		execMap[key] = val
	}
	out["exec"] = execMap
	return out
}

// resolveExec handles the closed set of exec.* keys.
//
//   - ModePreview: every key renders to {{AT RUNTIME:exec.<key>}}. This
//     is the spec's preview sentinel (line 612-613) — keeps Verify
//     deterministic, and makes `ateam prompt --action X` show clearly
//     which values defer.
//   - ModeReal: each key reads its corresponding rt field. A zero/empty
//     value is a wiring bug (flow.execute didn't populate prepared →
//     rt before calling Prompt.Resolve); surfaced as an error pointing
//     at the missing wire.
//   - Unknown key in known namespace: error (matches the spec's
//     "typos in known namespaces → engine errors" rule).
func (v *runtimeVars) resolveExec(key string) (string, bool, error) {
	if !validExecKey(key) {
		return "", true, fmt.Errorf("{{exec.%s}}: unknown key in exec namespace", key)
	}
	if v.rt.mode == prompts.ModePreview {
		// Spec line 559: exec.batch renders the real value in
		// ModePreview when known (operator pinned it via e.g.
		// `ateam prompt --batch X` → flow.WithBatch). Every other
		// exec.* key always falls back to the AT RUNTIME sentinel —
		// they are runner-allocated and never known at preview time.
		if key == "batch" && v.rt.Batch != "" {
			return v.rt.Batch, true, nil
		}
		return "{{AT RUNTIME:exec." + key + "}}", true, nil
	}
	switch key {
	// Spec Next-round step 2 lists ExecID / Batch / OutputDir / OutputFile
	// as the load-bearing four; PromptFile rides along because the codex
	// agent integration depends on it. Each one is required: an empty
	// value in ModeReal is a flow.execute wiring bug, surfaced loudly.
	case "id":
		return requireExec("exec.id", strconv.FormatInt(v.rt.ExecID, 10), v.rt.ExecID != 0)
	case "batch":
		return requireExec("exec.batch", v.rt.Batch, v.rt.Batch != "")
	case "output_dir":
		return requireExec("exec.output_dir", v.rt.OutputDir, v.rt.OutputDir != "")
	case "output_file":
		return requireExec("exec.output_file", v.rt.OutputFile, v.rt.OutputFile != "")
	case "prompt_file":
		return requireExec("exec.prompt_file", v.rt.PromptFile, v.rt.PromptFile != "")
	// Verb-supplied exec.* keys — empty-OK in ModeReal because the
	// verb chose not to wire them for this run, but the resolver
	// still substitutes whatever rt holds. Each is in the closed
	// set because at least one shipped default prompt references it
	// (timestamp / agent / model from project context blocks;
	// subrun_args from code_management.prompt.md; debug_context from
	// exec_debug.prompt.md; auto_roles_commands_output from
	// report_auto_roles.prompt.md).
	case "timestamp":
		return v.rt.Timestamp, true, nil
	case "agent":
		return v.rt.Agent, true, nil
	case "model":
		return v.rt.Model, true, nil
	case "subrun_args":
		return v.rt.SubRunArgs, true, nil
	case "debug_context":
		return v.rt.DebugContext, true, nil
	case "auto_roles_commands_output":
		return v.rt.AutoRolesCommandsOutput, true, nil
	}
	return "", true, fmt.Errorf("{{exec.%s}}: unknown key in exec namespace", key)
}

// execClosedSet is the single source of truth for the closed set of
// exec.* keys recognized by the prompt-body resolver. Used by
// validExecKey (Resolve path) and by runtimeVars.Enumerate (snapshot
// path) so the two surfaces can't drift.
//
// exec.{profile, effort, max_budget_usd, max_budget_usd_batch} are
// deliberately NOT in this set — no verb wires them from RunOpts → rt
// today, and no shipped default prompt references them. They are kept
// recognized in runner.TemplateVars (which substitutes runtime.hcl
// args / container fields against rt fields) but the prompt-body
// resolver errors on them. See plans/feature_prompt_cmd_bundle_aware.md
// "Future" section for the wire-on-demand pattern.
var execClosedSet = map[string]struct{}{
	"id":                         {},
	"batch":                      {},
	"output_dir":                 {},
	"output_file":                {},
	"prompt_file":                {},
	"timestamp":                  {},
	"agent":                      {},
	"model":                      {},
	"subrun_args":                {},
	"debug_context":              {},
	"auto_roles_commands_output": {},
}

func validExecKey(key string) bool {
	_, ok := execClosedSet[key]
	return ok
}

// requireExec returns the value if populated, or an error pointing at
// the missing wire. The error message names the offending key so a
// failing prompt run says exactly which Runtime field flow.execute
// forgot to set.
func requireExec(name, value string, populated bool) (string, bool, error) {
	if !populated {
		return "", true, fmt.Errorf("{{%s}}: not populated; flow.execute must wire it into rt before Prompt.Resolve runs", name)
	}
	return value, true, nil
}

// asserts that the runtime resolver satisfies the assembler engine's
// Vars contract. If this stops compiling, the engine consumers and the
// runtime resolver have drifted.
var _ assembler.Vars = (*runtimeVars)(nil)
