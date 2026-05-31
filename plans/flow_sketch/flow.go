//go:build sketch

// Package flow sketches the composition framework that replaces internal/stage.
// Not built in normal builds (see //go:build sketch). Reviewing scaffold only —
// types and dispatch shapes, no error-path completeness.
//
// Design points decided in plans/Feature_prompt_report_fs_refactor_impl_steps.md
// Phase G discussion:
//   - RunCtx is (Ctx, DB, Reporter). No progress chan.
//   - RuntimeEnv is "where & how to invoke the agent"; freely re-bound at
//     Pipeline/Parallel/Bundle boundaries via the optional Env field.
//   - Step is internal; cmd-layer calls flow.Run() at the top.
//   - Pipeline stops on first errored step; skip counts as success.
//   - Reporter is the single observability surface; nested-Parallel rendering
//     is a Reporter concern, not a framework concern.
package flow

import (
	"context"
	"sync"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
)

// ====== Carriers ======

// RunCtx is the execution-side carrier — cancellation, DB handle, the
// resolved ateam env (project/org/config), and the reporter. Same shape
// across every Step.Execute call; freely passed by value.
//
// Resolved is here (not on RuntimeEnv) because it's session-scoped like DB:
// opened once per cmd, threaded through every action that needs project /
// org / config access. Keeping it off RuntimeEnv preserves RuntimeEnv's
// "freely rebound at composition boundaries" property.
type RunCtx struct {
	Ctx      context.Context
	DB       *calldb.CallDB
	Resolved *root.ResolvedEnv
	Reporter Reporter // never nil after Run() entry; defaulted to NoopReporter
}

// RuntimeEnv is "where & how to run" — config that the bundle's Render and
// the AgentExecutor see at exec time. Freely rebound by Pipeline.Env /
// Parallel.Env / PromptBundle.Env at composition boundaries.
type RuntimeEnv struct {
	Executor *runner.AgentExecutor
	WorkDir  string
	Role     string // default for bundles that don't override
	Action   string // default for bundles that don't override
	DryRun   bool
	Batch    string

	// PromptDir is consumed by Render closures that need to know where
	// the assembler should look. Optional.
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

// ====== Flow & Result ======

type FlowState int

const (
	StateContinue FlowState = iota
	StateSkip
	StateError
)

type Flow struct {
	State  FlowState
	Reason string
	Err    error // populated when StateError
}

// Result is per-leaf. One PromptBundle execution → one Result.
type Result struct {
	Bundle  *PromptBundle
	Env     RuntimeEnv
	Flow    Flow
	Summary *runner.RunSummary // nil for skipped / pre-error
}

// StepOutcome is one entry in PipelineResult.Steps. Skipped is true iff a
// prior step in the same Pipeline errored and this step never executed.
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

// FirstError returns the first leaf error across all run steps, or nil.
// Convenience for cmd-layer "translate framework result to error return".
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

// ====== Step interface and top-level Run ======

// Step is implemented by PromptBundle, Parallel, Pipeline. Internal —
// callers go through Run() so PipelineResult shape is uniform at the top.
type Step interface {
	execute(rc RunCtx, env RuntimeEnv) []Result
	name() string
}

// Run is the top-level entry. Always returns PipelineResult.
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

// ====== Action ======

// Action is the common shape for PreExec / PostExec hooks on PromptBundle.
// Pre actions returning StateSkip end the bundle successfully; StateError
// aborts (no Render, no agent, no Post). Post actions see Result.Summary
// already populated; their non-StateContinue return upgrades the Result's
// Flow if it was Continue.
type Action interface {
	Run(rc RunCtx, env RuntimeEnv) Flow
}

// ====== PromptBundle (leaf) ======

type PromptBundle struct {
	Name   string
	Role   string // optional override of env.Role
	Action string // optional override of env.Action
	Env    *RuntimeEnv

	Render  func(env RuntimeEnv) (string, error)
	RunOpts func(env RuntimeEnv) runner.RunOpts

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
		f := a.Run(rc, env)
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

	progCh := openBundleProgress(rc, bi)
	summary := env.Executor.Execute(rc.Ctx, prompt, opts, progCh)
	closeBundleProgress(progCh)

	flow := Flow{State: StateContinue}
	if summary.IsError {
		flow = Flow{State: StateError, Reason: summary.ErrorCause, Err: summary.Err}
	}
	r := Result{Bundle: &b, Env: env, Flow: flow, Summary: &summary}

	for _, a := range b.PostExec {
		pf := a.Run(rc, env)
		if pf.State == StateError && r.Flow.State == StateContinue {
			r.Flow = pf
		}
	}

	return emit(r)
}

// openBundleProgress allocates a per-bundle progress chan, spawns a
// goroutine that forwards each RunProgress event to rc.Reporter.AgentEvent,
// and returns the write end. Implementation elided; the runner machinery
// is the same as today's stage.Ctx.Progress, just with a Reporter at the
// drain end instead of an inline display routine.
func openBundleProgress(rc RunCtx, bi BundleInfo) chan<- runner.RunProgress { return nil }
func closeBundleProgress(ch chan<- runner.RunProgress)                      {}

// ====== Pipeline ======

type Pipeline struct {
	Name  string
	Steps []Step
	Env   *RuntimeEnv
}

func (p Pipeline) name() string { return p.Name }

// execute exposes Pipeline as a Step in a parent Pipeline / Parallel.
// Inner step boundaries are NOT visible to the parent — they're collapsed
// to a flat []Result. PipelineResult granularity only exists at the level
// where Pipeline is the top of the call to Run().
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

// ====== Parallel ======

type Parallel struct {
	Name    string
	Steps   []Step
	Env     *RuntimeEnv
	Workers int // 0 = min(len(Steps), 5)
}

func (p Parallel) name() string { return p.Name }

func (p Parallel) execute(rc RunCtx, env RuntimeEnv) []Result {
	if p.Env != nil {
		env = *p.Env
	}
	si := StageInfo{Kind: StageParallel, Name: p.Name, Children: len(p.Steps)}
	rc.Reporter.StageStart(si)
	defer func() {}() // see StageEnd call below

	var results []Result
	switch {
	case env.DryRun, len(p.Steps) <= 1:
		for _, s := range p.Steps {
			results = append(results, s.execute(rc, env)...)
		}
	default:
		workers := p.Workers
		if workers == 0 {
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

// ====== Helpers ======

func hasError(rs []Result) bool {
	for _, r := range rs {
		if r.Flow.State == StateError {
			return true
		}
	}
	return false
}

func stageOutcomeFromPipeline(pr PipelineResult) StageOutcome {
	var so StageOutcome
	so.FirstErrorIndex = pr.FirstErrorIndex
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
