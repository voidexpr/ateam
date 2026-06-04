package flow

import (
	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
)

// Re-exports so callers that build PromptBundles and Prompts only have to
// import flow. Aliases (not new types) so prompts.Prompt and flow.Prompt are
// the same interface — a flow.Runtime built here satisfies any prompts.Prompt
// implemented anywhere.
type (
	Prompt                = prompts.Prompt
	ResolveContext        = prompts.ResolveContext
	ResolveMode           = prompts.ResolveMode
	Section               = prompts.Section
	Vars                  = prompts.Vars
	PromptDynamic         = prompts.PromptDynamic
	PromptDynamicFunction = prompts.PromptDynamicFunction
)

const (
	ModeReal    = prompts.ModeReal
	ModePreview = prompts.ModePreview
)

// Runtime is the per-invocation context that prompts and dynamics consume.
// Built by the flow framework around each bundle execution; satisfies
// prompts.ResolveContext.
//
// Fields fall into two groups:
//
//   - Session-scoped (DB, Env, WorkDir, Dynamics): set once at top-of-run
//     and carried unchanged through every bundle.
//   - Per-bundle (vars, mode, ExecID, Batch, OutputDir, OutputFile): built
//     by the executor's Prepare step (Step 3). Per-bundle scalars are zero
//     when mode == ModePreview — sentinel substitution at the Vars level
//     keeps preview renders deterministic.
//
// Methods (Vars, Mode, Dynamics) carry trailing parens to dodge the Go
// field/method-name collision that the spec called out.
type Runtime struct {
	DB      *calldb.CallDB
	Env     *root.ResolvedEnv
	WorkDir string

	vars     Vars
	mode     ResolveMode
	dynamics PromptDynamic

	ExecID     int64
	Batch      string
	OutputDir  string
	OutputFile string
}

// NewRuntime builds a Runtime in preview mode with the supplied carriers.
// Per-bundle scalars stay zero until the executor's Prepare step fills them
// for a real execution. The vars / dynamics / mode setters below are how
// flow tunes the runtime as it walks the pipeline.
func NewRuntime(db *calldb.CallDB, env *root.ResolvedEnv, workDir string) *Runtime {
	return &Runtime{
		DB:      db,
		Env:     env,
		WorkDir: workDir,
		mode:    ModePreview,
	}
}

// SetVars rebinds the variable resolver. Mutates the receiver — flow merges
// bundle.Vars on top of a base map per pipeline step, so callers reuse one
// Runtime across the walk.
func (r *Runtime) SetVars(v Vars) { r.vars = v }

// SetMode rebinds the resolve mode. Verification flips to ModePreview;
// real execution flips to ModeReal between Prepare and ExecutePrepared.
func (r *Runtime) SetMode(m ResolveMode) { r.mode = m }

// SetDynamics rebinds the dynamics map. Typically set once at top-of-run.
func (r *Runtime) SetDynamics(d PromptDynamic) { r.dynamics = d }

// Vars satisfies prompts.ResolveContext.
func (r *Runtime) Vars() Vars { return r.vars }

// Mode satisfies prompts.ResolveContext.
func (r *Runtime) Mode() ResolveMode { return r.mode }

// Dynamics satisfies prompts.ResolveContext.
func (r *Runtime) Dynamics() PromptDynamic { return r.dynamics }

// compile-time check: Runtime satisfies prompts.ResolveContext.
var _ ResolveContext = (*Runtime)(nil)
