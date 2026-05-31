package runner

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"github.com/ateam/internal/agent"
)

// PoolExec pairs a prompt with its run options for the worker pool.
type PoolExec struct {
	Prompt string
	RunOpts
	AgentExecutor *AgentExecutor // optional per-agent exec runner override; if nil, the pool's shared runner is used
}

// PoolOpts configures optional behavior for RunPoolWithOpts.
type PoolOpts struct {
	// PreDispatch is invoked once per task, just before a worker slot is
	// acquired. Returning an error stops dispatching further tasks; already
	// running tasks complete normally. Used by --max-budget-usd-batch to
	// short-circuit a pool whose accumulated cost has crossed the cap.
	PreDispatch func() error
}

// RunPool executes agent execs in parallel with a maximum concurrency limit.
// It returns results in completion order. If completed is non-nil, each
// RunSummary is sent on it as the agent exec finishes (before the final return).
//
// Contract:
//   - onProgress: synchronous callback invoked from every worker goroutine
//     for each agent's RunProgress event. May be nil. Implementations own
//     their own thread-safety. For chan-driven consumers, wrap a buffered
//     chan with ProgressChan to preserve drop-on-overflow semantics.
//   - completed: blocking send, one per agent exec plus a close. Callers MUST
//     provide a buffer ≥ len(tasks) OR drain it concurrently with RunPool;
//     otherwise workers deadlock after maxParallel summaries queue up.
//     An obviously-undersized channel is rejected up-front: callers are
//     returned an empty slice and a warning is printed rather than
//     silently hanging.
func RunPool(ctx context.Context, r *AgentExecutor, tasks []PoolExec, maxParallel int, onProgress func(RunProgress), completed chan<- RunSummary) []RunSummary {
	return RunPoolWithOpts(ctx, r, tasks, maxParallel, onProgress, completed, PoolOpts{})
}

// RunPoolWithOpts is RunPool plus optional hooks (see PoolOpts).
func RunPoolWithOpts(ctx context.Context, r *AgentExecutor, tasks []PoolExec, maxParallel int, onProgress func(RunProgress), completed chan<- RunSummary, opts PoolOpts) []RunSummary {
	if completed != nil && cap(completed) < len(tasks) {
		fmt.Fprintf(os.Stderr,
			"RunPool: completed channel buffer (%d) is smaller than len(tasks) (%d); "+
				"either size the channel to len(tasks) or drain it concurrently — refusing to dispatch to avoid deadlock\n",
			cap(completed), len(tasks))
		return nil
	}

	sem := make(chan struct{}, maxParallel)
	var mu sync.Mutex
	var results []RunSummary
	var wg sync.WaitGroup

	// record appends a summary to results and, if requested, forwards it on
	// the completed channel. Safe to call concurrently from workers and from
	// the dispatch loop. The completed send never blocks because the caller
	// guarantees cap(completed) >= len(tasks) and every task produces at most
	// one summary (dispatched OR skipped, never both).
	record := func(s RunSummary) {
		mu.Lock()
		results = append(results, s)
		mu.Unlock()
		if completed != nil {
			completed <- s
		}
	}

	for i := range tasks {
		task := tasks[i]
		if opts.PreDispatch != nil {
			if err := opts.PreDispatch(); err != nil {
				fmt.Fprintf(os.Stderr, "Pool: stopping dispatch — %v\n", err)
				// Surface the tasks that were never dispatched instead of
				// dropping them silently: emit a skipped summary per remaining
				// task so the batch reports them and exits non-zero.
				for _, skipped := range tasks[i:] {
					record(skippedSummary(skipped, err))
				}
				break
			}
		}

		wg.Add(1)
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Done()
			continue
		}

		go func(t PoolExec) {
			defer wg.Done()
			defer func() { <-sem }() // release slot

			var summary RunSummary
			func() {
				// A panic in Run (or the agent it dispatches) would otherwise
				// tear down the whole process and every sibling agent. Recover
				// in-frame, turn it into a failed-task summary, and let the
				// batch continue.
				defer func() {
					if rv := recover(); rv != nil {
						summary = panicSummary(t, rv)
					}
				}()
				taskRunner := r
				if t.AgentExecutor != nil {
					taskRunner = t.AgentExecutor
				}
				summary = taskRunner.Execute(ctx, t.Prompt, t.RunOpts, onProgress)
			}()

			record(summary)
		}(task)
	}

	wg.Wait()
	if completed != nil {
		close(completed)
	}
	return results
}

// panicSummary builds a failed-task summary from a recovered panic value. The
// stack trace is captured into ErrorCause so the panic stays diagnosable. Err
// is set so the cmd layer counts the task as failed.
func panicSummary(t PoolExec, rv any) RunSummary {
	stack := debug.Stack()
	now := time.Now()
	started := t.StartedAt
	if started.IsZero() {
		started = now
	}
	err := fmt.Errorf("panic: %v", rv)
	return RunSummary{
		RoleID:      t.RoleID,
		StartedAt:   started,
		EndedAt:     now,
		Duration:    now.Sub(started),
		ExitCode:    -1,
		IsError:     true,
		Err:         err,
		ErrorSource: agent.ErrorSourceAteamInternal,
		ErrorCause:  fmt.Sprintf("panic: %v\n\n%s", rv, stack),
	}
}

// skippedSummary builds a summary for a task that PreDispatch refused to
// dispatch (e.g. --max-budget-usd-batch reached). It is marked skipped rather
// than failed so the cmd layer can report it in its own "X skipped" bucket;
// Err is left nil to keep skipped distinct from failed.
func skippedSummary(t PoolExec, cause error) RunSummary {
	now := time.Now()
	return RunSummary{
		RoleID:      t.RoleID,
		StartedAt:   now,
		EndedAt:     now,
		ExitCode:    -1,
		IsError:     true,
		ErrorSource: agent.ErrorSourceSkipped,
		ErrorCause:  fmt.Sprintf("skipped: %v", cause),
	}
}
