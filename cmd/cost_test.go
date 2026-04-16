package cmd

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/root"
)

// seedCostDB seeds the calldb with records spread across two task groups and
// multiple actions with known token counts for asserting aggregated output.
//
//   - report task group: two "report" runs (testing_basic + security), each 1000 input + 500 output tokens
//   - code task group:   one "code" run (testing_basic) with 2000 input + 800 output tokens,
//                        one "run" run (testing_basic) with 300 input + 100 output tokens
//   - standalone run:    one "review" run (supervisor) with no task group, 600 input + 200 output tokens
func seedCostDB(t *testing.T, db *calldb.CallDB, projectID string) {
	t.Helper()
	now := time.Now()

	insert := func(action, role, tg string, offset time.Duration, inputTok, outputTok int) {
		id, err := db.InsertCall(&calldb.Call{
			ProjectID: projectID, Action: action, Role: role,
			TaskGroup: tg, StartedAt: now.Add(offset),
		})
		if err != nil {
			t.Fatalf("InsertCall(%s/%s): %v", action, role, err)
		}
		if err := db.UpdateCall(id, &calldb.CallResult{
			EndedAt:      now.Add(offset + time.Minute),
			DurationMS:   60000,
			CostUSD:      0.01,
			InputTokens:  inputTok,
			OutputTokens: outputTok,
		}); err != nil {
			t.Fatalf("UpdateCall: %v", err)
		}
	}

	reportTG := "report-2026-03-01_09-00-00"
	codeTG := "code-2026-03-01_10-00-00"

	insert("report", "testing_basic", reportTG, -30*time.Minute, 1000, 500)
	insert("report", "security", reportTG, -30*time.Minute, 1000, 500)
	insert("code", "testing_basic", codeTG, -20*time.Minute, 2000, 800)
	insert("run", "testing_basic", codeTG, -20*time.Minute, 300, 100)
	insert("review", "supervisor", "", -10*time.Minute, 600, 200)
}

// TestCostOutputContainsActionTotals verifies that the cost command produces
// output with per-action rows and a TOTAL row for the all-time section.
func TestCostOutputContainsActionTotals(t *testing.T) {
	base, projPath, env := setupTestProject(t)

	db, err := calldb.Open(env.ProjectDBPath())
	if err != nil {
		t.Fatalf("Open calldb: %v", err)
	}
	seedCostDB(t, db, env.ProjectID())
	db.Close()

	savedOrg := orgFlag
	defer func() { orgFlag = savedOrg }()
	orgFlag = filepath.Dir(env.OrgDir)

	_ = base // suppress unused warning; base holds the org parent
	var costErr error
	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			costErr = runCost(nil, nil)
		})
	})
	if costErr != nil {
		t.Fatalf("runCost: %v", costErr)
	}

	// Column header must appear.
	if !strings.Contains(out, "ACTION") {
		t.Errorf("expected 'ACTION' column header in output:\n%s", out)
	}
	if !strings.Contains(out, "TOTAL_TOKENS") {
		t.Errorf("expected 'TOTAL_TOKENS' column header in output:\n%s", out)
	}

	// Each action seeded should appear.
	for _, action := range []string{"report", "code", "review"} {
		if !strings.Contains(out, action) {
			t.Errorf("expected action %q in cost output:\n%s", action, out)
		}
	}

	// A TOTAL row must appear.
	if !strings.Contains(out, "TOTAL") {
		t.Errorf("expected 'TOTAL' row in cost output:\n%s", out)
	}
}

// TestCostTaskGroupBreakdown verifies that the cost command's task-group section
// groups runs correctly and shows the task group names.
func TestCostTaskGroupBreakdown(t *testing.T) {
	_, projPath, env := setupTestProject(t)

	db, err := calldb.Open(env.ProjectDBPath())
	if err != nil {
		t.Fatalf("Open calldb: %v", err)
	}
	seedCostDB(t, db, env.ProjectID())
	db.Close()

	savedOrg := orgFlag
	defer func() { orgFlag = savedOrg }()
	orgFlag = filepath.Dir(env.OrgDir)

	var costErr error
	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			costErr = runCost(nil, nil)
		})
	})
	if costErr != nil {
		t.Fatalf("runCost: %v", costErr)
	}

	// Task group section header must appear.
	if !strings.Contains(out, "Cost by Task Group") {
		t.Errorf("expected 'Cost by Task Group' section in output:\n%s", out)
	}

	// Both task groups seeded must appear.
	for _, tg := range []string{"report-2026-03-01_09-00-00", "code-2026-03-01_10-00-00"} {
		if !strings.Contains(out, tg) {
			t.Errorf("expected task group %q in cost output:\n%s", tg, out)
		}
	}

	// Task group section column headers must appear.
	if !strings.Contains(out, "TASK_GROUP") {
		t.Errorf("expected 'TASK_GROUP' column header in task group section:\n%s", out)
	}
}

// TestCostEmptyDB verifies that the cost command handles an empty database gracefully.
func TestCostEmptyDB(t *testing.T) {
	_, projPath, env := setupTestProject(t)

	savedOrg := orgFlag
	defer func() { orgFlag = savedOrg }()
	orgFlag = filepath.Dir(env.OrgDir)

	var costErr error
	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			costErr = runCost(nil, nil)
		})
	})
	if costErr != nil {
		t.Fatalf("runCost on empty DB: %v", costErr)
	}
	if !strings.Contains(out, "No data.") {
		t.Errorf("expected 'No data.' for empty DB, got:\n%s", out)
	}
}

// TestCostTotalTokensAggregation verifies that the all-time TOTAL row aggregates
// token counts across all actions correctly.
func TestCostTotalTokensAggregation(t *testing.T) {
	_, projPath, env := setupTestProject(t)

	db, err := calldb.Open(env.ProjectDBPath())
	if err != nil {
		t.Fatalf("Open calldb: %v", err)
	}

	// Seed two runs with known token counts so we can assert the TOTAL.
	// Run A: 1000 input + 200 output = 1200 total tokens
	// Run B: 500 input + 100 output  = 600 total tokens
	// Grand total: 1800 tokens → formatted as "1.8K"
	now := time.Now()
	idA, err := db.InsertCall(&calldb.Call{
		ProjectID: env.ProjectID(), Action: "run", Role: "testing_basic",
		StartedAt: now.Add(-2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("InsertCall A: %v", err)
	}
	if err := db.UpdateCall(idA, &calldb.CallResult{
		EndedAt: now.Add(-1 * time.Minute), DurationMS: 60000,
		CostUSD: 0.01, InputTokens: 1000, OutputTokens: 200,
	}); err != nil {
		t.Fatalf("UpdateCall A: %v", err)
	}

	idB, err := db.InsertCall(&calldb.Call{
		ProjectID: env.ProjectID(), Action: "run", Role: "security",
		StartedAt: now.Add(-3 * time.Minute),
	})
	if err != nil {
		t.Fatalf("InsertCall B: %v", err)
	}
	if err := db.UpdateCall(idB, &calldb.CallResult{
		EndedAt: now.Add(-2 * time.Minute), DurationMS: 60000,
		CostUSD: 0.01, InputTokens: 500, OutputTokens: 100,
	}); err != nil {
		t.Fatalf("UpdateCall B: %v", err)
	}
	db.Close()

	savedOrg := orgFlag
	defer func() { orgFlag = savedOrg }()
	orgFlag = filepath.Dir(env.OrgDir)

	var costErr error
	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			costErr = runCost(nil, nil)
		})
	})
	if costErr != nil {
		t.Fatalf("runCost: %v", costErr)
	}

	// 1800 total tokens formats as "1.8K" via display.FmtTokens.
	if !strings.Contains(out, "1.8K") {
		t.Errorf("expected total token count '1.8K' in output:\n%s", out)
	}
}

// TestCostProjectNameDisplayed verifies that the project name is printed when
// the cost command is run inside a project.
func TestCostProjectNameDisplayed(t *testing.T) {
	_, projPath, _ := setupTestProject(t)
	env, err := root.LookupFrom(projPath)
	if err != nil {
		t.Fatalf("LookupFrom: %v", err)
	}

	savedOrg := orgFlag
	defer func() { orgFlag = savedOrg }()
	orgFlag = filepath.Dir(env.OrgDir)

	var costErr error
	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			costErr = runCost(nil, nil)
		})
	})
	if costErr != nil {
		t.Fatalf("runCost: %v", costErr)
	}

	if !strings.Contains(out, "testproj") {
		t.Errorf("expected project name 'testproj' in cost output:\n%s", out)
	}
}
