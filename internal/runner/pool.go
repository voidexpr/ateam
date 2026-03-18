package runner

import (
	"context"
	"sync"
)

// PoolTask pairs a prompt with its run options for the worker pool.
type PoolTask struct {
	Prompt string
	RunOpts
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
		r.LogQueued(task.RunOpts)
	}

	for _, task := range tasks {
		wg.Add(1)
		sem <- struct{}{} // acquire slot

		go func(t PoolTask) {
			defer wg.Done()
			defer func() { <-sem }() // release slot

			summary := r.Run(ctx, t.Prompt, t.RunOpts, progress)

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
