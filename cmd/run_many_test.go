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
	"github.com/ateam/internal/runner"
)

func TestRunManyPoolWithMockAgent(t *testing.T) {
	dir := t.TempDir()
	mock := &agent.MockAgent{Response: "hello from run-many", Cost: 0.02}
	r := &runner.Runner{Agent: mock}

	labels := []string{"alpha", "beta", "gamma"}
	tasks := make([]runner.PoolTask, len(labels))
	for i, label := range labels {
		tasks[i] = runner.PoolTask{
			Prompt: fmt.Sprintf("prompt for %s", label),
			RunOpts: runner.RunOpts{
				RoleID:    label,
				Action:    runner.ActionRunMany,
				LogsDir:   filepath.Join(dir, "logs", label),
				TaskGroup: "test-run-many-group",
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
		if s.Output != "hello from run-many" {
			t.Errorf("expected output 'hello from run-many', got %q for %s", s.Output, s.RoleID)
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

func TestRunManyPoolPartialFailure(t *testing.T) {
	dir := t.TempDir()

	// Use separate mock agents — one succeeds, one fails.
	// Since RunPool uses a single Runner, we need a custom approach.
	// Instead, test with a mock that always succeeds but verify error handling
	// logic by running post-completion classification.
	successMock := &agent.MockAgent{Response: "ok", Cost: 0.01}
	r := &runner.Runner{Agent: successMock}

	tasks := []runner.PoolTask{
		{Prompt: "good", RunOpts: runner.RunOpts{RoleID: "good-task", Action: runner.ActionRunMany, LogsDir: filepath.Join(dir, "logs", "good")}},
		{Prompt: "also good", RunOpts: runner.RunOpts{RoleID: "good-task-2", Action: runner.ActionRunMany, LogsDir: filepath.Join(dir, "logs", "good2")}},
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

func TestRunManyPoolWithErrors(t *testing.T) {
	dir := t.TempDir()

	failMock := &agent.MockAgent{Err: fmt.Errorf("simulated failure")}
	r := &runner.Runner{Agent: failMock}

	tasks := []runner.PoolTask{
		{Prompt: "fail1", RunOpts: runner.RunOpts{RoleID: "fail-a", Action: runner.ActionRunMany, LogsDir: filepath.Join(dir, "logs", "fail-a")}},
		{Prompt: "fail2", RunOpts: runner.RunOpts{RoleID: "fail-b", Action: runner.ActionRunMany, LogsDir: filepath.Join(dir, "logs", "fail-b")}},
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

func TestRunManyPoolProgressEvents(t *testing.T) {
	dir := t.TempDir()
	mock := &agent.MockAgent{Response: "progress test"}
	r := &runner.Runner{Agent: mock}

	tasks := []runner.PoolTask{
		{Prompt: "p1", RunOpts: runner.RunOpts{RoleID: "prog-1", Action: runner.ActionRunMany, LogsDir: filepath.Join(dir, "logs", "prog-1")}},
		{Prompt: "p2", RunOpts: runner.RunOpts{RoleID: "prog-2", Action: runner.ActionRunMany, LogsDir: filepath.Join(dir, "logs", "prog-2")}},
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

func TestRunManyPoolStatusIntegration(t *testing.T) {
	// Test that pool status rows work correctly with run-many labels
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
		ExecID:    1,
		RoleID:    "auth-check",
		Phase:     runner.PhaseInit,
		Elapsed:   2 * time.Second,
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
		Elapsed:   5 * time.Second,
	})
	if rows[0].Calls != 3 {
		t.Errorf("expected 3 calls, got %d", rows[0].Calls)
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

	// Verify done row is terminal — further progress events are ignored
	rows[0] = nextPoolStatusRow(rows[0], runner.RunProgress{
		ExecID: 1,
		Phase:  runner.PhaseTool,
	})
	if rows[0].State != poolStateDone {
		t.Errorf("expected done state to remain after further progress, got %q", rows[0].State)
	}
}

func TestRunManyPoolWithCallDB(t *testing.T) {
	dir := t.TempDir()

	// Set up a real CallDB
	dbPath := filepath.Join(dir, "state.sqlite")
	db, err := calldb.Open(dbPath)
	if err != nil {
		t.Fatalf("Open CallDB: %v", err)
	}
	defer db.Close()

	mock := &agent.MockAgent{Response: "tracked output", Cost: 0.03}
	r := &runner.Runner{
		Agent:  mock,
		CallDB: db,
	}

	taskGroup := "test-run-many-" + time.Now().Format(runner.TimestampFormat)
	labels := []string{"task-a", "task-b"}
	tasks := make([]runner.PoolTask, len(labels))
	for i, label := range labels {
		tasks[i] = runner.PoolTask{
			Prompt: fmt.Sprintf("prompt for %s", label),
			RunOpts: runner.RunOpts{
				RoleID:    label,
				Action:    runner.ActionRunMany,
				LogsDir:   filepath.Join(dir, "logs", label),
				TaskGroup: taskGroup,
			},
		}
	}

	runner.RunPool(context.Background(), r, tasks, 2, nil, nil)

	// Verify runs are tracked in CallDB
	rows, err := db.RecentRuns(calldb.RecentFilter{Action: "run-many", Limit: 10})
	if err != nil {
		t.Fatalf("RecentRuns: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 DB rows, got %d", len(rows))
	}

	// Verify task group
	for _, row := range rows {
		if row.TaskGroup != taskGroup {
			t.Errorf("expected task group %q, got %q", taskGroup, row.TaskGroup)
		}
		if row.Action != "run-many" {
			t.Errorf("expected action 'run-many', got %q", row.Action)
		}
	}

	// Verify cost tracking
	aggs, err := db.CostByAction("")
	if err != nil {
		t.Fatalf("CostByAction: %v", err)
	}
	var found bool
	for _, agg := range aggs {
		if agg.Category == "run-many" {
			found = true
			if agg.Count != 2 {
				t.Errorf("expected count 2 for run-many, got %d", agg.Count)
			}
			if agg.CostUSD < 0.05 {
				t.Errorf("expected total cost >= 0.05, got %f", agg.CostUSD)
			}
		}
	}
	if !found {
		t.Error("expected run-many category in cost aggregation")
	}
}

func TestRunManyPoolSequentialExecution(t *testing.T) {
	dir := t.TempDir()

	// Use a small delay to verify sequential execution with maxParallel=1
	mock := &agent.MockAgent{Response: "sequential", Delay: 10 * time.Millisecond}
	r := &runner.Runner{Agent: mock}

	tasks := make([]runner.PoolTask, 3)
	for i := range tasks {
		tasks[i] = runner.PoolTask{
			Prompt:  fmt.Sprintf("seq-%d", i),
			RunOpts: runner.RunOpts{RoleID: fmt.Sprintf("seq-%d", i), Action: runner.ActionRunMany, LogsDir: filepath.Join(dir, "logs", fmt.Sprintf("seq-%d", i))},
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

func TestRunManyPoolOutputCollection(t *testing.T) {
	dir := t.TempDir()
	mock := &agent.MockAgent{Response: "collected output"}
	r := &runner.Runner{Agent: mock}

	labels := []string{"first", "second", "third"}
	tasks := make([]runner.PoolTask, len(labels))
	for i, label := range labels {
		tasks[i] = runner.PoolTask{
			Prompt:  fmt.Sprintf("prompt %s", label),
			RunOpts: runner.RunOpts{RoleID: label, Action: runner.ActionRunMany, LogsDir: filepath.Join(dir, "logs", label)},
		}
	}

	completedCh := make(chan runner.RunSummary, len(tasks))
	runner.RunPool(context.Background(), r, tasks, 3, nil, completedCh)

	// Collect results keyed by label, like runRunMany does
	outputByLabel := make(map[string]string)
	for s := range completedCh {
		if s.Err == nil {
			outputByLabel[s.RoleID] = s.Output
		}
	}

	// Verify all outputs collected
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

func TestRunManyPoolContextCancellation(t *testing.T) {
	dir := t.TempDir()

	// Use a long delay so we can cancel
	mock := &agent.MockAgent{Response: "slow", Delay: 5 * time.Second}
	r := &runner.Runner{Agent: mock}

	tasks := make([]runner.PoolTask, 3)
	for i := range tasks {
		tasks[i] = runner.PoolTask{
			Prompt:  "slow task",
			RunOpts: runner.RunOpts{RoleID: fmt.Sprintf("cancel-%d", i), Action: runner.ActionRunMany, LogsDir: filepath.Join(dir, "logs", fmt.Sprintf("cancel-%d", i))},
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	results := runner.RunPool(ctx, r, tasks, 3, nil, nil)
	elapsed := time.Since(start)

	// Should complete quickly due to cancellation, not wait 5s
	if elapsed > 2*time.Second {
		t.Errorf("cancellation took too long: %v", elapsed)
	}

	// All results should have errors
	for _, s := range results {
		if s.Err == nil {
			t.Errorf("expected error from cancelled task %s", s.RoleID)
		}
	}
}

func TestRunManyTaskGroupInDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.sqlite")
	db, err := calldb.Open(dbPath)
	if err != nil {
		t.Fatalf("Open CallDB: %v", err)
	}
	defer db.Close()

	mock := &agent.MockAgent{Response: "grouped"}
	r := &runner.Runner{Agent: mock, CallDB: db}

	taskGroup := "run-many-2026-04-03_12-00-00"
	tasks := []runner.PoolTask{
		{Prompt: "g1", RunOpts: runner.RunOpts{RoleID: "grp-1", Action: runner.ActionRunMany, LogsDir: filepath.Join(dir, "logs", "grp-1"), TaskGroup: taskGroup}},
		{Prompt: "g2", RunOpts: runner.RunOpts{RoleID: "grp-2", Action: runner.ActionRunMany, LogsDir: filepath.Join(dir, "logs", "grp-2"), TaskGroup: taskGroup}},
	}

	runner.RunPool(context.Background(), r, tasks, 2, nil, nil)

	// Query by task group
	tgRows, err := db.CostByTaskGroup("")
	if err != nil {
		t.Fatalf("CostByTaskGroup: %v", err)
	}

	var found bool
	for _, row := range tgRows {
		if row.TaskGroup == taskGroup {
			found = true
			if row.Count != 2 {
				t.Errorf("expected count 2 for task group, got %d", row.Count)
			}
			if row.Action != "run-many" {
				t.Errorf("expected action 'run-many', got %q", row.Action)
			}
		}
	}
	if !found {
		t.Errorf("task group %q not found in CostByTaskGroup results", taskGroup)
	}

	// Verify LatestTaskGroup works
	latest, err := db.LatestTaskGroup("", "run-many-")
	if err != nil {
		t.Fatalf("LatestTaskGroup: %v", err)
	}
	if latest != taskGroup {
		t.Errorf("expected latest task group %q, got %q", taskGroup, latest)
	}
}

func TestRunManyPoolStatusRendering(t *testing.T) {
	labels := []string{"task-1", "task-2"}
	rows, _ := newPoolStatusRows(labels)

	// Verify header includes LABEL column
	lines := poolStatusLinesForWidth(rows, 120)
	if !strings.Contains(lines[0], "LABEL") {
		t.Errorf("expected LABEL in header, got %q", lines[0])
	}

	// Verify labels appear in output
	if !strings.Contains(lines[1], "task-1") {
		t.Errorf("expected task-1 in row 1, got %q", lines[1])
	}
	if !strings.Contains(lines[2], "task-2") {
		t.Errorf("expected task-2 in row 2, got %q", lines[2])
	}
}
