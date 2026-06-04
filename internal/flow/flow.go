// Package flow is ateam's composition framework for agent-running commands.
//
// Replaces internal/stage. A Step is one of PromptBundle (leaf), Pipeline
// (sequence, stops on first errored step), or Parallel (fan-out). Top-level
// callers invoke Run(); per-step lifecycle observability flows through the
// Reporter interface.
//
// Scope: every built-in cmd that drives an agent (exec, parallel, verify,
// review, auto_setup, code, report). The package is internal on purpose;
// the supported external integration surface is `ateam exec` as a
// subprocess (see plans/python_framework_examples/).
//
// Design rationale and the Phase F→G migration are documented in
// plans/Feature_prompt_report_fs_refactor_phaseG.md.
package flow

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"sync"
	"time"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
)

// ============================================================
// Carriers
// ============================================================

// RunCtx is the execution-side carrier — cancellation, the call-tracking
// DB, the resolved ateam env, and the reporter. Threaded unchanged through
// every Step's execute call.
//
// Resolved lives here (not on RuntimeEnv) because it's session-scoped like
// DB. Keeping it off RuntimeEnv preserves RuntimeEnv's "freely rebound at
// composition boundaries" property.
type RunCtx struct {
	Ctx      context.Context
	DB       *calldb.CallDB
	Resolved *root.ResolvedEnv
	Reporter Reporter
}

// RuntimeEnv is the "where & how to invoke the agent" config. Freely
// rebound by Pipeline.Env / Parallel.Env / PromptBundle.Env at any
// composition boundary.
type RuntimeEnv struct {
	Executor  Executor
	WorkDir   string
	Role      string
	Action    string
	DryRun    bool
	Batch     string
	PromptDir string
}

func (e RuntimeEnv) withBundleOverrides(role, action string) RuntimeEnv {
	if role != "" {
		e.Role = role
	}
	if action != "" {
		e.Action = action
	}
	return e
}

// Executor is the minimal surface PromptBundle consumes from the agent
// runner. The production implementation is *runner.AgentExecutor. Defined
// as an interface here so flow tests can substitute a fake without
// standing up the runner machinery.
//
// The split into Prepare + ExecutePrepared lets PromptBundle fire
// AgentExecStart with a known exec_id BEFORE the agent process is
// launched, so per-run observers (e.g. BundleLogReporter) can open
// <LogsDir>/bundle.jsonl up front instead of buffering in memory.
//
// Prepare does NOT receive the prompt: prompt resolution happens in flow
// between Prepare and ExecutePrepared, so the resolver can reference the
// allocated exec_id and other runtime-dependent values.
//
// onProgress is invoked synchronously for each RunProgress event and may
// fire from multiple internal goroutines; the callback owns its own
// thread-safety. Nil disables progress reporting.
type Executor interface {
	Prepare(opts runner.RunOpts) (*runner.PreparedRun, error)
	ExecutePrepared(ctx context.Context, prepared *runner.PreparedRun, prompt string, onProgress func(runner.RunProgress)) runner.RunSummary
}

// ============================================================
// Flow & Result
// ============================================================

// FlowState classifies an action or bundle outcome.
type FlowState int

const (
	StateContinue FlowState = iota
	StateSkip
	StateError
)

func (s FlowState) String() string {
	switch s {
	case StateContinue:
		return "continue"
	case StateSkip:
		return "skip"
	case StateError:
		return "error"
	}
	return "unknown"
}

// Flow is the return type of Action.Run and the state record on Result.
// Err is populated only when State == StateError.
type Flow struct {
	State  FlowState
	Reason string
	Err    error
}

// Result is per-leaf. Each PromptBundle execution produces exactly one Result.
type Result struct {
	Bundle  *PromptBundle
	Env     RuntimeEnv
	Flow    Flow
	Summary *runner.RunSummary // nil for skipped / pre-error / render-error
}

// StepOutcome is one entry in PipelineResult.Steps. Skipped is true when
// a prior step in the same Pipeline errored and this step never executed;
// in that case Results is empty and Reason explains why.
type StepOutcome struct {
	Name    string
	Results []Result
	Skipped bool
	Reason  string
}

// PipelineResult is the top-level return from Run(). Non-Pipeline tops
// produce a single-step result wrapping their leaves.
type PipelineResult struct {
	Steps           []StepOutcome
	FirstErrorIndex int // -1 if no error
}

// FirstError returns the first leaf error across all steps, or nil.
// Convenience for cmd-layer "translate flow result into a Go error return."
func (r PipelineResult) FirstError() error {
	for _, s := range r.Steps {
		for _, lr := range s.Results {
			if lr.Flow.State == StateError && lr.Flow.Err != nil {
				return lr.Flow.Err
			}
		}
	}
	return nil
}

// ============================================================
// Step interface and top-level Run
// ============================================================

// Step is implemented by PromptBundle, Pipeline, Parallel. Internal —
// callers go through Run() so PipelineResult shape is uniform at the top.
type Step interface {
	execute(rc RunCtx, env RuntimeEnv) []Result
	name() string
	// Walk visits every PromptBundle reachable from this Step exactly
	// once. Order isn't part of the contract (Pipeline visits left-to-
	// right, Parallel in slice order — but consumers must not depend on
	// either). Used by Verify to fan out preview-mode resolution.
	Walk(fn func(*PromptBundle))
}

// VerifyError records a single bundle's resolve-time failure during the
// pre-execution verification pass.
type VerifyError struct {
	BundleName string
	Err        error
}

func (e VerifyError) Error() string {
	if e.BundleName == "" {
		return e.Err.Error()
	}
	return e.BundleName + ": " + e.Err.Error()
}

// Unwrap so errors.Is / errors.As reach the underlying resolve error —
// callers that pattern-match on a sentinel returned from Prompt.Resolve
// keep working when the failure happens during the verify pass.
func (e VerifyError) Unwrap() error { return e.Err }

// VerifyResult batches every bundle's verification outcome. nil or empty
// Errors means the pipeline is clean to execute.
type VerifyResult struct {
	Errors []VerifyError
}

// Verify walks every bundle reachable from top and runs Prompt.Resolve
// against a preview-mode runtime — exec.* keys substitute their sentinels,
// dynamics return their preview branches, and the engine surfaces typos in
// known namespaces, missing strict includes, and other authoring errors
// before any agent process is launched. Bundles with a nil Prompt are
// skipped (e.g. composition placeholders).
func Verify(top Step, rc RunCtx) *VerifyResult {
	var errs []VerifyError
	top.Walk(func(b *PromptBundle) {
		if b == nil || b.Prompt == nil {
			return
		}
		err := verifyBundle(b, rc)
		if err != nil {
			errs = append(errs, VerifyError{BundleName: b.Name, Err: err})
		}
	})
	if len(errs) == 0 {
		return nil
	}
	return &VerifyResult{Errors: errs}
}

// verifyBundle runs the bundle's Prompt.Resolve in preview mode, catching
// panics so a single broken bundle can't crash the whole verification
// pass.
func verifyBundle(b *PromptBundle, rc RunCtx) (err error) {
	defer func() {
		if rv := recover(); rv != nil {
			err = fmt.Errorf("panic during verify: %v", rv)
		}
	}()
	rt := NewRuntime(rc.DB, rc.Resolved, "")
	rt.SetMode(prompts.ModePreview)
	if b.BaseVars != nil {
		rt.SetVars(b.BaseVars)
	}
	if b.Dynamics != nil {
		rt.SetDynamics(b.Dynamics)
	}
	_, err = b.Prompt.Resolve(rt)
	return err
}

func failedWithVerifyErrors(vr *VerifyResult) PipelineResult {
	results := make([]Result, 0, len(vr.Errors))
	for _, e := range vr.Errors {
		bundle := &PromptBundle{Name: e.BundleName}
		results = append(results, Result{
			Bundle: bundle,
			Flow:   Flow{State: StateError, Reason: "verify failed", Err: e},
		})
	}
	return PipelineResult{
		Steps:           []StepOutcome{{Name: "verify", Results: results}},
		FirstErrorIndex: 0,
	}
}

// Run is the top-level entry. Always returns PipelineResult — a top-level
// PromptBundle or Parallel produces a one-step result.
//
// Verification runs first: every bundle's Prompt.Resolve is called against
// a preview-mode runtime so authoring errors (typos in known namespaces,
// missing strict includes, format errors in included files) surface
// before any Prepare touches the call DB. A non-empty VerifyResult
// short-circuits with a synthesized failure step.
func Run(top Step, env RuntimeEnv, rc RunCtx) PipelineResult {
	if rc.Reporter == nil {
		rc.Reporter = NoopReporter{}
	}
	if vr := Verify(top, rc); vr != nil && len(vr.Errors) > 0 {
		return failedWithVerifyErrors(vr)
	}
	if p, ok := top.(Pipeline); ok {
		return p.runDetailed(rc, env)
	}
	results := top.execute(rc, env)
	idx := -1
	if hasError(results) {
		idx = 0
	}
	return PipelineResult{
		Steps:           []StepOutcome{{Name: top.name(), Results: results}},
		FirstErrorIndex: idx,
	}
}

// RunBundle is the single-bundle shortcut. Callers that wrap one
// PromptBundle in Run and then drill `result.Steps[0].Results[0]` to
// retrieve the Summary should use this instead — it returns the leaf
// Result directly. Equivalent to `Run(b, env, rc).Steps[0].Results[0]`.
func RunBundle(b PromptBundle, env RuntimeEnv, rc RunCtx) Result {
	if rc.Reporter == nil {
		rc.Reporter = NoopReporter{}
	}
	results := b.execute(rc, env)
	if len(results) == 0 {
		return Result{Bundle: &b, Env: env}
	}
	return results[0]
}

// ============================================================
// Action
// ============================================================

// Action is the common shape for PreExec / PostExec hooks on PromptBundle.
//
// res is nil for PreExec actions (no Result yet) and non-nil for PostExec
// (the Result-in-progress; Summary is populated, Flow reflects the agent
// outcome). PostExec actions read res for fallback values (Summary.Output)
// but should not mutate res — the framework owns Flow upgrades.
//
// PreExec semantics:
//   - Flow{State: StateContinue} → next Pre action (or Render+Execute)
//   - Flow{State: StateSkip}     → bundle ends successfully; no agent, no Post
//   - Flow{State: StateError}    → bundle ends with the error; no agent, no Post
//
// PostExec semantics:
//   - PostExec only runs when the agent (and all Pre actions) succeeded.
//     If Pre returned Skip/Error or the agent reported IsError, Post is
//     skipped entirely. Mirrors stage's FailOnExecError-gated behavior:
//     "Verification report: ..." and similar pointers should not fire
//     on a failed run.
//   - When it does run, all Post actions execute in order, regardless of
//     each other's return. The bundle's Result.Flow is upgraded to the
//     first Error returned by a Post action.
type Action interface {
	Run(rc RunCtx, env RuntimeEnv, res *Result) Flow
}

// ============================================================
// PromptBundle (leaf)
// ============================================================

// PromptBundle is a single agent-invocation envelope. The cmd-layer
// constructs one per call; the bundle's Prompt produces the agent input
// text, RunOpts shapes the runner invocation, and the action hooks fire
// around the agent call.
//
// Vars holds factory-curated args.* / roles.* / action.* values. Forward-
// looking field — defaults don't reference these keys yet (Step 6's sweep
// activates them). Carried on the bundle so the same Prompt impl can be
// reused across verbs with different exposed values.
type PromptBundle struct {
	Name   string
	Role   string      // optional override of env.Role
	Action string      // optional override of env.Action
	Env    *RuntimeEnv // optional override of parent's env (used for per-role profile)

	Prompt prompts.Prompt
	// BaseVars is the factory-supplied resolver for non-exec.* namespaces
	// (project.*, prompt.*, git.*, container.*, ateam.*, role.*). exec.*
	// always dispatches to rt's fields via runtimeVars regardless of
	// what's in BaseVars. flow.execute calls rt.SetVars(b.BaseVars)
	// before Prompt.Resolve runs, so any consumer of ctx.Vars() sees the
	// runtime-aware view. Spec: this is "the framework builds an impl
	// per-invocation and stores it on Runtime.Vars" (line 310-311) made
	// concrete.
	BaseVars prompts.Vars
	// Vars is the factory-curated args.* / roles.* / action.* map. Spec
	// line 287; the merge into rt.Vars happens in step 8.
	Vars     map[string]string
	Dynamics prompts.PromptDynamic
	RunOpts  func(env RuntimeEnv) runner.RunOpts
	PreExec  []Action
	PostExec []Action
}

// resolvePrompt runs the bundle's Prompt against a freshly built runtime.
// A nil Prompt is a programmer error — the factory must set one (callers
// that just want literal text use prompts.RawTextPrompt).
//
// SPEC INVARIANT: when prepared != nil, this is the live-execution path
// and rt's exec.* fields are populated from prepared + opts so the
// resolver substitutes real values inline (Next round step 2). When
// prepared == nil, the call is preview / dry-run / verify, mode is
// prompts.ModePreview, and exec.* renders to {{AT RUNTIME:exec.<key>}} sentinels.
// The runner does NOT substitute the prompt body afterward.
func (b PromptBundle) resolvePrompt(rc RunCtx, env RuntimeEnv, opts runner.RunOpts, prepared *runner.PreparedRun) (string, error) {
	if b.Prompt == nil {
		return "", errBundleHasNoPrompt
	}
	rt := newBundleRuntime(rc, env, opts, prepared)
	if b.BaseVars != nil {
		rt.SetVars(b.BaseVars)
	}
	if b.Dynamics != nil {
		rt.SetDynamics(b.Dynamics)
	}
	return b.Prompt.Resolve(rt)
}

var errBundleHasNoPrompt = fmt.Errorf("PromptBundle has no Prompt set")

// ResolvePreview renders the bundle's Prompt in prompts.ModePreview against a
// freshly built runtime that auto-loads bundle.BaseVars + bundle.Dynamics.
// The single entry point for `ateam prompt --action X`, dry-run preview,
// and other operator-facing inspection — every caller used to repeat
// the rt.SetVars / rt.SetDynamics wiring inline. Spec Next-round step 8.
func (b *PromptBundle) ResolvePreview(env *root.ResolvedEnv, workDir string) (string, error) {
	if b.Prompt == nil {
		return "", errBundleHasNoPrompt
	}
	rt := b.previewRuntime(env, workDir)
	return b.Prompt.Resolve(rt)
}

// InspectPreview is ResolvePreview's section-level counterpart used by
// --paths / --inline-paths. Same auto-loading contract.
func (b *PromptBundle) InspectPreview(env *root.ResolvedEnv, workDir string) ([]prompts.Section, error) {
	if b.Prompt == nil {
		return nil, errBundleHasNoPrompt
	}
	rt := b.previewRuntime(env, workDir)
	return b.Prompt.Inspect(rt)
}

func (b *PromptBundle) previewRuntime(env *root.ResolvedEnv, workDir string) *Runtime {
	rt := NewRuntime(nil, env, workDir)
	rt.SetMode(prompts.ModePreview)
	if b.BaseVars != nil {
		rt.SetVars(b.BaseVars)
	}
	if b.Dynamics != nil {
		rt.SetDynamics(b.Dynamics)
	}
	return rt
}

// newBundleRuntime constructs the per-bundle prompt resolution context.
//
// Mode + field population are paired:
//
//   - prepared == nil (dry-run, verify, ateam prompt --action X): rt.Mode
//     = prompts.ModePreview. Per-bundle scalars stay zero — the runtime-aware
//     Vars dispatches exec.* to the AT RUNTIME sentinel pattern.
//   - prepared != nil (live exec after runner.Prepare): rt.Mode = prompts.ModeReal
//     and the prepared-by-Prepare fields (ExecID, Batch, OutputDir,
//     OutputFile, PromptFile) plus the run-config fields (Timestamp,
//     Profile, Agent, Model, Effort, MaxBudgetUSD, MaxBudgetUSDBatch,
//     SubRunArgs) are populated. The resolver substitutes real values
//     during Prompt.Resolve; the runner's ResolveTemplateString pass on
//     the prompt body is deleted (Next round step 3).
//
// AutoRolesCommandsOutput / DebugContext are verb-supplied and propagate
// via RunOpts so the auto-roles planner and inspect --auto-debug verbs
// can carry their pre-baked context without touching the runner.
func newBundleRuntime(rc RunCtx, env RuntimeEnv, opts runner.RunOpts, prepared *runner.PreparedRun) *Runtime {
	rt := NewRuntime(rc.DB, rc.Resolved, env.WorkDir)
	if prepared == nil {
		rt.SetMode(prompts.ModePreview)
		return rt
	}
	rt.SetMode(prompts.ModeReal)
	rt.ExecID = prepared.ExecID
	rt.Batch = opts.Batch
	rt.OutputDir = prepared.RuntimeDir
	rt.OutputFile = primaryOutputFile(prepared, opts)
	rt.PromptFile = prepared.PromptFile
	rt.Timestamp = prepared.StartedAt.Format(time.RFC3339)
	rt.Agent = prepared.AgentName
	rt.Model = prepared.Model
	rt.AutoRolesCommandsOutput = opts.AutoRolesCommandsOutput
	// Profile / Effort / MaxBudgetUSD* / SubRunArgs / DebugContext are
	// recognized in the resolver but populated by the verb migration in a
	// later step — they live on AgentExecutor or in verb-specific paths
	// today and would need RunOpts to grow to carry them. No shipped
	// default prompt references those keys; user prompts that do render
	// to "" instead of erroring, which surfaces the gap without breaking
	// runs.
	return rt
}

// primaryOutputFile derives the canonical OutputFile path the runner
// would substitute as `{{OUTPUT_FILE}}`/`{{exec.output_file}}`. Live
// runs use prepared.RuntimeDir + the primary output name (e.g.
// report.md). For actions with no primary output, returns "" — callers
// that reference {{exec.output_file}} for such actions will hit the
// resolver's "not populated" error, which is the correct signal.
func primaryOutputFile(prepared *runner.PreparedRun, opts runner.RunOpts) string {
	if prepared == nil {
		return ""
	}
	primary := runner.PrimaryOutputName(opts.OutputKind, opts.PromptName)
	if primary == "" {
		return ""
	}
	if prepared.RuntimeDir == "" {
		return ""
	}
	return filepath.Join(prepared.RuntimeDir, primary)
}

func (b PromptBundle) name() string { return b.Name }

func (b PromptBundle) Walk(fn func(*PromptBundle)) {
	// Capture b in a fresh local so the pointer survives across recursive
	// walks (the parameter b is value-copied per call).
	bb := b
	fn(&bb)
}

func (b PromptBundle) execute(rc RunCtx, env RuntimeEnv) (out []Result) {
	if b.Env != nil {
		env = *b.Env
	}
	env = env.withBundleOverrides(b.Role, b.Action)

	bi := BundleInfo{Name: b.Name, Role: env.Role, Action: env.Action, WorkDir: env.WorkDir, Batch: env.Batch}
	rc.Reporter.BundleStart(bi)

	emit := func(r Result) []Result {
		rc.Reporter.BundleEnd(bi, r)
		return []Result{r}
	}

	// Recover panics here (not just at the worker level) so BundleStart is
	// always paired with BundleEnd. Without this, a panic in Render /
	// PreExec / Executor leaves the reporter row stuck mid-state and the
	// command-level counts disagree with the synthetic Error Result the
	// worker would otherwise append.
	defer func() {
		if rv := recover(); rv != nil {
			out = emit(panicResult(b, env, rv))
		}
	}()

	for i, a := range b.PreExec {
		at := actionTypeName(a)
		rc.Reporter.ActionStart(bi, PreExec, at, i)
		start := time.Now()
		f := a.Run(rc, env, nil)
		rc.Reporter.ActionEnd(bi, PreExec, at, i, f, time.Since(start))
		if f.State != StateContinue {
			return emit(Result{Bundle: &b, Env: env, Flow: f})
		}
	}

	var opts runner.RunOpts
	if b.RunOpts != nil {
		opts = b.RunOpts(env)
	}
	if opts.RoleID == "" {
		opts.RoleID = env.Role
	}
	if opts.Action == "" {
		opts.Action = env.Action
	}
	if opts.WorkDir == "" {
		opts.WorkDir = env.WorkDir
	}
	if opts.Batch == "" {
		opts.Batch = env.Batch
	}

	if env.DryRun {
		// Resolve in dry-run so verbs can surface resolution-time errors
		// and --plan-only style previews still see the resolved prompt.
		// Skip the allocate/execute path entirely. Pass nil prepared so
		// rt enters prompts.ModePreview and exec.* renders to the AT RUNTIME
		// sentinel pattern (per Next-round step 1's resolver).
		if _, err := b.resolvePrompt(rc, env, opts, nil); err != nil {
			return emit(Result{Bundle: &b, Env: env, Flow: Flow{
				State: StateError, Reason: "render failed", Err: err,
			}})
		}
		return emit(Result{Bundle: &b, Env: env, Flow: Flow{
			State: StateContinue, Reason: "dry-run",
		}})
	}

	prepared, prepErr := env.Executor.Prepare(opts)
	if prepErr != nil {
		return emit(Result{Bundle: &b, Env: env, Flow: Flow{
			State: StateError, Reason: "prepare failed", Err: prepErr,
		}})
	}

	// Prompt resolution happens between Prepare and ExecutePrepared so the
	// resolver can reference runtime-dependent values (exec.id, etc.).
	// SPEC INVARIANT (Next-round step 2): the prepared run is wired into
	// rt by newBundleRuntime here, so rt.{ExecID, Batch, OutputDir,
	// OutputFile, PromptFile} drive Prompt.Resolve's substitution
	// directly. The runner does NOT substitute the prompt body afterward
	// (step 3 deleted that pass). A failure here happens after the exec
	// row was allocated, so we close the row as failed before propagating.
	prompt, err := b.resolvePrompt(rc, env, opts, prepared)
	if err != nil {
		if rc.DB != nil {
			_ = rc.DB.MarkExecFailed(prepared.ExecID, "render failed: "+err.Error(), time.Now())
		}
		return emit(Result{Bundle: &b, Env: env, Flow: Flow{
			State: StateError, Reason: "render failed", Err: err,
		}})
	}
	prepared.PromptBytes = len(prompt)
	rc.Reporter.AgentExecStart(bi, prepared)
	summary := env.Executor.ExecutePrepared(rc.Ctx, prepared, prompt, func(p runner.RunProgress) {
		rc.Reporter.AgentEvent(bi, p)
	})
	rc.Reporter.AgentExecEnd(bi, summary)

	flow := Flow{State: StateContinue}
	if summary.IsError {
		flow = Flow{State: StateError, Reason: summary.ErrorCause, Err: summary.Err}
	}
	r := Result{Bundle: &b, Env: env, Flow: flow, Summary: &summary}

	if r.Flow.State == StateContinue {
		for i, a := range b.PostExec {
			at := actionTypeName(a)
			rc.Reporter.ActionStart(bi, PostExec, at, i)
			start := time.Now()
			pf := a.Run(rc, env, &r)
			rc.Reporter.ActionEnd(bi, PostExec, at, i, pf, time.Since(start))
			if pf.State == StateError && r.Flow.State == StateContinue {
				r.Flow = pf
			}
		}
	}

	return emit(r)
}

// ============================================================
// Pipeline
// ============================================================

// Pipeline runs steps in order. The first errored step stops the chain;
// remaining steps are marked Skipped and surfaced via Reporter.StepSkipped.
// Skip results (StateSkip) do NOT stop the chain — skip counts as success.
type Pipeline struct {
	Name  string
	Steps []Step
	Env   *RuntimeEnv
}

func (p Pipeline) name() string { return p.Name }

func (p Pipeline) Walk(fn func(*PromptBundle)) {
	for _, s := range p.Steps {
		s.Walk(fn)
	}
}

// execute exposes Pipeline as a Step inside a parent composition. Inner
// step boundaries collapse to flat []Result for the parent; structured
// PipelineResult granularity exists only at the level where Pipeline is
// the top of Run().
func (p Pipeline) execute(rc RunCtx, env RuntimeEnv) []Result {
	pr := p.runDetailed(rc, env)
	var flat []Result
	for _, s := range pr.Steps {
		flat = append(flat, s.Results...)
	}
	return flat
}

func (p Pipeline) runDetailed(rc RunCtx, env RuntimeEnv) PipelineResult {
	if p.Env != nil {
		env = *p.Env
	}
	si := StageInfo{Kind: StagePipeline, Name: p.Name, Children: len(p.Steps)}
	rc.Reporter.StageStart(si)

	pr := PipelineResult{FirstErrorIndex: -1}
	stopped := false
	for i, s := range p.Steps {
		if stopped {
			reason := "earlier step failed"
			pr.Steps = append(pr.Steps, StepOutcome{
				Name:    s.name(),
				Skipped: true,
				Reason:  reason,
			})
			rc.Reporter.StepSkipped(si, s.name(), reason)
			continue
		}
		results := s.execute(rc, env)
		pr.Steps = append(pr.Steps, StepOutcome{Name: s.name(), Results: results})
		if hasError(results) {
			pr.FirstErrorIndex = i
			stopped = true
		}
	}

	rc.Reporter.StageEnd(si, stageOutcomeFromPipeline(pr))
	return pr
}

// ============================================================
// Parallel
// ============================================================

// Parallel fans out steps concurrently. Workers caps the live goroutine
// count; 0 → min(len(Steps), 5). DryRun and len(Steps) <= 1 fall back to
// sequential execution. Parallel does NOT short-circuit on errors — every
// step runs to completion; the parent (a Pipeline, if any) decides what
// to do with the aggregate.
//
// PreDispatch, if set, fires once per step in dispatch order, BEFORE the
// step's worker slot is acquired. Returning a non-nil error stops further
// dispatching: the remaining steps are emitted as StateSkip results with
// the error's message as the skip reason. In-flight steps run to
// completion. Models runner.RunPoolWithOpts.PoolOpts.PreDispatch.
//
// Worker goroutines recover from panics and emit a synthetic Error Result
// per panicking step (matching runner.RunPool's per-task panic safety
// net). Without this, a panic in any step would tear down every sibling.
type Parallel struct {
	Name        string
	Steps       []Step
	Env         *RuntimeEnv
	Workers     int
	PreDispatch func() error
}

func (p Parallel) name() string { return p.Name }

func (p Parallel) Walk(fn func(*PromptBundle)) {
	for _, s := range p.Steps {
		s.Walk(fn)
	}
}

func (p Parallel) execute(rc RunCtx, env RuntimeEnv) []Result {
	if p.Env != nil {
		env = *p.Env
	}
	si := StageInfo{Kind: StageParallel, Name: p.Name, Children: len(p.Steps)}
	rc.Reporter.StageStart(si)

	var results []Result
	switch {
	case env.DryRun, len(p.Steps) <= 1:
		results = p.runSequential(rc, env)
	default:
		workers := p.Workers
		if workers <= 0 {
			workers = min(len(p.Steps), 5)
		}
		results = p.runConcurrently(rc, env, workers)
	}

	rc.Reporter.StageEnd(si, stageOutcomeFromResults(results))
	return results
}

// runSequential dispatches steps one at a time. Used for DryRun and
// single-step Parallels. PreDispatch is honored.
func (p Parallel) runSequential(rc RunCtx, env RuntimeEnv) []Result {
	var results []Result
	for i, s := range p.Steps {
		if p.PreDispatch != nil {
			if err := p.PreDispatch(); err != nil {
				for _, skip := range p.Steps[i:] {
					results = append(results, skippedDispatchResult(skip, env, err))
				}
				return results
			}
		}
		results = append(results, s.execute(rc, env)...)
	}
	return results
}

// runConcurrently fans the steps across `workers` goroutines with a
// semaphore-bounded pool. PreDispatch errors short-circuit the dispatch
// loop with skipped results for remaining steps. Each worker recovers
// from panics so a panic in one step doesn't tear down its siblings.
func (p Parallel) runConcurrently(rc RunCtx, env RuntimeEnv, workers int) []Result {
	var (
		mu      sync.Mutex
		results []Result
		wg      sync.WaitGroup
	)
	emit := func(rs ...Result) {
		mu.Lock()
		results = append(results, rs...)
		mu.Unlock()
	}

	sem := make(chan struct{}, workers)
	for i, s := range p.Steps {
		if p.PreDispatch != nil {
			if err := p.PreDispatch(); err != nil {
				for _, skip := range p.Steps[i:] {
					emit(skippedDispatchResult(skip, env, err))
				}
				break
			}
		}
		s := s
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				if rv := recover(); rv != nil {
					emit(panicResult(s, env, rv))
				}
			}()
			emit(s.execute(rc, env)...)
		}()
	}
	wg.Wait()
	return results
}

// skippedDispatchResult builds a Skip Result for a Step that PreDispatch
// refused to dispatch. The reason carries the PreDispatch error message
// so cmd-layer reductions can surface "skipped: budget reached" etc.
// Bundle is populated when the step is a PromptBundle so consumers that
// reach for r.Bundle.Name (failure summaries, debugging) don't nil-deref.
func skippedDispatchResult(s Step, env RuntimeEnv, cause error) Result {
	return Result{
		Bundle: bundleOfStep(s),
		Env:    env,
		Flow: Flow{
			State:  StateSkip,
			Reason: fmt.Sprintf("not dispatched: %v", cause),
		},
	}
}

// panicResult builds an Error Result from a recovered panic. The stack
// is captured into Reason so the panic stays diagnosable in the cmd
// layer's failure summary. Mirrors runner.RunPool's panic recovery.
func panicResult(s Step, env RuntimeEnv, rv any) Result {
	err := fmt.Errorf("panic in step %q: %v", s.name(), rv)
	return Result{
		Bundle: bundleOfStep(s),
		Env:    env,
		Flow: Flow{
			State:  StateError,
			Reason: fmt.Sprintf("panic: %v\n\n%s", rv, debug.Stack()),
			Err:    err,
		},
	}
}

// bundleOfStep returns a pointer to the underlying PromptBundle if s is
// one, else a placeholder bundle carrying only the step name. Used by
// the skip/panic result constructors so Result.Bundle is never nil.
func bundleOfStep(s Step) *PromptBundle {
	if b, ok := s.(PromptBundle); ok {
		return &b
	}
	return &PromptBundle{Name: s.name()}
}

// ============================================================
// Helpers
// ============================================================

func hasError(rs []Result) bool {
	for _, r := range rs {
		if r.Flow.State == StateError {
			return true
		}
	}
	return false
}

// tallyResults walks rs, incrementing so's counters per Result.Flow.State.
func (so *StageOutcome) tallyResults(rs []Result) {
	for _, r := range rs {
		switch r.Flow.State {
		case StateContinue:
			so.Succeeded++
		case StateSkip:
			so.Skipped++
		case StateError:
			so.Failed++
		}
	}
}

func stageOutcomeFromPipeline(pr PipelineResult) StageOutcome {
	so := StageOutcome{FirstErrorIndex: pr.FirstErrorIndex}
	for _, s := range pr.Steps {
		if s.Skipped {
			so.Skipped++
			continue
		}
		so.tallyResults(s.Results)
	}
	return so
}

func stageOutcomeFromResults(rs []Result) StageOutcome {
	so := StageOutcome{FirstErrorIndex: -1}
	so.tallyResults(rs)
	return so
}

// actionTypeName returns the Go type name of an Action for the
// `action_type` field in Reporter.ActionStart / ActionEnd callbacks.
// Used by BundleLogReporter to emit `action_type: "CheckConcurrentRuns"`
// etc. into bundle.jsonl. Pointer receivers are unwrapped.
func actionTypeName(a Action) string {
	t := reflect.TypeOf(a)
	if t == nil {
		return ""
	}
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t.Name()
}
