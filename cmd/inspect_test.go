package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ateam/internal/calldb"
)

// inspectGlobals captures the package-level flags used by the inspect command.
type inspectGlobals struct {
	taskGroup            string
	lastRun              bool
	lastReport           bool
	lastReview           bool
	lastCode             bool
	autoDebug            bool
	autoDebugPrompt      bool
	autoDebugExtraPrompt string
	profile              string
	agent                string
	org                  string
}

func saveInspectGlobals() inspectGlobals {
	return inspectGlobals{
		taskGroup:            inspectTaskGroup,
		lastRun:              inspectLastRun,
		lastReport:           inspectLastReport,
		lastReview:           inspectLastReview,
		lastCode:             inspectLastCode,
		autoDebug:            inspectAutoDebug,
		autoDebugPrompt:      inspectAutoDebugPrompt,
		autoDebugExtraPrompt: inspectAutoDebugExtraPrompt,
		profile:              inspectProfile,
		agent:                inspectAgent,
		org:                  orgFlag,
	}
}

func (g inspectGlobals) restore() {
	inspectTaskGroup = g.taskGroup
	inspectLastRun = g.lastRun
	inspectLastReport = g.lastReport
	inspectLastReview = g.lastReview
	inspectLastCode = g.lastCode
	inspectAutoDebug = g.autoDebug
	inspectAutoDebugPrompt = g.autoDebugPrompt
	inspectAutoDebugExtraPrompt = g.autoDebugExtraPrompt
	inspectProfile = g.profile
	inspectAgent = g.agent
	orgFlag = g.org
}

// seedInspectDB seeds the given calldb with a set of runs for testing run selection:
//   - one "run" action run (most recent, no task group)
//   - one "review" action run
//   - two "report" action runs in a report task group
//   - two "run" action runs in a code task group
//
// Note: project_id is left empty so that resolveRunSelection's call to
// LatestTaskGroup with an empty string prefix finds the rows (queries WHERE project_id = empty).
func seedInspectDB(t *testing.T, db *calldb.CallDB) (reportTG, codeTG string) {
	t.Helper()
	now := time.Now()

	reportTG = "report-2026-01-10_10-00-00"
	codeTG = "code-2026-01-10_11-00-00"

	insert := func(action, role, tg string, offset time.Duration) int64 {
		id, err := db.InsertCall(&calldb.Call{
			ProjectID: "", Action: action, Role: role,
			TaskGroup: tg, StartedAt: now.Add(offset),
		})
		if err != nil {
			t.Fatalf("InsertCall(%s/%s): %v", action, role, err)
		}
		if err := db.UpdateCall(id, &calldb.CallResult{
			EndedAt: now.Add(offset + time.Minute), DurationMS: 60000,
		}); err != nil {
			t.Fatalf("UpdateCall: %v", err)
		}
		return id
	}

	insert("report", "testing_basic", reportTG, -10*time.Minute)
	insert("report", "security", reportTG, -10*time.Minute)
	insert("review", "supervisor", "", -8*time.Minute)
	insert("run", "testing_basic", codeTG, -6*time.Minute)
	insert("run", "security", codeTG, -6*time.Minute)
	// Most recent run — no task group
	insert("run", "testing_basic", "", -2*time.Minute)

	return reportTG, codeTG
}

// TestInspectRunSelection verifies that each --last-* flag selects the correct
// set of runs from the database.
func TestInspectRunSelection(t *testing.T) {
	_, _, env := setupTestProject(t)

	db, err := calldb.Open(env.ProjectDBPath())
	if err != nil {
		t.Fatalf("Open calldb: %v", err)
	}
	reportTG, codeTG := seedInspectDB(t, db)
	db.Close()

	// Re-open a fresh handle as resolveRunSelection would.
	openDB := func(t *testing.T) *calldb.CallDB {
		t.Helper()
		d, err := calldb.Open(env.ProjectDBPath())
		if err != nil {
			t.Fatalf("Open calldb: %v", err)
		}
		return d
	}

	t.Run("last-run returns single most recent run", func(t *testing.T) {
		saved := saveInspectGlobals()
		defer saved.restore()
		inspectLastRun = true

		d := openDB(t)
		defer d.Close()
		rows, err := resolveRunSelection(d, env, nil)
		if err != nil {
			t.Fatalf("resolveRunSelection: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("expected 1 row, got %d", len(rows))
		}
		// The most recent run has no task group and action "run"
		if rows[0].Action != "run" {
			t.Errorf("expected action 'run', got %q", rows[0].Action)
		}
		if rows[0].TaskGroup != "" {
			t.Errorf("expected empty task group for most recent run, got %q", rows[0].TaskGroup)
		}
	})

	t.Run("last-report returns all runs in latest report task group", func(t *testing.T) {
		saved := saveInspectGlobals()
		defer saved.restore()
		inspectLastReport = true

		d := openDB(t)
		defer d.Close()
		rows, err := resolveRunSelection(d, env, nil)
		if err != nil {
			t.Fatalf("resolveRunSelection: %v", err)
		}
		if len(rows) == 0 {
			t.Fatal("expected rows, got none")
		}
		for _, r := range rows {
			if r.TaskGroup != reportTG {
				t.Errorf("expected task group %q, got %q", reportTG, r.TaskGroup)
			}
			if r.Action != "report" {
				t.Errorf("expected action 'report', got %q", r.Action)
			}
		}
	})

	t.Run("last-review returns single most recent review run", func(t *testing.T) {
		saved := saveInspectGlobals()
		defer saved.restore()
		inspectLastReview = true

		d := openDB(t)
		defer d.Close()
		rows, err := resolveRunSelection(d, env, nil)
		if err != nil {
			t.Fatalf("resolveRunSelection: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("expected 1 row, got %d", len(rows))
		}
		if rows[0].Action != "review" {
			t.Errorf("expected action 'review', got %q", rows[0].Action)
		}
	})

	t.Run("last-code returns all runs in latest code task group", func(t *testing.T) {
		saved := saveInspectGlobals()
		defer saved.restore()
		inspectLastCode = true

		d := openDB(t)
		defer d.Close()
		rows, err := resolveRunSelection(d, env, nil)
		if err != nil {
			t.Fatalf("resolveRunSelection: %v", err)
		}
		if len(rows) == 0 {
			t.Fatal("expected rows, got none")
		}
		for _, r := range rows {
			if r.TaskGroup != codeTG {
				t.Errorf("expected task group %q, got %q", codeTG, r.TaskGroup)
			}
		}
	})

	t.Run("no flags returns error", func(t *testing.T) {
		saved := saveInspectGlobals()
		defer saved.restore()

		d := openDB(t)
		defer d.Close()
		_, err := resolveRunSelection(d, env, nil)
		if err == nil {
			t.Fatal("expected error when no selection flags set")
		}
	})
}

// TestInspectAutoDebugExtraPromptFromFile verifies that --auto-debug-extra-prompt
// with a @filepath reference loads the file's contents and includes them in the
// printed debug prompt.
func TestInspectAutoDebugExtraPromptFromFile(t *testing.T) {
	base, projPath, env := setupTestProject(t)

	// Seed a run so the inspect command can find something to inspect.
	db, err := calldb.Open(env.ProjectDBPath())
	if err != nil {
		t.Fatalf("Open calldb: %v", err)
	}
	now := time.Now()
	id, err := db.InsertCall(&calldb.Call{
		ProjectID: "", Action: "run", Role: "testing_basic",
		StartedAt: now.Add(-1 * time.Minute),
	})
	if err != nil {
		t.Fatalf("InsertCall: %v", err)
	}
	if err := db.UpdateCall(id, &calldb.CallResult{
		EndedAt: now, DurationMS: 60000,
	}); err != nil {
		t.Fatalf("UpdateCall: %v", err)
	}
	db.Close()

	// Write an extra prompt file with known content.
	extraFile := filepath.Join(base, "extra_debug.txt")
	const extraContent = "investigate the flaky test in suite alpha"
	if err := os.WriteFile(extraFile, []byte(extraContent), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	saved := saveInspectGlobals()
	defer saved.restore()
	orgFlag = filepath.Dir(env.OrgDir)
	inspectLastRun = true
	inspectAutoDebugPrompt = true // print prompt, don't exec agent
	inspectAutoDebugExtraPrompt = "@" + extraFile

	var runErr error
	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			runErr = runPsFiles(nil, nil)
		})
	})

	if runErr != nil {
		t.Fatalf("runPsFiles: %v", runErr)
	}
	if !strings.Contains(out, extraContent) {
		t.Errorf("expected extra prompt content %q in output:\n%s", extraContent, out)
	}
	if !strings.Contains(out, "Additional Debug Instructions") {
		t.Errorf("expected 'Additional Debug Instructions' section in output:\n%s", out)
	}
}
