package runner

import (
	"context"
	"sync"
)

// AgentTask represents a unit of work for the worker pool.
type AgentTask struct {
	AgentID    string
	Prompt     string
	OutputFile string
}

// PoolResult pairs an agent ID with its run result.
type PoolResult struct {
	AgentID string
	Result  RunResult
}

// RunPool executes tasks in parallel with a maximum concurrency limit.
// It returns results in completion order.
func RunPool(ctx context.Context, tasks []AgentTask, maxParallel, timeoutMinutes int) []PoolResult {
	sem := make(chan struct{}, maxParallel)
	var mu sync.Mutex
	var results []PoolResult
	var wg sync.WaitGroup

	for _, task := range tasks {
		wg.Add(1)
		sem <- struct{}{} // acquire slot

		go func(t AgentTask) {
			defer wg.Done()
			defer func() { <-sem }() // release slot

			result := RunClaude(ctx, t.Prompt, t.OutputFile, timeoutMinutes)
			result.AgentID = t.AgentID

			mu.Lock()
			results = append(results, PoolResult{AgentID: t.AgentID, Result: result})
			mu.Unlock()
		}(task)
	}

	wg.Wait()
	return results
}
