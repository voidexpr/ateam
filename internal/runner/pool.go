package runner

import (
	"context"
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
func RunPool(ctx context.Context, r *Runner, tasks []PoolTask, maxParallel int, progress chan<- RunProgress, completed chan<- RunSummary) []RunSummary {
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
