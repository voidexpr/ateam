package calldb

import (
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestOpenCreatesTables(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	var name string
	err = db.db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='agent_execs'").Scan(&name)
	if err != nil {
		t.Fatalf("table not created: %v", err)
	}
	if name != "agent_execs" {
		t.Fatalf("expected agent_execs, got %s", name)
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
	err = db.db.QueryRow("SELECT cost_usd, input_tokens FROM agent_execs WHERE id = ?", id).Scan(&costUSD, &inputTokens)
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
	_ = db.db.QueryRow("SELECT COUNT(*) FROM agent_execs").Scan(&count)
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
			Call{ProjectID: "proj-a", Action: "report", Role: "security", TaskGroup: "report-2026-03-13_09-00-00", StartedAt: now.Add(-3 * time.Hour), StreamFile: "/logs/report_security.jsonl"},
			CallResult{EndedAt: now.Add(-3*time.Hour + 2*time.Minute), DurationMS: 120000, CostUSD: 0.10, InputTokens: 5000, OutputTokens: 1000, CacheReadTokens: 500},
		},
		{
			Call{ProjectID: "proj-a", Action: "report", Role: "testing", TaskGroup: "report-2026-03-13_09-00-00", StartedAt: now.Add(-3*time.Hour + time.Minute), StreamFile: "/logs/report_testing.jsonl"},
			CallResult{EndedAt: now.Add(-3*time.Hour + 3*time.Minute), DurationMS: 120000, CostUSD: 0.08, InputTokens: 4000, OutputTokens: 800, CacheReadTokens: 300},
		},
		{
			Call{ProjectID: "proj-a", Action: "review", Role: "supervisor", StartedAt: now.Add(-2 * time.Hour)},
			CallResult{EndedAt: now.Add(-2*time.Hour + 5*time.Minute), DurationMS: 300000, CostUSD: 0.20, InputTokens: 10000, OutputTokens: 2000, CacheReadTokens: 1000},
		},
		{
			Call{ProjectID: "proj-a", Action: "code", Role: "supervisor", TaskGroup: "code-2026-03-13_10-00-00", StartedAt: now.Add(-1 * time.Hour), StreamFile: "/logs/code_supervisor.jsonl"},
			CallResult{EndedAt: now.Add(-1*time.Hour + 10*time.Minute), DurationMS: 600000, CostUSD: 0.50, InputTokens: 20000, OutputTokens: 5000, CacheReadTokens: 2000},
		},
		{
			Call{ProjectID: "proj-a", Action: "run", Role: "security", TaskGroup: "code-2026-03-13_10-00-00", StartedAt: now.Add(-50 * time.Minute), StreamFile: "/logs/run_security.jsonl"},
			CallResult{EndedAt: now.Add(-45 * time.Minute), DurationMS: 300000, CostUSD: 0.15, InputTokens: 8000, OutputTokens: 1500, CacheReadTokens: 600},
		},
		{
			Call{ProjectID: "proj-a", Action: "run", Role: "testing", TaskGroup: "code-2026-03-13_10-00-00", StartedAt: now.Add(-44 * time.Minute), StreamFile: "/logs/run_testing.jsonl"},
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
	// Should be ordered by started_at DESC — first row is newest
	if rows[0].ProjectID != "proj-b" {
		t.Errorf("expected proj-b first (newest), got %s", rows[0].ProjectID)
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
	err = db2.db.QueryRow("SELECT pid, container_id FROM agent_execs WHERE id = ?", id).Scan(&pid, &containerID)
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

func TestMigrateFromOldTableName(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.sqlite")

	// Manually create old-style agent_calls table with absolute stream_file paths.
	dsn := dbPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	rawDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_, err = rawDB.Exec(`
		CREATE TABLE agent_calls (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_id TEXT NOT NULL DEFAULT '',
			profile TEXT NOT NULL DEFAULT '',
			agent TEXT NOT NULL DEFAULT '',
			container TEXT NOT NULL DEFAULT 'none',
			action TEXT NOT NULL DEFAULT '',
			role TEXT NOT NULL DEFAULT '',
			task_group TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			prompt_hash TEXT NOT NULL DEFAULT '',
			started_at TEXT NOT NULL,
			stream_file TEXT NOT NULL DEFAULT '',
			ended_at TEXT,
			duration_ms INTEGER,
			exit_code INTEGER,
			is_error INTEGER NOT NULL DEFAULT 0,
			error_message TEXT NOT NULL DEFAULT '',
			cost_usd REAL,
			input_tokens INTEGER,
			output_tokens INTEGER,
			cache_read_tokens INTEGER,
			turns INTEGER,
			pid INTEGER NOT NULL DEFAULT 0,
			container_id TEXT NOT NULL DEFAULT ''
		);
		CREATE INDEX idx_calls_started ON agent_calls(started_at);
	`)
	if err != nil {
		t.Fatalf("create old table: %v", err)
	}
	// Insert a row with an absolute stream_file path.
	absStream := filepath.Join(dir, "logs", "2026-01-01_stream.jsonl")
	_, err = rawDB.Exec(`INSERT INTO agent_calls (project_id, started_at, stream_file) VALUES ('proj', '2026-01-01T00:00:00Z', ?)`, absStream)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	rawDB.Close()

	// Open via calldb — should auto-migrate.
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	// Verify table was renamed.
	var tableName string
	err = db.db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='agent_execs'").Scan(&tableName)
	if err != nil {
		t.Fatalf("agent_execs table not found: %v", err)
	}

	// Verify old table is gone.
	var oldCount int
	_ = db.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='agent_calls'").Scan(&oldCount)
	if oldCount != 0 {
		t.Fatal("old agent_calls table still exists")
	}

	// Verify stream_file was converted to relative.
	var sf string
	err = db.db.QueryRow("SELECT stream_file FROM agent_execs WHERE id = 1").Scan(&sf)
	if err != nil {
		t.Fatalf("query stream_file: %v", err)
	}
	expected := filepath.Join("logs", "2026-01-01_stream.jsonl")
	if sf != expected {
		t.Errorf("expected relative path %q, got %q", expected, sf)
	}
}

func TestOpenIfExistsReturnsNilForMissingFile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "nonexistent.sqlite")

	db, err := OpenIfExists(dbPath)
	if err != nil {
		t.Fatalf("OpenIfExists: %v", err)
	}
	if db != nil {
		db.Close()
		t.Fatal("expected nil db for missing file")
	}

	// Verify the file was NOT created.
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("expected file to not exist, but got err=%v", err)
	}
}

func TestOpenIfExistsOpensExistingFile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "existing.sqlite")

	// Create the DB first via Open.
	db1, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	db1.Close()

	// OpenIfExists should open the existing file.
	db2, err := OpenIfExists(dbPath)
	if err != nil {
		t.Fatalf("OpenIfExists: %v", err)
	}
	if db2 == nil {
		t.Fatal("expected non-nil db for existing file")
	}
	db2.Close()
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

func TestCallsByIDs(t *testing.T) {
	db := testDB(t)
	seedCalls(t, db)

	rows, err := db.CallsByIDs([]int64{1, 4, 5})
	if err != nil {
		t.Fatalf("CallsByIDs: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	if rows[0].ID != 1 {
		t.Errorf("expected first ID=1, got %d", rows[0].ID)
	}
	if rows[0].StreamFile != "/logs/report_security.jsonl" {
		t.Errorf("expected stream file, got %q", rows[0].StreamFile)
	}

	// Empty IDs returns nil
	rows, err = db.CallsByIDs(nil)
	if err != nil {
		t.Fatalf("CallsByIDs nil: %v", err)
	}
	if rows != nil {
		t.Fatalf("expected nil for empty IDs, got %d rows", len(rows))
	}
}

func TestCallsByTaskGroup(t *testing.T) {
	db := testDB(t)
	seedCalls(t, db)

	rows, err := db.CallsByTaskGroup("code-2026-03-13_10-00-00")
	if err != nil {
		t.Fatalf("CallsByTaskGroup: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows in code task group, got %d", len(rows))
	}
	// Should include the supervisor (code) and two sub-runs
	actions := map[string]int{}
	for _, r := range rows {
		actions[r.Action]++
	}
	if actions["code"] != 1 {
		t.Errorf("expected 1 code action, got %d", actions["code"])
	}
	if actions["run"] != 2 {
		t.Errorf("expected 2 run actions, got %d", actions["run"])
	}
}

func TestLatestTaskGroup(t *testing.T) {
	db := testDB(t)
	seedCalls(t, db)

	tg, err := db.LatestTaskGroup("proj-a", "code-")
	if err != nil {
		t.Fatalf("LatestTaskGroup: %v", err)
	}
	if tg != "code-2026-03-13_10-00-00" {
		t.Errorf("expected code-2026-03-13_10-00-00, got %q", tg)
	}

	// No match
	tg, err = db.LatestTaskGroup("proj-a", "nonexistent-")
	if err != nil {
		t.Fatalf("LatestTaskGroup no match: %v", err)
	}
	if tg != "" {
		t.Errorf("expected empty string, got %q", tg)
	}
}

func TestRecentRunsStreamFile(t *testing.T) {
	db := testDB(t)
	seedCalls(t, db)

	rows, err := db.RecentRuns(RecentFilter{TaskGroup: "code-2026-03-13_10-00-00"})
	if err != nil {
		t.Fatalf("RecentRuns: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	// DESC order: newest first — testing is most recent in this task group
	if rows[0].StreamFile != "/logs/run_testing.jsonl" {
		t.Errorf("expected stream file on testing (newest), got %q", rows[0].StreamFile)
	}
}

func TestGetRunByID(t *testing.T) {
	db := testDB(t)
	seedCalls(t, db)

	// Existing row
	row, err := db.GetRunByID(1)
	if err != nil {
		t.Fatalf("GetRunByID: %v", err)
	}
	if row == nil {
		t.Fatal("expected non-nil row for ID 1")
	}
	if row.ID != 1 {
		t.Errorf("expected ID 1, got %d", row.ID)
	}
	if row.ProjectID != "proj-a" {
		t.Errorf("expected proj-a, got %s", row.ProjectID)
	}
	if row.Action != "report" {
		t.Errorf("expected action report, got %s", row.Action)
	}
	if row.Role != "security" {
		t.Errorf("expected role security, got %s", row.Role)
	}
	if row.CostUSD != 0.10 {
		t.Errorf("expected cost 0.10, got %f", row.CostUSD)
	}

	// Non-existent row
	row, err = db.GetRunByID(9999)
	if err != nil {
		t.Fatalf("GetRunByID non-existent: %v", err)
	}
	if row != nil {
		t.Fatalf("expected nil for non-existent ID, got %+v", row)
	}
}

func TestRunCostByActionRole(t *testing.T) {
	db := testDB(t)
	seedCalls(t, db)

	// security + run: seed has two entries (IDs 5 and 7)
	costs, err := db.RunCostByActionRole("run", "security")
	if err != nil {
		t.Fatalf("RunCostByActionRole: %v", err)
	}
	if len(costs) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(costs))
	}
	var totalCost float64
	for _, rc := range costs {
		totalCost += rc.CostUSD
		if rc.TotalTokens <= 0 {
			t.Errorf("expected positive TotalTokens, got %d", rc.TotalTokens)
		}
	}
	if totalCost != 0.25 {
		t.Errorf("expected total cost 0.25, got %f", totalCost)
	}

	// No matches
	costs, err = db.RunCostByActionRole("nonexistent", "nobody")
	if err != nil {
		t.Fatalf("RunCostByActionRole no match: %v", err)
	}
	if len(costs) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(costs))
	}
}

func TestFindRunning(t *testing.T) {
	db := testDB(t)
	seedCalls(t, db)

	// All seeded calls are completed — FindRunning should return none
	rows, err := db.FindRunning("proj-a", "")
	if err != nil {
		t.Fatalf("FindRunning: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected 0 running rows, got %d", len(rows))
	}

	// Insert a call without UpdateCall so ended_at stays NULL
	now := time.Now()
	id1, err := db.InsertCall(&Call{
		ProjectID: "proj-a", Action: "run", Role: "security",
		StartedAt: now,
	})
	if err != nil {
		t.Fatalf("InsertCall: %v", err)
	}
	id2, err := db.InsertCall(&Call{
		ProjectID: "proj-a", Action: "report", Role: "testing",
		StartedAt: now,
	})
	if err != nil {
		t.Fatalf("InsertCall: %v", err)
	}
	// Different project — should not appear
	if _, err := db.InsertCall(&Call{
		ProjectID: "proj-b", Action: "run", Role: "security",
		StartedAt: now,
	}); err != nil {
		t.Fatalf("InsertCall: %v", err)
	}

	// All running for proj-a
	rows, err = db.FindRunning("proj-a", "")
	if err != nil {
		t.Fatalf("FindRunning: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 running rows, got %d", len(rows))
	}
	ids := map[int64]bool{}
	for _, r := range rows {
		ids[r.ID] = true
	}
	if !ids[id1] || !ids[id2] {
		t.Errorf("expected IDs %d and %d, got %v", id1, id2, rows)
	}

	// Filter by action
	rows, err = db.FindRunning("proj-a", "run")
	if err != nil {
		t.Fatalf("FindRunning with action: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 running row, got %d", len(rows))
	}
	if rows[0].Role != "security" {
		t.Errorf("expected role security, got %s", rows[0].Role)
	}

	// proj-b should have 1 running
	rows, err = db.FindRunning("proj-b", "")
	if err != nil {
		t.Fatalf("FindRunning proj-b: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 running row for proj-b, got %d", len(rows))
	}
}

func TestRenameProject(t *testing.T) {
	db := testDB(t)

	now := time.Now()
	for _, c := range []Call{
		{ProjectID: "services_api", Action: "report", Role: "security", StartedAt: now, StreamFile: "projects/services_api/roles/security/logs/stream.jsonl"},
		{ProjectID: "services_api", Action: "run", Role: "testing", StartedAt: now, StreamFile: "projects/services_api/roles/testing/logs/stream.jsonl"},
		{ProjectID: "other_proj", Action: "run", Role: "security", StartedAt: now, StreamFile: "projects/other_proj/roles/security/logs/stream.jsonl"},
	} {
		if _, err := db.InsertCall(&c); err != nil {
			t.Fatalf("InsertCall: %v", err)
		}
	}

	n, err := db.RenameProject("services_api", "backends_api")
	if err != nil {
		t.Fatalf("RenameProject: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 rows affected, got %d", n)
	}

	// Verify project_id updated
	rows, err := db.RecentRuns(RecentFilter{ProjectID: "backends_api"})
	if err != nil {
		t.Fatalf("RecentRuns: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows with new project_id, got %d", len(rows))
	}

	// Verify old project_id gone
	rows, err = db.RecentRuns(RecentFilter{ProjectID: "services_api"})
	if err != nil {
		t.Fatalf("RecentRuns: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected 0 rows with old project_id, got %d", len(rows))
	}

	// Verify stream_file paths updated
	var sf string
	err = db.db.QueryRow("SELECT stream_file FROM agent_execs WHERE id = 1").Scan(&sf)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if sf != "projects/backends_api/roles/security/logs/stream.jsonl" {
		t.Errorf("expected updated stream_file, got %q", sf)
	}

	// Verify other project untouched
	err = db.db.QueryRow("SELECT stream_file FROM agent_execs WHERE id = 3").Scan(&sf)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if sf != "projects/other_proj/roles/security/logs/stream.jsonl" {
		t.Errorf("expected other project stream_file unchanged, got %q", sf)
	}
}
