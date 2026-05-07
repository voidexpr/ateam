package runner

import (
	"context"
	"fmt"
	"strings"
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

func (a *concurrencyTrackingAgent) SetModel(model string) {}

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

func TestRunPoolBasic(t *testing.T) {
	dir := t.TempDir()

	mock := &agent.MockAgent{Response: "pool output"}
	r := newTestRunner(t, dir, mock)

	tasks := []PoolExec{
		{Prompt: "task 1", RunOpts: RunOpts{RoleID: "role-1", Action: ActionRun}},
		{Prompt: "task 2", RunOpts: RunOpts{RoleID: "role-2", Action: ActionRun}},
		{Prompt: "task 3", RunOpts: RunOpts{RoleID: "role-3", Action: ActionRun}},
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
			RunOpts: RunOpts{RoleID: fmt.Sprintf("role-%d", i), Action: ActionRun},
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
		{Prompt: "t1", RunOpts: RunOpts{RoleID: "c-role-1", Action: ActionRun}},
		{Prompt: "t2", RunOpts: RunOpts{RoleID: "c-role-2", Action: ActionRun}},
		{Prompt: "t3", RunOpts: RunOpts{RoleID: "c-role-3", Action: ActionRun}},
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
		{Prompt: "p1", RunOpts: RunOpts{RoleID: "res-role", Action: ActionRun}},
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
			RunOpts: RunOpts{RoleID: fmt.Sprintf("concurrent-role-%d", i), Action: ActionRun},
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
