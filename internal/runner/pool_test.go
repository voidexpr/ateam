package runner

import (
	"context"
	"fmt"
	"path/filepath"
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

// makeTaskLogsDir returns a unique logs subdirectory for each task index,
// avoiding file-name collisions when tasks share the same timestamp prefix.
func makeTaskLogsDir(baseDir string, i int) string {
	return filepath.Join(baseDir, fmt.Sprintf("task-%d", i))
}

func TestRunPoolBasic(t *testing.T) {
	dir := t.TempDir()

	mock := &agent.MockAgent{Response: "pool output"}
	r := &Runner{Agent: mock}

	tasks := []PoolTask{
		{Prompt: "task 1", RunOpts: RunOpts{RoleID: "role-1", Action: ActionRun, LogsDir: makeTaskLogsDir(dir, 0)}},
		{Prompt: "task 2", RunOpts: RunOpts{RoleID: "role-2", Action: ActionRun, LogsDir: makeTaskLogsDir(dir, 1)}},
		{Prompt: "task 3", RunOpts: RunOpts{RoleID: "role-3", Action: ActionRun, LogsDir: makeTaskLogsDir(dir, 2)}},
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
	r := &Runner{Agent: tracking}

	tasks := make([]PoolTask, numTasks)
	for i := range tasks {
		tasks[i] = PoolTask{
			Prompt:  "task",
			RunOpts: RunOpts{RoleID: fmt.Sprintf("role-%d", i), Action: ActionRun, LogsDir: makeTaskLogsDir(dir, i)},
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
	r := &Runner{Agent: mock}

	tasks := []PoolTask{
		{Prompt: "t1", RunOpts: RunOpts{RoleID: "c-role-1", Action: ActionRun, LogsDir: makeTaskLogsDir(dir, 0)}},
		{Prompt: "t2", RunOpts: RunOpts{RoleID: "c-role-2", Action: ActionRun, LogsDir: makeTaskLogsDir(dir, 1)}},
		{Prompt: "t3", RunOpts: RunOpts{RoleID: "c-role-3", Action: ActionRun, LogsDir: makeTaskLogsDir(dir, 2)}},
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
	r := &Runner{Agent: mock}

	tasks := []PoolTask{
		{Prompt: "p1", RunOpts: RunOpts{RoleID: "res-role", Action: ActionRun, LogsDir: makeTaskLogsDir(dir, 0)}},
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
	r := &Runner{Agent: mock}

	const numTasks = 10
	tasks := make([]PoolTask, numTasks)
	for i := range tasks {
		tasks[i] = PoolTask{
			Prompt:  "concurrent task",
			RunOpts: RunOpts{RoleID: fmt.Sprintf("concurrent-role-%d", i), Action: ActionRun, LogsDir: makeTaskLogsDir(dir, i)},
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
