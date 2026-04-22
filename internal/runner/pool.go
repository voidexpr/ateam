package runner

import (
	"context"
	"fmt"
	"os"
	"sync"
)

// PoolTask pairs a prompt with its run options for the worker pool.
type PoolTask struct {
	Prompt string
	RunOpts
	Runner *Runner // optional per-task runner override; if nil, the pool's shared runner is used
}

// RunPool executes tasks in parallel with a maximum concurrency limit.
// It returns results in completion order. If completed is non-nil, each
// RunSummary is sent on it as the task finishes (before the final return).
//
// Channel contract (see CONCURRENCY.md):
//   - progress: non-blocking send. Callers may pass a small buffer or nil.
//   - completed: blocking send, one per task plus a close. Callers MUST
//     provide a buffer ≥ len(tasks) OR drain it concurrently with RunPool;
//     otherwise workers deadlock after maxParallel summaries queue up.
//     An obviously-undersized channel is rejected up-front: callers are
//     returned an empty slice and a warning is printed rather than
//     silently hanging.
func RunPool(ctx context.Context, r *Runner, tasks []PoolTask, maxParallel int, progress chan<- RunProgress, completed chan<- RunSummary) []RunSummary {
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
		qr := r
		if task.Runner != nil {
			qr = task.Runner
		}
		qr.LogQueued(task.RunOpts)
	}

	for _, task := range tasks {
		wg.Add(1)
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Done()
			continue
		}

		go func(t PoolTask) {
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
