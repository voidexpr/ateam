package calldb

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestOpenCreatesTables(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	var name string
	err = db.db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='agent_calls'").Scan(&name)
	if err != nil {
		t.Fatalf("table not created: %v", err)
	}
	if name != "agent_calls" {
		t.Fatalf("expected agent_calls, got %s", name)
	}
}

func TestInsertAndUpdate(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	now := time.Now()
	id, err := db.InsertCall(&Call{
		ProjectID:  "myproject",
		Profile:    "default",
		Agent:      "claude",
		Container:  "none",
		Action:     "run",
		Role:       "security",
		TaskGroup:  "code-2026-03-13",
		Model:      "opus",
		PromptHash: "abc123",
		StartedAt:  now,
		StreamFile: "/tmp/stream.jsonl",
	})
	if err != nil {
		t.Fatalf("InsertCall: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive ID, got %d", id)
	}

	err = db.UpdateCall(id, &CallResult{
		EndedAt:         now.Add(30 * time.Second),
		DurationMS:      30000,
		ExitCode:        0,
		IsError:         false,
		CostUSD:         0.05,
		InputTokens:     1000,
		OutputTokens:    500,
		CacheReadTokens: 200,
		Turns:           3,
	})
	if err != nil {
		t.Fatalf("UpdateCall: %v", err)
	}

	var costUSD float64
	var inputTokens int
	err = db.db.QueryRow("SELECT cost_usd, input_tokens FROM agent_calls WHERE id = ?", id).Scan(&costUSD, &inputTokens)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if costUSD != 0.05 {
		t.Fatalf("expected cost 0.05, got %f", costUSD)
	}
	if inputTokens != 1000 {
		t.Fatalf("expected 1000 input tokens, got %d", inputTokens)
	}
}

func TestConcurrentInserts(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	var wg sync.WaitGroup
	n := 20
	errs := make([]error, n)

	for i := range n {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = db.InsertCall(&Call{
				ProjectID: "proj",
				Agent:     "claude",
				Container: "none",
				Action:    "run",
				StartedAt: time.Now(),
			})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: %v", i, err)
		}
	}

	var count int
	db.db.QueryRow("SELECT COUNT(*) FROM agent_calls").Scan(&count)
	if count != n {
		t.Fatalf("expected %d rows, got %d", n, count)
	}
}

func testDB(t *testing.T) *CallDB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func seedCalls(t *testing.T, db *CallDB) {
	t.Helper()
	now := time.Now()
	calls := []struct {
		call   Call
		result CallResult
	}{
		{
			Call{ProjectID: "proj-a", Action: "report", Role: "security", TaskGroup: "report-2026-03-13_09-00-00", StartedAt: now.Add(-3 * time.Hour)},
			CallResult{EndedAt: now.Add(-3*time.Hour + 2*time.Minute), DurationMS: 120000, CostUSD: 0.10, InputTokens: 5000, OutputTokens: 1000, CacheReadTokens: 500},
		},
		{
			Call{ProjectID: "proj-a", Action: "report", Role: "testing", TaskGroup: "report-2026-03-13_09-00-00", StartedAt: now.Add(-3*time.Hour + time.Minute)},
			CallResult{EndedAt: now.Add(-3*time.Hour + 3*time.Minute), DurationMS: 120000, CostUSD: 0.08, InputTokens: 4000, OutputTokens: 800, CacheReadTokens: 300},
		},
		{
			Call{ProjectID: "proj-a", Action: "review", Role: "supervisor", StartedAt: now.Add(-2 * time.Hour)},
			CallResult{EndedAt: now.Add(-2*time.Hour + 5*time.Minute), DurationMS: 300000, CostUSD: 0.20, InputTokens: 10000, OutputTokens: 2000, CacheReadTokens: 1000},
		},
		{
			Call{ProjectID: "proj-a", Action: "code", Role: "supervisor", TaskGroup: "code-2026-03-13_10-00-00", StartedAt: now.Add(-1 * time.Hour)},
			CallResult{EndedAt: now.Add(-1*time.Hour + 10*time.Minute), DurationMS: 600000, CostUSD: 0.50, InputTokens: 20000, OutputTokens: 5000, CacheReadTokens: 2000},
		},
		{
			Call{ProjectID: "proj-a", Action: "run", Role: "security", TaskGroup: "code-2026-03-13_10-00-00", StartedAt: now.Add(-50 * time.Minute)},
			CallResult{EndedAt: now.Add(-45 * time.Minute), DurationMS: 300000, CostUSD: 0.15, InputTokens: 8000, OutputTokens: 1500, CacheReadTokens: 600},
		},
		{
			Call{ProjectID: "proj-a", Action: "run", Role: "testing", TaskGroup: "code-2026-03-13_10-00-00", StartedAt: now.Add(-44 * time.Minute)},
			CallResult{EndedAt: now.Add(-40 * time.Minute), DurationMS: 240000, CostUSD: 0.12, InputTokens: 6000, OutputTokens: 1200, CacheReadTokens: 400},
		},
		{
			Call{ProjectID: "proj-a", Action: "run", Role: "security", StartedAt: now.Add(-30 * time.Minute)},
			CallResult{EndedAt: now.Add(-25 * time.Minute), DurationMS: 300000, CostUSD: 0.10, InputTokens: 5000, OutputTokens: 1000, CacheReadTokens: 400},
		},
		{
			Call{ProjectID: "proj-b", Action: "report", Role: "security", StartedAt: now.Add(-20 * time.Minute)},
			CallResult{EndedAt: now.Add(-15 * time.Minute), DurationMS: 300000, CostUSD: 0.09, InputTokens: 4500, OutputTokens: 900, CacheReadTokens: 350},
		},
	}

	for _, c := range calls {
		id, err := db.InsertCall(&c.call)
		if err != nil {
			t.Fatalf("InsertCall: %v", err)
		}
		if err := db.UpdateCall(id, &c.result); err != nil {
			t.Fatalf("UpdateCall: %v", err)
		}
	}
}

func TestRecentRuns(t *testing.T) {
	db := testDB(t)
	seedCalls(t, db)

	// All runs, no filter
	rows, err := db.RecentRuns(RecentFilter{Limit: 100})
	if err != nil {
		t.Fatalf("RecentRuns: %v", err)
	}
	if len(rows) != 8 {
		t.Fatalf("expected 8 rows, got %d", len(rows))
	}
	// Should be ordered by started_at ASC — first row is oldest
	if rows[0].ProjectID != "proj-a" {
		t.Errorf("expected proj-a first, got %s", rows[0].ProjectID)
	}

	// Filter by project
	rows, err = db.RecentRuns(RecentFilter{ProjectID: "proj-a"})
	if err != nil {
		t.Fatalf("RecentRuns: %v", err)
	}
	if len(rows) != 7 {
		t.Fatalf("expected 7 rows for proj-a, got %d", len(rows))
	}

	// Filter by role
	rows, err = db.RecentRuns(RecentFilter{Role: "security"})
	if err != nil {
		t.Fatalf("RecentRuns: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("expected 4 security rows, got %d", len(rows))
	}

	// Filter by action
	rows, err = db.RecentRuns(RecentFilter{Action: "report"})
	if err != nil {
		t.Fatalf("RecentRuns: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 report rows, got %d", len(rows))
	}
}

func TestCostByAction(t *testing.T) {
	db := testDB(t)
	seedCalls(t, db)

	aggs, err := db.CostByAction("")
	if err != nil {
		t.Fatalf("CostByAction: %v", err)
	}

	catMap := make(map[string]ActionAgg)
	for _, a := range aggs {
		catMap[a.Category] = a
	}

	// The two runs with task_group "code-..." should be categorized as "code-task-run"
	if ctr, ok := catMap["code-task-run"]; !ok {
		t.Fatal("expected code-task-run category")
	} else if ctr.Count != 2 {
		t.Errorf("expected 2 code-task-run, got %d", ctr.Count)
	}

	// The standalone "run" (no code task_group) should stay as "run"
	if r, ok := catMap["run"]; !ok {
		t.Fatal("expected run category")
	} else if r.Count != 1 {
		t.Errorf("expected 1 standalone run, got %d", r.Count)
	}

	// Filter by project
	aggs, err = db.CostByAction("proj-b")
	if err != nil {
		t.Fatalf("CostByAction: %v", err)
	}
	if len(aggs) != 1 {
		t.Fatalf("expected 1 category for proj-b, got %d", len(aggs))
	}
	if aggs[0].Category != "report" || aggs[0].Count != 1 {
		t.Errorf("unexpected: %+v", aggs[0])
	}
}

func TestCostByTaskGroup(t *testing.T) {
	db := testDB(t)
	seedCalls(t, db)

	rows, err := db.CostByTaskGroup("")
	if err != nil {
		t.Fatalf("CostByTaskGroup: %v", err)
	}

	// Group rows by task_group
	groups := make(map[string]map[string]TaskGroupRow)
	for _, r := range rows {
		if groups[r.TaskGroup] == nil {
			groups[r.TaskGroup] = make(map[string]TaskGroupRow)
		}
		groups[r.TaskGroup][r.Action] = r
	}

	// code-2026-03-13_10-00-00: code + run
	codeGroup := groups["code-2026-03-13_10-00-00"]
	if codeGroup == nil {
		t.Fatal("expected code task group")
	}
	if code, ok := codeGroup["code"]; !ok {
		t.Fatal("expected code action in code group")
	} else if code.Count != 1 {
		t.Errorf("expected 1 code call, got %d", code.Count)
	}
	if run, ok := codeGroup["run"]; !ok {
		t.Fatal("expected run action in code group")
	} else if run.Count != 2 {
		t.Errorf("expected 2 run calls, got %d", run.Count)
	}

	// report-2026-03-13_09-00-00: report
	reportGroup := groups["report-2026-03-13_09-00-00"]
	if reportGroup == nil {
		t.Fatal("expected report task group")
	}
	if rep, ok := reportGroup["report"]; !ok {
		t.Fatal("expected report action in report group")
	} else if rep.Count != 2 {
		t.Errorf("expected 2 report calls, got %d", rep.Count)
	}
}

func TestMigrateIdempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")

	// First open creates table + migrates
	db1, err := Open(dbPath)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	db1.Close()

	// Second open re-runs migrate — should be a no-op
	db2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer db2.Close()

	// Verify columns exist
	var pid int
	var containerID string
	id, err := db2.InsertCall(&Call{
		ProjectID: "proj", Agent: "claude", Container: "none",
		Action: "run", StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("InsertCall: %v", err)
	}
	if err := db2.SetPID(id, 12345, "ateam-proj-security"); err != nil {
		t.Fatalf("SetPID: %v", err)
	}
	err = db2.db.QueryRow("SELECT pid, container_id FROM agent_calls WHERE id = ?", id).Scan(&pid, &containerID)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if pid != 12345 {
		t.Errorf("expected pid 12345, got %d", pid)
	}
	if containerID != "ateam-proj-security" {
		t.Errorf("expected container_id ateam-proj-security, got %s", containerID)
	}
}

func TestDBErrorsDoNotPanic(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	db.Close()

	// Operations on closed DB should return errors, not panic.
	_, err = db.InsertCall(&Call{StartedAt: time.Now()})
	if err == nil {
		t.Fatal("expected error on closed DB insert")
	}

	err = db.UpdateCall(1, &CallResult{EndedAt: time.Now()})
	if err == nil {
		t.Fatal("expected error on closed DB update")
	}

	// Opening at invalid path should fail.
	_, err = Open("/nonexistent/path/db.sqlite")
	if err == nil {
		t.Fatal("expected error opening invalid path")
	}
}
