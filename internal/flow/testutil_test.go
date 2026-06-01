package flow

import (
	"context"
	"sync"
	"time"

	"github.com/ateam/internal/runner"
)

// ============================================================
// fakeExecutor
// ============================================================

// fakeExecutor implements Executor without needing a real *runner.AgentExecutor.
// Configure Summary, Delay, or Events to drive happy/error/concurrency tests.
//
// PrepareErr, if non-nil, causes Prepare to fail — letting tests exercise
// the PromptBundle.execute "prepare failed" Error branch.
type fakeExecutor struct {
	Summary    runner.RunSummary
	Delay      time.Duration
	Events     []runner.RunProgress
	PrepareErr error

	mu      sync.Mutex
	calls   []fakeCall
	nextID  int64
	prepped int64 // count of successful Prepare calls
}

type fakeCall struct {
	Prompt string
	Opts   runner.RunOpts
}

func (f *fakeExecutor) Prepare(opts runner.RunOpts, _ string) (*runner.PreparedRun, error) {
	if f.PrepareErr != nil {
		return nil, f.PrepareErr
	}
	f.mu.Lock()
	f.nextID++
	id := f.nextID
	f.prepped++
	f.mu.Unlock()
	return &runner.PreparedRun{ExecID: id, Opts: opts}, nil
}

func (f *fakeExecutor) ExecutePrepared(ctx context.Context, prepared *runner.PreparedRun, prompt string, onProgress func(runner.RunProgress)) runner.RunSummary {
	f.mu.Lock()
	f.calls = append(f.calls, fakeCall{Prompt: prompt, Opts: prepared.Opts})
	f.mu.Unlock()

	for _, e := range f.Events {
		if onProgress != nil {
			onProgress(e)
		}
	}

	if f.Delay > 0 {
		select {
		case <-ctx.Done():
		case <-time.After(f.Delay):
		}
	}
	s := f.Summary
	if s.ExecID == 0 {
		s.ExecID = prepared.ExecID
	}
	return s
}

func (f *fakeExecutor) Calls() []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// ============================================================
// recordingReporter
// ============================================================

// reporterEvent captures one Reporter callback for assertions.
type reporterEvent struct {
	Kind         string
	StageInfo    StageInfo
	StageOutcome StageOutcome
	BundleInfo   BundleInfo
	Result       Result
	Progress     runner.RunProgress
	StepName     string
	Reason       string
}

// recordingReporter implements Reporter by appending every call to Events
// under a mutex. Safe for concurrent fan-in from Parallel children.
//
// Embeds BaseReporter so new Reporter methods automatically no-op
// without breaking pre-existing tests; tests that need to assert
// ActionStart / ActionEnd / AgentExec* must explicitly override.
type recordingReporter struct {
	BaseReporter
	mu     sync.Mutex
	Events []reporterEvent
}

func (r *recordingReporter) append(e reporterEvent) {
	r.mu.Lock()
	r.Events = append(r.Events, e)
	r.mu.Unlock()
}

func (r *recordingReporter) StageStart(s StageInfo) {
	r.append(reporterEvent{Kind: "StageStart", StageInfo: s})
}

func (r *recordingReporter) StageEnd(s StageInfo, o StageOutcome) {
	r.append(reporterEvent{Kind: "StageEnd", StageInfo: s, StageOutcome: o})
}

func (r *recordingReporter) StepSkipped(parent StageInfo, name, reason string) {
	r.append(reporterEvent{Kind: "StepSkipped", StageInfo: parent, StepName: name, Reason: reason})
}

func (r *recordingReporter) BundleStart(b BundleInfo) {
	r.append(reporterEvent{Kind: "BundleStart", BundleInfo: b})
}

func (r *recordingReporter) BundleEnd(b BundleInfo, res Result) {
	r.append(reporterEvent{Kind: "BundleEnd", BundleInfo: b, Result: res})
}

func (r *recordingReporter) AgentEvent(b BundleInfo, p runner.RunProgress) {
	r.append(reporterEvent{Kind: "AgentEvent", BundleInfo: b, Progress: p})
}

func (r *recordingReporter) countOf(kind string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.Events {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

// ============================================================
// Helpers
// ============================================================

func newCtx() RunCtx {
	return RunCtx{Ctx: context.Background(), Reporter: &recordingReporter{}}
}

func newEnv(exec Executor) RuntimeEnv {
	return RuntimeEnv{Executor: exec, Role: "tester", Action: "test"}
}

func makeBundle(name string, render func(RuntimeEnv) (string, error)) PromptBundle {
	if render == nil {
		render = func(RuntimeEnv) (string, error) { return "hello", nil }
	}
	return PromptBundle{
		Name:   name,
		Render: render,
		RunOpts: func(RuntimeEnv) runner.RunOpts {
			return runner.RunOpts{}
		},
	}
}
