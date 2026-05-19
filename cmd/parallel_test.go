package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/display"
	"github.com/ateam/internal/runner"
)

// newCmdTestRunner returns a Runner backed by a temp project DB so Run() can
// satisfy its "CallDB required" precondition.
func newCmdTestRunner(t *testing.T, baseDir string, ag agent.Agent) *runner.Runner {
	t.Helper()
	db, err := calldb.Open(filepath.Join(baseDir, "state.sqlite"))
	if err != nil {
		t.Fatalf("open test calldb: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &runner.Runner{
		Agent:      ag,
		ProjectDir: baseDir,
		CallDB:     db,
	}
}

func TestParallelPoolWithMockAgent(t *testing.T) {
	dir := t.TempDir()
	mock := &agent.MockAgent{Response: "hello from parallel", Cost: 0.02}
	r := newCmdTestRunner(t, dir, mock)

	labels := []string{"alpha", "beta", "gamma"}
	tasks := make([]runner.PoolExec, len(labels))
	for i, label := range labels {
		tasks[i] = runner.PoolExec{
			Prompt: fmt.Sprintf("prompt for %s", label),
			RunOpts: runner.RunOpts{
				RoleID: label,
				Action: runner.ActionParallel,
				Batch:  "test-parallel-group",
			},
		}
	}

	completedCh := make(chan runner.RunSummary, len(tasks))
	results := runner.RunPool(context.Background(), r, tasks, 2, nil, completedCh)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	var completedCount int
	for range completedCh {
		completedCount++
	}
	if completedCount != 3 {
		t.Fatalf("expected 3 completed events, got %d", completedCount)
	}

	for _, s := range results {
		if s.Err != nil {
			t.Errorf("unexpected error for %s: %v", s.RoleID, s.Err)
		}
		if s.Output != "hello from parallel" {
			t.Errorf("expected output 'hello from parallel', got %q for %s", s.Output, s.RoleID)
		}
		if s.Cost != 0.02 {
			t.Errorf("expected cost 0.02, got %f for %s", s.Cost, s.RoleID)
		}
	}

	// Verify mock received all prompts
	if len(mock.Requests) != 3 {
		t.Fatalf("expected 3 requests to mock, got %d", len(mock.Requests))
	}
	promptSet := map[string]bool{}
	for _, req := range mock.Requests {
		promptSet[req.Prompt] = true
	}
	for _, label := range labels {
		want := fmt.Sprintf("prompt for %s", label)
		if !promptSet[want] {
			t.Errorf("missing prompt %q from mock requests", want)
		}
	}
}

func TestParallelPoolPartialFailure(t *testing.T) {
	dir := t.TempDir()

	successMock := &agent.MockAgent{Response: "ok", Cost: 0.01}
	r := newCmdTestRunner(t, dir, successMock)

	tasks := []runner.PoolExec{
		{Prompt: "good", RunOpts: runner.RunOpts{RoleID: "good-task", Action: runner.ActionParallel}},
		{Prompt: "also good", RunOpts: runner.RunOpts{RoleID: "good-task-2", Action: runner.ActionParallel}},
	}

	completedCh := make(chan runner.RunSummary, len(tasks))
	results := runner.RunPool(context.Background(), r, tasks, 2, nil, completedCh)

	var succeeded, failed int
	for _, s := range results {
		if s.Err != nil {
			failed++
		} else {
			succeeded++
		}
	}

	if succeeded != 2 {
		t.Errorf("expected 2 succeeded, got %d", succeeded)
	}
	if failed != 0 {
		t.Errorf("expected 0 failed, got %d", failed)
	}
}

func TestParallelPoolWithErrors(t *testing.T) {
	dir := t.TempDir()

	failMock := &agent.MockAgent{Err: fmt.Errorf("simulated failure")}
	r := newCmdTestRunner(t, dir, failMock)

	tasks := []runner.PoolExec{
		{Prompt: "fail1", RunOpts: runner.RunOpts{RoleID: "fail-a", Action: runner.ActionParallel}},
		{Prompt: "fail2", RunOpts: runner.RunOpts{RoleID: "fail-b", Action: runner.ActionParallel}},
	}

	completedCh := make(chan runner.RunSummary, len(tasks))
	results := runner.RunPool(context.Background(), r, tasks, 2, nil, completedCh)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, s := range results {
		if s.Err == nil {
			t.Errorf("expected error for %s", s.RoleID)
		}
	}
}

func TestParallelPoolProgressEvents(t *testing.T) {
	dir := t.TempDir()
	mock := &agent.MockAgent{Response: "progress test"}
	r := newCmdTestRunner(t, dir, mock)

	tasks := []runner.PoolExec{
		{Prompt: "p1", RunOpts: runner.RunOpts{RoleID: "prog-1", Action: runner.ActionParallel}},
		{Prompt: "p2", RunOpts: runner.RunOpts{RoleID: "prog-2", Action: runner.ActionParallel}},
	}

	progressCh := make(chan runner.RunProgress, 64)
	completedCh := make(chan runner.RunSummary, len(tasks))

	var progressEvents []runner.RunProgress
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		runner.RunPool(context.Background(), r, tasks, 2, progressCh, completedCh)
		close(progressCh)
	}()

	for p := range progressCh {
		progressEvents = append(progressEvents, p)
	}
	wg.Wait()

	// Drain completed
	for range completedCh {
	}

	if len(progressEvents) == 0 {
		t.Fatal("expected progress events, got none")
	}

	// Verify we got events for both tasks
	rolesSeen := map[string]bool{}
	for _, p := range progressEvents {
		rolesSeen[p.RoleID] = true
	}
	if !rolesSeen["prog-1"] || !rolesSeen["prog-2"] {
		t.Errorf("expected events for both roles, got roles: %v", rolesSeen)
	}
}

func TestParallelPoolStatusIntegration(t *testing.T) {
	labels := []string{"auth-check", "payment-check", "user-check"}
	rows, index := newPoolStatusRows(labels)

	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}

	for i, label := range labels {
		if rows[i].Label != label {
			t.Errorf("expected label %q at index %d, got %q", label, i, rows[i].Label)
		}
		if rows[i].State != poolStateQueued {
			t.Errorf("expected queued state, got %q", rows[i].State)
		}
		if index[label] != i {
			t.Errorf("expected index %d for label %q, got %d", i, label, index[label])
		}
	}

	// Simulate progress update
	rows[0] = nextPoolStatusRow(rows[0], runner.RunProgress{
		ExecID:  1,
		RoleID:  "auth-check",
		Phase:   runner.PhaseInit,
		Elapsed: 2 * time.Second,
	})
	if rows[0].State != poolStateRunning {
		t.Errorf("expected running state after init, got %q", rows[0].State)
	}
	if rows[0].ExecID != 1 {
		t.Errorf("expected exec ID 1, got %d", rows[0].ExecID)
	}

	// Simulate tool call
	rows[0] = nextPoolStatusRow(rows[0], runner.RunProgress{
		ExecID:    1,
		RoleID:    "auth-check",
		Phase:     runner.PhaseTool,
		ToolName:  "Bash",
		ToolCount: 3,
		TurnCount: 2,
		Elapsed:   5 * time.Second,
	})
	if rows[0].Turns != 2 {
		t.Errorf("expected 2 turns, got %d", rows[0].Turns)
	}
	if !strings.Contains(rows[0].Detail, "Bash") {
		t.Errorf("expected detail to contain 'Bash', got %q", rows[0].Detail)
	}

	// Simulate done
	rows[0] = donePoolStatusRow(rows[0], runner.RunSummary{
		ExecID:       1,
		RoleID:       "auth-check",
		Cost:         0.05,
		InputTokens:  1000,
		OutputTokens: 500,
		Duration:     10 * time.Second,
		EndedAt:      time.Now(),
	}, "")
	if rows[0].State != poolStateDone {
		t.Errorf("expected done state, got %q", rows[0].State)
	}

	// Verify done row is terminal
	rows[0] = nextPoolStatusRow(rows[0], runner.RunProgress{
		ExecID: 1,
		Phase:  runner.PhaseTool,
	})
	if rows[0].State != poolStateDone {
		t.Errorf("expected done state to remain after further progress, got %q", rows[0].State)
	}
}

func TestParallelPoolWithCallDB(t *testing.T) {
	dir := t.TempDir()

	dbPath := filepath.Join(dir, "state.sqlite")
	db, err := calldb.Open(dbPath)
	if err != nil {
		t.Fatalf("Open CallDB: %v", err)
	}
	defer db.Close()

	mock := &agent.MockAgent{Response: "tracked output", Cost: 0.03}
	r := &runner.Runner{
		Agent:      mock,
		ProjectDir: dir,
		CallDB:     db,
	}

	batch := "test-parallel-" + time.Now().Format(display.TimestampFormat)
	labels := []string{"task-a", "task-b"}
	tasks := make([]runner.PoolExec, len(labels))
	for i, label := range labels {
		tasks[i] = runner.PoolExec{
			Prompt: fmt.Sprintf("prompt for %s", label),
			RunOpts: runner.RunOpts{
				RoleID: label,
				Action: runner.ActionParallel,
				Batch:  batch,
			},
		}
	}

	runner.RunPool(context.Background(), r, tasks, 2, nil, nil)

	rows, err := db.RecentRuns(calldb.RecentFilter{Action: "parallel", Limit: 10})
	if err != nil {
		t.Fatalf("RecentRuns: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 DB rows, got %d", len(rows))
	}

	for _, row := range rows {
		if row.Batch != batch {
			t.Errorf("expected batch %q, got %q", batch, row.Batch)
		}
		if row.Action != "parallel" {
			t.Errorf("expected action 'parallel', got %q", row.Action)
		}
	}

	aggs, err := db.CostByAction("")
	if err != nil {
		t.Fatalf("CostByAction: %v", err)
	}
	var found bool
	for _, agg := range aggs {
		if agg.Category == "parallel" {
			found = true
			if agg.Count != 2 {
				t.Errorf("expected count 2 for parallel, got %d", agg.Count)
			}
			if agg.CostUSD < 0.05 {
				t.Errorf("expected total cost >= 0.05, got %f", agg.CostUSD)
			}
		}
	}
	if !found {
		t.Error("expected parallel category in cost aggregation")
	}
}

func TestParallelPoolSequentialExecution(t *testing.T) {
	dir := t.TempDir()

	mock := &agent.MockAgent{Response: "sequential", Delay: 10 * time.Millisecond}
	r := newCmdTestRunner(t, dir, mock)

	tasks := make([]runner.PoolExec, 3)
	for i := range tasks {
		tasks[i] = runner.PoolExec{
			Prompt:  fmt.Sprintf("seq-%d", i),
			RunOpts: runner.RunOpts{RoleID: fmt.Sprintf("seq-%d", i), Action: runner.ActionParallel},
		}
	}

	start := time.Now()
	results := runner.RunPool(context.Background(), r, tasks, 1, nil, nil)
	elapsed := time.Since(start)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// With 10ms delay and maxParallel=1, total should be >= 30ms
	if elapsed < 25*time.Millisecond {
		t.Errorf("sequential execution too fast (%v), expected >= 30ms", elapsed)
	}

	for _, s := range results {
		if s.Err != nil {
			t.Errorf("unexpected error: %v", s.Err)
		}
	}
}

func TestParallelPoolOutputCollection(t *testing.T) {
	dir := t.TempDir()
	mock := &agent.MockAgent{Response: "collected output"}
	r := newCmdTestRunner(t, dir, mock)

	labels := []string{"first", "second", "third"}
	tasks := make([]runner.PoolExec, len(labels))
	for i, label := range labels {
		tasks[i] = runner.PoolExec{
			Prompt:  fmt.Sprintf("prompt %s", label),
			RunOpts: runner.RunOpts{RoleID: label, Action: runner.ActionParallel},
		}
	}

	completedCh := make(chan runner.RunSummary, len(tasks))
	runner.RunPool(context.Background(), r, tasks, 3, nil, completedCh)

	outputByLabel := make(map[string]string)
	for s := range completedCh {
		if s.Err == nil {
			outputByLabel[s.RoleID] = s.Output
		}
	}

	for _, label := range labels {
		output, ok := outputByLabel[label]
		if !ok {
			t.Errorf("missing output for label %q", label)
			continue
		}
		if output != "collected output" {
			t.Errorf("expected 'collected output' for %q, got %q", label, output)
		}
	}
}

func TestParallelPoolContextCancellation(t *testing.T) {
	dir := t.TempDir()

	mock := &agent.MockAgent{Response: "slow", Delay: 5 * time.Second}
	r := newCmdTestRunner(t, dir, mock)

	tasks := make([]runner.PoolExec, 3)
	for i := range tasks {
		tasks[i] = runner.PoolExec{
			Prompt:  "slow task",
			RunOpts: runner.RunOpts{RoleID: fmt.Sprintf("cancel-%d", i), Action: runner.ActionParallel},
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	results := runner.RunPool(ctx, r, tasks, 3, nil, nil)
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Errorf("cancellation took too long: %v", elapsed)
	}

	for _, s := range results {
		if s.Err == nil {
			t.Errorf("expected error from cancelled task %s", s.RoleID)
		}
	}
}

func TestParallelBatchInDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.sqlite")
	db, err := calldb.Open(dbPath)
	if err != nil {
		t.Fatalf("Open CallDB: %v", err)
	}
	defer db.Close()

	mock := &agent.MockAgent{Response: "grouped"}
	r := &runner.Runner{Agent: mock, ProjectDir: dir, CallDB: db}

	batch := "parallel-2026-04-03_12-00-00"
	tasks := []runner.PoolExec{
		{Prompt: "g1", RunOpts: runner.RunOpts{RoleID: "grp-1", Action: runner.ActionParallel, Batch: batch}},
		{Prompt: "g2", RunOpts: runner.RunOpts{RoleID: "grp-2", Action: runner.ActionParallel, Batch: batch}},
	}

	runner.RunPool(context.Background(), r, tasks, 2, nil, nil)

	batchRows, err := db.CostByBatch("")
	if err != nil {
		t.Fatalf("CostByBatch: %v", err)
	}

	var found bool
	for _, row := range batchRows {
		if row.Batch == batch {
			found = true
			if row.Count != 2 {
				t.Errorf("expected count 2 for batch, got %d", row.Count)
			}
			if row.Action != "parallel" {
				t.Errorf("expected action 'parallel', got %q", row.Action)
			}
		}
	}
	if !found {
		t.Errorf("batch %q not found in CostByBatch results", batch)
	}

	latest, err := db.LatestBatch("", "parallel-")
	if err != nil {
		t.Fatalf("LatestBatch: %v", err)
	}
	if latest != batch {
		t.Errorf("expected latest batch %q, got %q", batch, latest)
	}
}

func TestParallelPoolStatusRendering(t *testing.T) {
	if !strings.Contains(poolStatusHeader, "LABEL") {
		t.Errorf("expected LABEL in header, got %q", poolStatusHeader)
	}

	labels := []string{"task-1", "task-2"}
	rows, _ := newPoolStatusRows(labels)
	for _, row := range rows {
		line := formatPoolRowSingleLine(row)
		if !strings.Contains(line, row.Label) {
			t.Errorf("expected %q in row line, got %q", row.Label, line)
		}
	}
}
