package runner

import (
	"context"
	"fmt"
	"os"
	"sync"
)

// PoolExec pairs a prompt with its run options for the worker pool.
type PoolExec struct {
	Prompt string
	RunOpts
	Runner *Runner // optional per-agent exec runner override; if nil, the pool's shared runner is used
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
// Channel contract (see CONCURRENCY.md):
//   - progress: non-blocking send. Callers may pass a small buffer or nil.
//   - completed: blocking send, one per agent exec plus a close. Callers MUST
//     provide a buffer ≥ len(tasks) OR drain it concurrently with RunPool;
//     otherwise workers deadlock after maxParallel summaries queue up.
//     An obviously-undersized channel is rejected up-front: callers are
//     returned an empty slice and a warning is printed rather than
//     silently hanging.
func RunPool(ctx context.Context, r *Runner, tasks []PoolExec, maxParallel int, progress chan<- RunProgress, completed chan<- RunSummary) []RunSummary {
	return RunPoolWithOpts(ctx, r, tasks, maxParallel, progress, completed, PoolOpts{})
}

// RunPoolWithOpts is RunPool plus optional hooks (see PoolOpts).
func RunPoolWithOpts(ctx context.Context, r *Runner, tasks []PoolExec, maxParallel int, progress chan<- RunProgress, completed chan<- RunSummary, opts PoolOpts) []RunSummary {
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

	for _, task := range tasks {
		if opts.PreDispatch != nil {
			if err := opts.PreDispatch(); err != nil {
				fmt.Fprintf(os.Stderr, "Pool: stopping dispatch — %v\n", err)
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

			taskRunner := r
			if t.Runner != nil {
				taskRunner = t.Runner
			}
			summary := taskRunner.Run(ctx, t.Prompt, t.RunOpts, progress)

			mu.Lock()
			results = append(results, summary)
			mu.Unlock()

			if completed != nil {
				completed <- summary
			}
		}(task)
	}

	wg.Wait()
	if completed != nil {
		close(completed)
	}
	return results
}
