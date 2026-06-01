package flow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ateam/internal/runner"
)

// ============================================================
// PromptBundle
// ============================================================

func TestPromptBundle_HappyPath(t *testing.T) {
	exec := &fakeExecutor{Summary: runner.RunSummary{RoleID: "tester", Duration: 5 * time.Millisecond}}
	rc := newCtx()
	env := newEnv(exec)

	b := makeBundle("verify", nil)
	out := Run(b, env, rc)

	if len(out.Steps) != 1 || len(out.Steps[0].Results) != 1 {
		t.Fatalf("expected 1 result, got %#v", out)
	}
	r := out.Steps[0].Results[0]
	if r.Flow.State != StateContinue {
		t.Errorf("flow state: got %v want continue", r.Flow.State)
	}
	if r.Summary == nil {
		t.Fatal("expected Summary populated")
	}
	if calls := exec.Calls(); len(calls) != 1 || calls[0].Prompt != "hello" {
		t.Errorf("executor calls: %#v", calls)
	}
}

func TestPromptBundle_PreSkip(t *testing.T) {
	exec := &fakeExecutor{}
	rc := newCtx()
	env := newEnv(exec)

	b := makeBundle("verify", nil)
	b.PreExec = []Action{
		funcAction(func(RunCtx, RuntimeEnv, *Result) Flow {
			return Flow{State: StateSkip, Reason: "nothing to do"}
		}),
	}
	b.PostExec = []Action{
		funcAction(func(RunCtx, RuntimeEnv, *Result) Flow {
			t.Error("Post should not run when Pre skipped")
			return Flow{State: StateContinue}
		}),
	}
	out := Run(b, env, rc)

	r := out.Steps[0].Results[0]
	if r.Flow.State != StateSkip || r.Flow.Reason != "nothing to do" {
		t.Errorf("flow: %#v", r.Flow)
	}
	if len(exec.Calls()) != 0 {
		t.Error("Execute should not run when Pre skipped")
	}
}

func TestPromptBundle_PreError(t *testing.T) {
	exec := &fakeExecutor{}
	rc := newCtx()
	env := newEnv(exec)
	preErr := errors.New("concurrent run")

	b := makeBundle("verify", nil)
	b.PreExec = []Action{
		funcAction(func(RunCtx, RuntimeEnv, *Result) Flow {
			return Flow{State: StateError, Err: preErr}
		}),
	}
	b.PostExec = []Action{
		funcAction(func(RunCtx, RuntimeEnv, *Result) Flow {
			t.Error("Post should not run on Pre error")
			return Flow{State: StateContinue}
		}),
	}
	out := Run(b, env, rc)

	r := out.Steps[0].Results[0]
	if r.Flow.State != StateError || !errors.Is(r.Flow.Err, preErr) {
		t.Errorf("flow: %#v", r.Flow)
	}
	if len(exec.Calls()) != 0 {
		t.Error("Execute should not run on Pre error")
	}
	if out.FirstErrorIndex != 0 {
		t.Errorf("FirstErrorIndex: got %d want 0", out.FirstErrorIndex)
	}
}

func TestPromptBundle_RenderError(t *testing.T) {
	exec := &fakeExecutor{}
	rc := newCtx()
	env := newEnv(exec)
	renderErr := errors.New("template failed")

	b := makeBundle("verify", func(RuntimeEnv) (string, error) { return "", renderErr })
	out := Run(b, env, rc)

	r := out.Steps[0].Results[0]
	if r.Flow.State != StateError || !errors.Is(r.Flow.Err, renderErr) {
		t.Errorf("flow: %#v", r.Flow)
	}
	if len(exec.Calls()) != 0 {
		t.Error("Execute should not run when Render errored")
	}
}

func TestPromptBundle_ExecutorError(t *testing.T) {
	execErr := errors.New("agent crashed")
	exec := &fakeExecutor{Summary: runner.RunSummary{
		IsError: true, Err: execErr, ErrorCause: "agent crashed",
	}}
	rc := newCtx()
	env := newEnv(exec)

	var postRan atomic.Bool
	b := makeBundle("verify", nil)
	b.PostExec = []Action{
		funcAction(func(_ RunCtx, _ RuntimeEnv, _ *Result) Flow {
			postRan.Store(true)
			return Flow{State: StateContinue}
		}),
	}
	out := Run(b, env, rc)

	if postRan.Load() {
		t.Error("Post should NOT run when agent errored (parity with stage's FailOnExecError gate)")
	}
	r := out.Steps[0].Results[0]
	if r.Flow.State != StateError {
		t.Errorf("flow state: got %v want error", r.Flow.State)
	}
	if r.Summary == nil {
		t.Error("Summary should still be populated on error")
	}
}

func TestPromptBundle_DryRun(t *testing.T) {
	exec := &fakeExecutor{}
	rc := newCtx()
	env := newEnv(exec)
	env.DryRun = true

	b := makeBundle("verify", nil)
	out := Run(b, env, rc)

	r := out.Steps[0].Results[0]
	if r.Flow.State != StateContinue || r.Flow.Reason != "dry-run" {
		t.Errorf("flow: %#v", r.Flow)
	}
	if len(exec.Calls()) != 0 {
		t.Error("Execute should not run under dry-run")
	}
}

func TestPromptBundle_EnvOverride(t *testing.T) {
	exec := &fakeExecutor{}
	rc := newCtx()
	parent := newEnv(exec)
	parent.Role = "parent-role"

	override := newEnv(exec)
	override.Role = "override-role"

	b := makeBundle("verify", func(env RuntimeEnv) (string, error) {
		if env.Role != "override-role" {
			t.Errorf("Render saw env.Role = %q, expected override-role", env.Role)
		}
		return "ok", nil
	})
	b.Env = &override
	Run(b, parent, rc)
}

// ============================================================
// Pipeline
// ============================================================

func TestPipeline_SequentialOrder(t *testing.T) {
	exec := &fakeExecutor{Summary: runner.RunSummary{}}
	rc := newCtx()
	env := newEnv(exec)

	pipe := Pipeline{
		Name: "seq",
		Steps: []Step{
			makeBundle("a", nil),
			makeBundle("b", nil),
			makeBundle("c", nil),
		},
	}
	out := Run(pipe, env, rc)
	if len(out.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(out.Steps))
	}
	names := []string{out.Steps[0].Name, out.Steps[1].Name, out.Steps[2].Name}
	if got, want := strings.Join(names, ","), "a,b,c"; got != want {
		t.Errorf("step order: got %q want %q", got, want)
	}
	if got := len(exec.Calls()); got != 3 {
		t.Errorf("executor calls: got %d want 3", got)
	}
}

func TestPipeline_StopOnError(t *testing.T) {
	rec := &recordingReporter{}
	exec := &errOnceExecutor{errAt: 0}
	rc := RunCtx{Ctx: newCtx().Ctx, Reporter: rec}
	env := newEnv(exec)

	pipe := Pipeline{
		Name: "seq",
		Steps: []Step{
			makeBundle("a", nil),
			makeBundle("b", nil),
			makeBundle("c", nil),
		},
	}
	out := Run(pipe, env, rc)
	if len(out.Steps) != 3 {
		t.Fatalf("expected 3 step outcomes, got %d", len(out.Steps))
	}
	if out.FirstErrorIndex != 0 {
		t.Errorf("FirstErrorIndex: got %d want 0", out.FirstErrorIndex)
	}
	if !out.Steps[1].Skipped || !out.Steps[2].Skipped {
		t.Errorf("expected steps 1,2 to be Skipped: %#v", out.Steps[1:])
	}
	if exec.calls != 1 {
		t.Errorf("Execute should run only once before stop, got %d", exec.calls)
	}
}

func TestPipeline_SkipDoesNotStop(t *testing.T) {
	exec := &fakeExecutor{}
	rc := newCtx()
	env := newEnv(exec)

	skipping := makeBundle("skip", nil)
	skipping.PreExec = []Action{
		funcAction(func(RunCtx, RuntimeEnv, *Result) Flow {
			return Flow{State: StateSkip, Reason: "no work"}
		}),
	}
	pipe := Pipeline{
		Name: "seq",
		Steps: []Step{
			skipping,
			makeBundle("b", nil),
			makeBundle("c", nil),
		},
	}
	out := Run(pipe, env, rc)
	if out.FirstErrorIndex != -1 {
		t.Errorf("FirstErrorIndex on no-error pipeline: got %d want -1", out.FirstErrorIndex)
	}
	for _, s := range out.Steps {
		if s.Skipped {
			t.Errorf("no step should be Skipped after a skip-flowing leaf: %#v", s)
		}
	}
	if got := len(exec.Calls()); got != 2 {
		t.Errorf("expected 2 Execute calls (b, c) after skip(a); got %d", got)
	}
}

func TestPipeline_StepSkippedReporterFires(t *testing.T) {
	rec := &recordingReporter{}
	exec := &errOnceExecutor{errAt: 0}
	rc := RunCtx{Ctx: newCtx().Ctx, Reporter: rec}
	env := newEnv(exec)

	pipe := Pipeline{
		Name: "seq",
		Steps: []Step{
			makeBundle("a", nil),
			makeBundle("b", nil),
			makeBundle("c", nil),
		},
	}
	Run(pipe, env, rc)

	if got := rec.countOf("StepSkipped"); got != 2 {
		t.Errorf("StepSkipped count: got %d want 2 (b, c)", got)
	}
}

// ============================================================
// Parallel
// ============================================================

func TestParallel_RunsConcurrently(t *testing.T) {
	exec := &fakeExecutor{Delay: 50 * time.Millisecond}
	rc := newCtx()
	env := newEnv(exec)

	par := Parallel{
		Name:    "fan",
		Workers: 4,
		Steps: []Step{
			makeBundle("a", nil),
			makeBundle("b", nil),
			makeBundle("c", nil),
			makeBundle("d", nil),
		},
	}
	start := time.Now()
	out := Run(par, env, rc)
	elapsed := time.Since(start)

	if elapsed > 200*time.Millisecond {
		t.Errorf("4 parallel runs of 50ms took %v; expected ~50ms, not serialised", elapsed)
	}
	if got := len(out.Steps[0].Results); got != 4 {
		t.Errorf("expected 4 leaf results, got %d", got)
	}
}

func TestParallel_WorkersBound(t *testing.T) {
	concurrent := &maxConcurrentExecutor{Delay: 20 * time.Millisecond}
	rc := newCtx()
	env := newEnv(concurrent)

	par := Parallel{
		Name:    "fan",
		Workers: 2,
		Steps: []Step{
			makeBundle("a", nil),
			makeBundle("b", nil),
			makeBundle("c", nil),
			makeBundle("d", nil),
			makeBundle("e", nil),
		},
	}
	Run(par, env, rc)
	if got := concurrent.peak(); got > 2 {
		t.Errorf("peak concurrency: got %d, want <= 2", got)
	}
}

func TestParallel_AllRunEvenIfOneErrors(t *testing.T) {
	exec := &errOnceExecutor{errAt: 1}
	rc := newCtx()
	env := newEnv(exec)

	par := Parallel{
		Name: "fan",
		Steps: []Step{
			makeBundle("a", nil),
			makeBundle("b", nil),
			makeBundle("c", nil),
		},
	}
	out := Run(par, env, rc)
	if got := exec.calls; got != 3 {
		t.Errorf("expected all 3 leaves to Execute, got %d", got)
	}
	if got := len(out.Steps[0].Results); got != 3 {
		t.Errorf("expected 3 leaf results, got %d", got)
	}
}

func TestParallel_PreDispatchSkipsRemaining(t *testing.T) {
	exec := &fakeExecutor{}
	rc := newCtx()
	env := newEnv(exec)

	// Allow the first 2 dispatches, then refuse.
	calls := 0
	preErr := errors.New("budget exhausted")
	par := Parallel{
		Name:    "fan",
		Workers: 4,
		Steps: []Step{
			makeBundle("a", nil),
			makeBundle("b", nil),
			makeBundle("c", nil),
			makeBundle("d", nil),
			makeBundle("e", nil),
		},
		PreDispatch: func() error {
			calls++
			if calls > 2 {
				return preErr
			}
			return nil
		},
	}
	out := Run(par, env, rc)
	if got := len(out.Steps[0].Results); got != 5 {
		t.Fatalf("expected 5 results (2 ran + 3 skipped), got %d", got)
	}

	succeeded, skipped := 0, 0
	skippedNames := map[string]bool{}
	for _, r := range out.Steps[0].Results {
		switch r.Flow.State {
		case StateContinue:
			succeeded++
		case StateSkip:
			skipped++
			if !strings.Contains(r.Flow.Reason, "budget exhausted") {
				t.Errorf("expected skip reason to carry PreDispatch error; got %q", r.Flow.Reason)
			}
			if r.Bundle == nil || r.Bundle.Name == "" {
				t.Errorf("skipped result missing Bundle; got %#v", r.Bundle)
			} else {
				skippedNames[r.Bundle.Name] = true
			}
		}
	}
	if succeeded != 2 || skipped != 3 {
		t.Errorf("got %d succeeded / %d skipped; want 2 / 3", succeeded, skipped)
	}
	for _, want := range []string{"c", "d", "e"} {
		if !skippedNames[want] {
			t.Errorf("expected skipped Bundle.Name %q, got set %v", want, skippedNames)
		}
	}
	if exec.Calls()[0].Prompt != "hello" {
		t.Errorf("agent should have been invoked for the 2 dispatched bundles")
	}
}

func TestParallel_PreDispatchHonoredSequentialPath(t *testing.T) {
	// Sequential path (DryRun OR len(Steps)<=1) must also honor PreDispatch.
	exec := &fakeExecutor{}
	rc := newCtx()
	env := newEnv(exec)
	env.DryRun = true

	calls := 0
	par := Parallel{
		Name: "fan",
		Steps: []Step{
			makeBundle("a", nil), makeBundle("b", nil), makeBundle("c", nil),
		},
		PreDispatch: func() error {
			calls++
			if calls > 1 {
				return errors.New("stop")
			}
			return nil
		},
	}
	out := Run(par, env, rc)
	skipped := 0
	for _, r := range out.Steps[0].Results {
		if r.Flow.State == StateSkip && strings.Contains(r.Flow.Reason, "stop") {
			skipped++
		}
	}
	if skipped != 2 {
		t.Errorf("sequential PreDispatch: got %d skipped, want 2", skipped)
	}
}

func TestParallel_PanicRecovered(t *testing.T) {
	exec := &fakeExecutor{}
	rc := newCtx()
	env := newEnv(exec)

	panicking := PromptBundle{
		Name: "boom",
		Render: func(RuntimeEnv) (string, error) {
			panic("synthetic panic")
		},
		RunOpts: func(RuntimeEnv) runner.RunOpts { return runner.RunOpts{} },
	}
	par := Parallel{
		Name:    "fan",
		Workers: 4,
		Steps: []Step{
			makeBundle("a", nil),
			panicking,
			makeBundle("c", nil),
		},
	}
	out := Run(par, env, rc)
	if got := len(out.Steps[0].Results); got != 3 {
		t.Fatalf("expected 3 results despite panic, got %d", got)
	}
	sawPanic := false
	succeeded := 0
	for _, r := range out.Steps[0].Results {
		switch r.Flow.State {
		case StateError:
			if strings.Contains(r.Flow.Reason, "panic") {
				sawPanic = true
				if r.Bundle == nil || r.Bundle.Name != "boom" {
					t.Errorf("panic result Bundle: got %#v want Name=boom", r.Bundle)
				}
			}
		case StateContinue:
			succeeded++
		}
	}
	if !sawPanic {
		t.Error("expected an Error result with 'panic' in reason")
	}
	if succeeded != 2 {
		t.Errorf("siblings should still run after panic; got %d succeeded", succeeded)
	}
}

// TestParallel_PanicEmitsBundleEnd verifies that a panic inside a
// PromptBundle's Render still pairs BundleStart with BundleEnd carrying
// the Error Result, so reporters (e.g. tableReporter) can count it.
func TestParallel_PanicEmitsBundleEnd(t *testing.T) {
	exec := &fakeExecutor{}
	rep := &recordingReporter{}
	rc := RunCtx{Ctx: context.Background(), Reporter: rep}
	env := newEnv(exec)

	panicking := PromptBundle{
		Name:    "boom",
		Render:  func(RuntimeEnv) (string, error) { panic("synthetic panic") },
		RunOpts: func(RuntimeEnv) runner.RunOpts { return runner.RunOpts{} },
	}
	par := Parallel{
		Name:    "fan",
		Workers: 4,
		Steps: []Step{
			makeBundle("a", nil),
			panicking,
			makeBundle("c", nil),
		},
	}
	Run(par, env, rc)

	if got, want := rep.countOf("BundleStart"), 3; got != want {
		t.Errorf("BundleStart count: got %d want %d", got, want)
	}
	if got, want := rep.countOf("BundleEnd"), 3; got != want {
		t.Errorf("BundleEnd count: got %d want %d — panic skipped reporter", got, want)
	}

	sawPanicEnd := false
	rep.mu.Lock()
	for _, e := range rep.Events {
		if e.Kind != "BundleEnd" || e.BundleInfo.Name != "boom" {
			continue
		}
		if e.Result.Flow.State == StateError && strings.Contains(e.Result.Flow.Reason, "panic") {
			sawPanicEnd = true
		}
	}
	rep.mu.Unlock()
	if !sawPanicEnd {
		t.Error("expected BundleEnd for 'boom' with StateError + panic reason")
	}
}

func TestParallel_DryRun(t *testing.T) {
	exec := &fakeExecutor{}
	rc := newCtx()
	env := newEnv(exec)
	env.DryRun = true

	par := Parallel{
		Name:    "fan",
		Workers: 4,
		Steps: []Step{
			makeBundle("a", nil),
			makeBundle("b", nil),
			makeBundle("c", nil),
		},
	}
	Run(par, env, rc)
	if got := len(exec.Calls()); got != 0 {
		t.Errorf("DryRun should not Execute; got %d calls", got)
	}
}

// ============================================================
// Run() shapes
// ============================================================

func TestRun_TopLevelSingleBundle(t *testing.T) {
	exec := &fakeExecutor{}
	out := Run(makeBundle("solo", nil), newEnv(exec), newCtx())
	if len(out.Steps) != 1 {
		t.Errorf("expected 1 step (wrapped), got %d", len(out.Steps))
	}
	if got, want := out.Steps[0].Name, "solo"; got != want {
		t.Errorf("step name: got %q want %q", got, want)
	}
}

func TestRun_TopLevelParallel(t *testing.T) {
	exec := &fakeExecutor{}
	par := Parallel{Name: "fan", Steps: []Step{makeBundle("a", nil), makeBundle("b", nil)}}
	out := Run(par, newEnv(exec), newCtx())
	if len(out.Steps) != 1 {
		t.Errorf("expected 1 wrapping step, got %d", len(out.Steps))
	}
	if got := len(out.Steps[0].Results); got != 2 {
		t.Errorf("expected 2 leaf results, got %d", got)
	}
}

func TestRun_TopLevelPipelineGetsRunDetailed(t *testing.T) {
	exec := &fakeExecutor{}
	pipe := Pipeline{Name: "p", Steps: []Step{makeBundle("a", nil), makeBundle("b", nil)}}
	out := Run(pipe, newEnv(exec), newCtx())
	if len(out.Steps) != 2 {
		t.Errorf("expected 2 step outcomes, got %d", len(out.Steps))
	}
}

func TestPipelineResult_FirstError(t *testing.T) {
	wantErr := errors.New("boom")
	pr := PipelineResult{
		Steps: []StepOutcome{
			{Results: []Result{{Flow: Flow{State: StateContinue}}}},
			{Results: []Result{{Flow: Flow{State: StateError, Err: wantErr}}}},
		},
	}
	if got := pr.FirstError(); !errors.Is(got, wantErr) {
		t.Errorf("FirstError: got %v want %v", got, wantErr)
	}
	empty := PipelineResult{Steps: []StepOutcome{{Results: []Result{{Flow: Flow{State: StateContinue}}}}}}
	if got := empty.FirstError(); got != nil {
		t.Errorf("FirstError on success: got %v want nil", got)
	}
}

func TestPipelineResult_FirstErrorIndex(t *testing.T) {
	exec := &errOnceExecutor{errAt: 1}
	pipe := Pipeline{
		Steps: []Step{
			makeBundle("a", nil),
			makeBundle("b", nil),
			makeBundle("c", nil),
		},
	}
	out := Run(pipe, newEnv(exec), newCtx())
	if out.FirstErrorIndex != 1 {
		t.Errorf("FirstErrorIndex: got %d want 1", out.FirstErrorIndex)
	}
	clean := Pipeline{Steps: []Step{makeBundle("a", nil), makeBundle("b", nil)}}
	out2 := Run(clean, newEnv(&fakeExecutor{}), newCtx())
	if out2.FirstErrorIndex != -1 {
		t.Errorf("FirstErrorIndex on clean: got %d want -1", out2.FirstErrorIndex)
	}
}

func TestRunCtx_ReporterDefault(t *testing.T) {
	// nil Reporter must not panic — Run() should swap in NoopReporter.
	exec := &fakeExecutor{}
	rc := RunCtx{Ctx: newCtx().Ctx}
	Run(makeBundle("verify", nil), newEnv(exec), rc)
}

// ============================================================
// Test-only support types
// ============================================================

// funcAction adapts a function into a flow.Action for inline test usage.
type funcAction func(rc RunCtx, env RuntimeEnv, res *Result) Flow

func (f funcAction) Run(rc RunCtx, env RuntimeEnv, res *Result) Flow {
	return f(rc, env, res)
}

// errOnceExecutor returns an IsError summary for the call at index errAt;
// other calls return success. Threadsafe; tracks Calls atomically.
type errOnceExecutor struct {
	errAt int
	calls int
	mu    sync.Mutex
}

func (e *errOnceExecutor) Prepare(opts runner.RunOpts, _ string) (*runner.PreparedRun, error) {
	return &runner.PreparedRun{Opts: opts}, nil
}

func (e *errOnceExecutor) ExecutePrepared(ctx context.Context, prepared *runner.PreparedRun, prompt string, onProgress func(runner.RunProgress)) runner.RunSummary {
	e.mu.Lock()
	idx := e.calls
	e.calls++
	e.mu.Unlock()
	if idx == e.errAt {
		return runner.RunSummary{
			IsError:    true,
			Err:        fmt.Errorf("errOnceExecutor: call %d failed", idx),
			ErrorCause: "synthetic error",
		}
	}
	return runner.RunSummary{}
}

// maxConcurrentExecutor tracks the peak number of concurrent Execute calls.
// Used to verify Parallel respects its Workers cap.
type maxConcurrentExecutor struct {
	Delay   time.Duration
	mu      sync.Mutex
	current int
	peakN   int
}

func (e *maxConcurrentExecutor) Prepare(opts runner.RunOpts, _ string) (*runner.PreparedRun, error) {
	return &runner.PreparedRun{Opts: opts}, nil
}

func (e *maxConcurrentExecutor) ExecutePrepared(ctx context.Context, prepared *runner.PreparedRun, prompt string, onProgress func(runner.RunProgress)) runner.RunSummary {
	e.mu.Lock()
	e.current++
	if e.current > e.peakN {
		e.peakN = e.current
	}
	e.mu.Unlock()
	if e.Delay > 0 {
		time.Sleep(e.Delay)
	}
	e.mu.Lock()
	e.current--
	e.mu.Unlock()
	return runner.RunSummary{}
}

func (e *maxConcurrentExecutor) peak() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.peakN
}
