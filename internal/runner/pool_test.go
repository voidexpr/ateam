package runner

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ateam/internal/agent"
)

// concurrencyTrackingAgent implements agent.Agent and records the peak number
// of concurrent Run calls. Used to verify semaphore behavior in RunPool.
type concurrencyTrackingAgent struct {
	delay         time.Duration
	active        atomic.Int64
	maxConcurrent atomic.Int64
}

func (a *concurrencyTrackingAgent) Name() string { return "tracking" }

func (a *concurrencyTrackingAgent) SetModel(model string)        {}
func (a *concurrencyTrackingAgent) SetEffort(effort string)      {}
func (a *concurrencyTrackingAgent) SetMaxBudgetUSD(value string) {}

func (a *concurrencyTrackingAgent) CloneWithResolvedTemplates(replacer *strings.Replacer) agent.Agent {
	return a
}

func (a *concurrencyTrackingAgent) DebugCommandArgs(extraArgs []string) (string, []string) {
	return "tracking", nil
}

func (a *concurrencyTrackingAgent) Run(ctx context.Context, req agent.Request) <-chan agent.StreamEvent {
	ch := make(chan agent.StreamEvent, 4)

	current := a.active.Add(1)
	for {
		seen := a.maxConcurrent.Load()
		if current <= seen || a.maxConcurrent.CompareAndSwap(seen, current) {
			break
		}
	}

	go func() {
		defer close(ch)
		defer a.active.Add(-1)

		if a.delay > 0 {
			select {
			case <-time.After(a.delay):
			case <-ctx.Done():
				ch <- agent.StreamEvent{Type: "error", Err: ctx.Err(), ExitCode: -1}
				return
			}
		}

		ch <- agent.StreamEvent{Type: "system", SessionID: "tracking-session"}
		ch <- agent.StreamEvent{Type: "assistant", Text: "ok"}
		ch <- agent.StreamEvent{Type: "result", Output: "ok", ExitCode: 0}
	}()
	return ch
}

// panicAgent panics synchronously inside Run, simulating an unexpected nil
// deref or similar fault reached through AgentExecutor.Execute on a worker goroutine.
type panicAgent struct{}

func (panicAgent) Name() string           { return "panic" }
func (panicAgent) SetModel(string)        {}
func (panicAgent) SetEffort(string)       {}
func (panicAgent) SetMaxBudgetUSD(string) {}
func (a panicAgent) CloneWithResolvedTemplates(*strings.Replacer) agent.Agent {
	return a
}
func (panicAgent) DebugCommandArgs([]string) (string, []string) { return "panic", nil }
func (panicAgent) Run(context.Context, agent.Request) <-chan agent.StreamEvent {
	panic("boom in agent.Run")
}

// TestRunPoolRecoversWorkerPanic verifies that a panic on one task is converted
// into a failed-task summary instead of tearing down the process, and that
// sibling tasks still complete.
func TestRunPoolRecoversWorkerPanic(t *testing.T) {
	dir := t.TempDir()
	r := newTestRunner(t, dir, panicAgent{})

	tasks := []PoolExec{
		{Prompt: "boom", RunOpts: RunOpts{RoleID: "panic-role", Action: ActionExec}},
	}

	completed := make(chan RunSummary, len(tasks))
	results := RunPool(context.Background(), r, tasks, 1, nil, completed)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	s := results[0]
	if s.Err == nil {
		t.Fatal("expected a non-nil Err for the panicking task")
	}
	if !s.IsError || s.ErrorSource != agent.ErrorSourceAteamInternal {
		t.Errorf("expected IsError + ateam_internal source, got IsError=%v source=%q", s.IsError, s.ErrorSource)
	}
	if !strings.Contains(s.ErrorCause, "panic: boom in agent.Run") {
		t.Errorf("expected panic message in ErrorCause, got %q", s.ErrorCause)
	}
	if !strings.Contains(s.ErrorCause, "goroutine") {
		t.Errorf("expected a stack trace in ErrorCause, got %q", s.ErrorCause)
	}
	if s.RoleID != "panic-role" {
		t.Errorf("expected RoleID preserved on panic summary, got %q", s.RoleID)
	}

	// The summary must also reach the completed channel so the cmd-layer
	// drain loop accounts for it.
	var fromCh int
	for range completed {
		fromCh++
	}
	if fromCh != 1 {
		t.Errorf("expected 1 summary on completed channel, got %d", fromCh)
	}
}

func TestRunPoolBasic(t *testing.T) {
	dir := t.TempDir()

	mock := &agent.MockAgent{Response: "pool output"}
	r := newTestRunner(t, dir, mock)

	tasks := []PoolExec{
		{Prompt: "task 1", RunOpts: RunOpts{RoleID: "role-1", Action: ActionExec}},
		{Prompt: "task 2", RunOpts: RunOpts{RoleID: "role-2", Action: ActionExec}},
		{Prompt: "task 3", RunOpts: RunOpts{RoleID: "role-3", Action: ActionExec}},
	}

	results := RunPool(context.Background(), r, tasks, 2, nil, nil)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	for _, s := range results {
		if s.Err != nil {
			t.Errorf("unexpected error for role %q: %v", s.RoleID, s.Err)
		}
		if s.Output != "pool output" {
			t.Errorf("expected output 'pool output', got %q", s.Output)
		}
	}
}

func TestRunPoolSemaphoreLimit(t *testing.T) {
	dir := t.TempDir()

	const numTasks = 6
	const maxParallel = 2

	tracking := &concurrencyTrackingAgent{delay: 20 * time.Millisecond}
	r := newTestRunner(t, dir, tracking)

	tasks := make([]PoolExec, numTasks)
	for i := range tasks {
		tasks[i] = PoolExec{
			Prompt:  "task",
			RunOpts: RunOpts{RoleID: fmt.Sprintf("role-%d", i), Action: ActionExec},
		}
	}

	results := RunPool(context.Background(), r, tasks, maxParallel, nil, nil)

	if len(results) != numTasks {
		t.Fatalf("expected %d results, got %d", numTasks, len(results))
	}
	if got := tracking.maxConcurrent.Load(); got > maxParallel {
		t.Errorf("max concurrent goroutines was %d, expected <= %d", got, maxParallel)
	}
}

func TestRunPoolCompletedChannel(t *testing.T) {
	dir := t.TempDir()

	mock := &agent.MockAgent{Response: "completed"}
	r := newTestRunner(t, dir, mock)

	tasks := []PoolExec{
		{Prompt: "t1", RunOpts: RunOpts{RoleID: "c-role-1", Action: ActionExec}},
		{Prompt: "t2", RunOpts: RunOpts{RoleID: "c-role-2", Action: ActionExec}},
		{Prompt: "t3", RunOpts: RunOpts{RoleID: "c-role-3", Action: ActionExec}},
	}

	completed := make(chan RunSummary, len(tasks))
	results := RunPool(context.Background(), r, tasks, 3, nil, completed)

	// completed is closed by RunPool; drain it to verify all summaries arrived.
	var completedSummaries []RunSummary
	for s := range completed {
		completedSummaries = append(completedSummaries, s)
	}

	if len(results) != len(tasks) {
		t.Errorf("expected %d results, got %d", len(tasks), len(results))
	}
	if len(completedSummaries) != len(tasks) {
		t.Errorf("expected %d completed summaries, got %d", len(tasks), len(completedSummaries))
	}
}

func TestRunPoolResultCollection(t *testing.T) {
	dir := t.TempDir()

	mock := &agent.MockAgent{Response: "result-output", Cost: 0.05}
	r := newTestRunner(t, dir, mock)

	tasks := []PoolExec{
		{Prompt: "p1", RunOpts: RunOpts{RoleID: "res-role", Action: ActionExec}},
	}

	results := RunPool(context.Background(), r, tasks, 1, nil, nil)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	s := results[0]
	if s.RoleID != "res-role" {
		t.Errorf("expected RoleID 'res-role', got %q", s.RoleID)
	}
	if s.Output != "result-output" {
		t.Errorf("expected Output 'result-output', got %q", s.Output)
	}
	if s.Cost != 0.05 {
		t.Errorf("expected Cost 0.05, got %f", s.Cost)
	}
}

func TestRunPoolConcurrentResultsAreSafe(t *testing.T) {
	// Run with -race to detect concurrent map/slice access issues.
	dir := t.TempDir()

	mock := &agent.MockAgent{Response: "concurrent"}
	r := newTestRunner(t, dir, mock)

	const numTasks = 10
	tasks := make([]PoolExec, numTasks)
	for i := range tasks {
		tasks[i] = PoolExec{
			Prompt:  "concurrent task",
			RunOpts: RunOpts{RoleID: fmt.Sprintf("concurrent-role-%d", i), Action: ActionExec},
		}
	}

	completed := make(chan RunSummary, numTasks)
	results := RunPool(context.Background(), r, tasks, 5, nil, completed)

	if len(results) != numTasks {
		t.Errorf("expected %d results, got %d", numTasks, len(results))
	}

	var count int
	for range completed {
		count++
	}
	if count != numTasks {
		t.Errorf("expected %d completed events, got %d", numTasks, count)
	}
}

// TestRunPoolPerExecRunnerOverride verifies that a PoolExec carrying its own
// AgentExecutor (set on the per-task .AgentExecutor field) is executed against that runner
// rather than the pool's shared one. This is the path used when each role
// needs its own agent configuration (model/effort/etc).
func TestRunPoolPerExecRunnerOverride(t *testing.T) {
	dir := t.TempDir()

	sharedAgent := &agent.MockAgent{Response: "shared"}
	sharedRunner := newTestRunner(t, dir, sharedAgent)

	overrideAgent := &agent.MockAgent{Response: "override"}
	overrideRunner := newTestRunner(t, dir, overrideAgent)

	tasks := []PoolExec{
		{Prompt: "t1", RunOpts: RunOpts{RoleID: "shared-role", Action: ActionExec}},
		{Prompt: "t2", RunOpts: RunOpts{RoleID: "override-role", Action: ActionExec}, AgentExecutor: overrideRunner},
	}

	results := RunPool(context.Background(), sharedRunner, tasks, 1, nil, nil)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	got := map[string]string{}
	for _, s := range results {
		got[s.RoleID] = s.Output
	}
	if got["shared-role"] != "shared" {
		t.Errorf("shared-role output = %q, want %q", got["shared-role"], "shared")
	}
	if got["override-role"] != "override" {
		t.Errorf("override-role output = %q, want %q (per-exec AgentExecutor override not honored)",
			got["override-role"], "override")
	}
}

// signalAgent closes `started` on its first Run call and then blocks on
// `release` (independent of ctx) so the pool's worker slot stays held until the
// test explicitly lets it finish. This lets a test deterministically cancel ctx
// after dispatch has started but before the worker releases its sem slot — the
// only state in which the ctx.Done() branch of the dispatch select can win
// without racing the worker.
type signalAgent struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (a *signalAgent) Name() string           { return "signal" }
func (a *signalAgent) SetModel(string)        {}
func (a *signalAgent) SetEffort(string)       {}
func (a *signalAgent) SetMaxBudgetUSD(string) {}
func (a *signalAgent) CloneWithResolvedTemplates(*strings.Replacer) agent.Agent {
	return a
}
func (a *signalAgent) DebugCommandArgs([]string) (string, []string) { return "signal", nil }
func (a *signalAgent) Run(ctx context.Context, _ agent.Request) <-chan agent.StreamEvent {
	ch := make(chan agent.StreamEvent, 4)
	a.once.Do(func() { close(a.started) })
	go func() {
		defer close(ch)
		select {
		case <-a.release:
		case <-ctx.Done():
		}
		ch <- agent.StreamEvent{Type: "system", SessionID: "signal-session"}
		ch <- agent.StreamEvent{Type: "assistant", Text: "ok"}
		ch <- agent.StreamEvent{Type: "result", Output: "ok", ExitCode: 0}
	}()
	return ch
}

// TestRunPoolCtxCancelSkipsUnDispatched verifies that when ctx is cancelled
// mid-dispatch (e.g. Ctrl-C with more tasks than worker slots), the un-dispatched
// remainder is surfaced as skipped summaries rather than dropped silently.
// Mirrors the existing PreDispatch coverage above.
func TestRunPoolCtxCancelSkipsUnDispatched(t *testing.T) {
	dir := t.TempDir()

	ag := &signalAgent{started: make(chan struct{}), release: make(chan struct{})}
	r := newTestRunner(t, dir, ag)

	const numTasks = 5
	tasks := make([]PoolExec, numTasks)
	for i := range tasks {
		tasks[i] = PoolExec{
			Prompt:  "task",
			RunOpts: RunOpts{RoleID: fmt.Sprintf("cancel-role-%d", i), Action: ActionExec},
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Cancel only after the first worker has actually entered agent.Run, so
	// the sem slot is guaranteed held when ctx.Done fires in the dispatch select.
	go func() {
		<-ag.started
		cancel()
	}()

	completed := make(chan RunSummary, numTasks)

	// Drain completed concurrently. The first skipped summary tells us the
	// dispatch loop's ctx.Done() branch has fired, so it is safe to release
	// the in-flight worker and let RunPool return.
	var (
		mu        sync.Mutex
		summaries []RunSummary
	)
	released := false
	drainDone := make(chan struct{})
	go func() {
		defer close(drainDone)
		for s := range completed {
			mu.Lock()
			summaries = append(summaries, s)
			mu.Unlock()
			if !released && s.ErrorSource == agent.ErrorSourceSkipped {
				released = true
				close(ag.release)
			}
		}
	}()

	results := RunPoolWithOpts(ctx, r, tasks, 1, nil, completed, PoolOpts{})
	<-drainDone

	// Safety net: ensure release is closed even if no skip ever arrived (would
	// indicate the bug regressed and the test would otherwise hang).
	if !released {
		close(ag.release)
	}

	if len(results) != numTasks {
		t.Fatalf("expected %d results (dispatched + skipped), got %d", numTasks, len(results))
	}

	mu.Lock()
	fromCh := len(summaries)
	mu.Unlock()
	if fromCh != numTasks {
		t.Errorf("expected %d completed events, got %d", numTasks, fromCh)
	}

	seen := map[string]int{}
	var skipped int
	for _, s := range results {
		seen[s.RoleID]++
		if s.ErrorSource == agent.ErrorSourceSkipped {
			skipped++
			if s.ErrorCause == "" || !strings.Contains(s.ErrorCause, context.Canceled.Error()) {
				t.Errorf("skipped summary for %q should carry ctx.Err() in ErrorCause, got %q",
					s.RoleID, s.ErrorCause)
			}
		}
	}
	for i := 0; i < numTasks; i++ {
		id := fmt.Sprintf("cancel-role-%d", i)
		if seen[id] != 1 {
			t.Errorf("role %q: expected exactly 1 result, got %d", id, seen[id])
		}
	}
	if skipped == 0 {
		t.Errorf("expected at least one skipped task after ctx cancel, got 0")
	}
}

// TestRunPoolPreDispatchAborts verifies PoolOpts.PreDispatch can stop further
// dispatch mid-pool while letting in-flight tasks finish.
func TestRunPoolPreDispatchAborts(t *testing.T) {
	dir := t.TempDir()

	mock := &agent.MockAgent{Response: "ok"}
	r := newTestRunner(t, dir, mock)

	tasks := []PoolExec{
		{Prompt: "t1", RunOpts: RunOpts{RoleID: "p-1", Action: ActionExec}},
		{Prompt: "t2", RunOpts: RunOpts{RoleID: "p-2", Action: ActionExec}},
		{Prompt: "t3", RunOpts: RunOpts{RoleID: "p-3", Action: ActionExec}},
		{Prompt: "t4", RunOpts: RunOpts{RoleID: "p-4", Action: ActionExec}},
	}

	// Allow the first two dispatches, then refuse — only those two should run.
	var calls atomic.Int64
	opts := PoolOpts{
		PreDispatch: func() error {
			if calls.Add(1) > 2 {
				return fmt.Errorf("budget cap reached")
			}
			return nil
		},
	}

	results := RunPoolWithOpts(context.Background(), r, tasks, 4, nil, nil, opts)
	// 2 tasks dispatched + 2 surfaced as skipped summaries = 4 total.
	if len(results) != 4 {
		t.Fatalf("expected 4 results (2 dispatched + 2 skipped), got %d", len(results))
	}
	var dispatched, skipped int
	for _, s := range results {
		if s.ErrorSource == agent.ErrorSourceSkipped {
			skipped++
			if s.Err != nil {
				t.Errorf("skipped summary for %q should have nil Err, got %v", s.RoleID, s.Err)
			}
		} else {
			dispatched++
		}
	}
	if dispatched != 2 || skipped != 2 {
		t.Errorf("expected 2 dispatched + 2 skipped, got %d dispatched + %d skipped", dispatched, skipped)
	}
}
