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
	"sync"

	"github.com/ateam/internal/calldb"
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
// onProgress is invoked synchronously for each RunProgress event and may
// fire from multiple internal goroutines; the callback owns its own
// thread-safety. Nil disables progress reporting.
type Executor interface {
	Execute(ctx context.Context, prompt string, opts runner.RunOpts, onProgress func(runner.RunProgress)) runner.RunSummary
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
}

// Run is the top-level entry. Always returns PipelineResult — a top-level
// PromptBundle or Parallel produces a one-step result.
func Run(top Step, env RuntimeEnv, rc RunCtx) PipelineResult {
	if rc.Reporter == nil {
		rc.Reporter = NoopReporter{}
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
// constructs one per call; closures capture cmd-layer state for Render
// and RunOpts.
type PromptBundle struct {
	Name   string
	Role   string      // optional override of env.Role
	Action string      // optional override of env.Action
	Env    *RuntimeEnv // optional override of parent's env (used for per-role profile)

	Render   func(env RuntimeEnv) (string, error)
	RunOpts  func(env RuntimeEnv) runner.RunOpts
	PreExec  []Action
	PostExec []Action
}

func (b PromptBundle) name() string { return b.Name }

func (b PromptBundle) execute(rc RunCtx, env RuntimeEnv) []Result {
	if b.Env != nil {
		env = *b.Env
	}
	env = env.withBundleOverrides(b.Role, b.Action)

	bi := BundleInfo{Name: b.Name, Role: env.Role, Action: env.Action}
	rc.Reporter.BundleStart(bi)

	emit := func(r Result) []Result {
		rc.Reporter.BundleEnd(bi, r)
		return []Result{r}
	}

	for _, a := range b.PreExec {
		f := a.Run(rc, env, nil)
		if f.State != StateContinue {
			return emit(Result{Bundle: &b, Env: env, Flow: f})
		}
	}

	prompt, err := b.Render(env)
	if err != nil {
		return emit(Result{Bundle: &b, Env: env, Flow: Flow{
			State: StateError, Reason: "render failed", Err: err,
		}})
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
		return emit(Result{Bundle: &b, Env: env, Flow: Flow{
			State: StateContinue, Reason: "dry-run",
		}})
	}

	summary := env.Executor.Execute(rc.Ctx, prompt, opts, func(p runner.RunProgress) {
		rc.Reporter.AgentEvent(bi, p)
	})

	flow := Flow{State: StateContinue}
	if summary.IsError {
		flow = Flow{State: StateError, Reason: summary.ErrorCause, Err: summary.Err}
	}
	r := Result{Bundle: &b, Env: env, Flow: flow, Summary: &summary}

	if r.Flow.State == StateContinue {
		for _, a := range b.PostExec {
			pf := a.Run(rc, env, &r)
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
type Parallel struct {
	Name    string
	Steps   []Step
	Env     *RuntimeEnv
	Workers int
}

func (p Parallel) name() string { return p.Name }

func (p Parallel) execute(rc RunCtx, env RuntimeEnv) []Result {
	if p.Env != nil {
		env = *p.Env
	}
	si := StageInfo{Kind: StageParallel, Name: p.Name, Children: len(p.Steps)}
	rc.Reporter.StageStart(si)

	var results []Result
	switch {
	case env.DryRun, len(p.Steps) <= 1:
		for _, s := range p.Steps {
			results = append(results, s.execute(rc, env)...)
		}
	default:
		workers := p.Workers
		if workers <= 0 {
			workers = minInt(len(p.Steps), 5)
		}
		results = runConcurrently(rc, env, p.Steps, workers)
	}

	rc.Reporter.StageEnd(si, stageOutcomeFromResults(results))
	return results
}

func runConcurrently(rc RunCtx, env RuntimeEnv, steps []Step, workers int) []Result {
	var (
		mu      sync.Mutex
		results []Result
		wg      sync.WaitGroup
	)
	sem := make(chan struct{}, workers)
	for _, s := range steps {
		s := s
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			rs := s.execute(rc, env)
			mu.Lock()
			results = append(results, rs...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	return results
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

func stageOutcomeFromPipeline(pr PipelineResult) StageOutcome {
	so := StageOutcome{FirstErrorIndex: pr.FirstErrorIndex}
	for _, s := range pr.Steps {
		if s.Skipped {
			so.Skipped++
			continue
		}
		for _, r := range s.Results {
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
	return so
}

func stageOutcomeFromResults(rs []Result) StageOutcome {
	so := StageOutcome{FirstErrorIndex: -1}
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
	return so
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
