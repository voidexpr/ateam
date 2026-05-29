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
	batch       string
	lastRun     bool
	lastReport  bool
	lastReview  bool
	lastCode    bool
	autoDebug   bool
	extraPrompt string
	profile     string
	agent       string
	org         string
}

func saveInspectGlobals() inspectGlobals {
	return inspectGlobals{
		batch:       inspectBatch,
		lastRun:     inspectLastRun,
		lastReport:  inspectLastReport,
		lastReview:  inspectLastReview,
		lastCode:    inspectLastCode,
		autoDebug:   inspectAutoDebug,
		extraPrompt: inspectExtraPrompt,
		profile:     inspectProfile,
		agent:       inspectAgent,
		org:         orgFlag,
	}
}

func (g inspectGlobals) restore() {
	inspectBatch = g.batch
	inspectLastRun = g.lastRun
	inspectLastReport = g.lastReport
	inspectLastReview = g.lastReview
	inspectLastCode = g.lastCode
	inspectAutoDebug = g.autoDebug
	inspectExtraPrompt = g.extraPrompt
	inspectProfile = g.profile
	inspectAgent = g.agent
	orgFlag = g.org
}

// seedInspectDB seeds the given calldb with a set of runs for testing run selection:
//   - one "exec" action run (most recent, no batch)
//   - one "review" action run
//   - two "report" action runs in a report batch
//   - two "exec" action runs in a code batch
//
// projectID is required so seeded rows match what resolveRunSelection's call to
// LatestBatch(env.ProjectID(), ...) filters on.
func seedInspectDB(t *testing.T, db *calldb.CallDB, projectID string) (reportBatch, codeBatch string) {
	t.Helper()
	now := time.Now()

	reportBatch = "report-2026-01-10_10-00-00"
	codeBatch = "code-2026-01-10_11-00-00"

	insert := func(action, role, batch string, offset time.Duration) int64 {
		id, err := db.InsertCall(&calldb.Call{
			ProjectID: projectID, Action: action, Role: role,
			Batch: batch, StartedAt: now.Add(offset),
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

	insert("report", "testing_basic", reportBatch, -10*time.Minute)
	insert("report", "security", reportBatch, -10*time.Minute)
	insert("review", "supervisor", "", -8*time.Minute)
	insert("exec", "testing_basic", codeBatch, -6*time.Minute)
	insert("exec", "security", codeBatch, -6*time.Minute)
	insert("exec", "testing_basic", "", -2*time.Minute)

	return reportBatch, codeBatch
}

// TestInspectRunSelection verifies that each --last-* flag selects the correct
// set of runs from the database.
func TestInspectRunSelection(t *testing.T) {
	_, _, env := setupTestProject(t)

	db, err := calldb.Open(env.ProjectDBPath())
	if err != nil {
		t.Fatalf("Open calldb: %v", err)
	}
	reportBatch, codeBatch := seedInspectDB(t, db, env.ProjectID())
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
		if rows[0].Action != "exec" {
			t.Errorf("expected action 'exec', got %q", rows[0].Action)
		}
		if rows[0].Batch != "" {
			t.Errorf("expected empty batch for most recent run, got %q", rows[0].Batch)
		}
	})

	t.Run("last-report returns all runs in latest report batch", func(t *testing.T) {
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
			if r.Batch != reportBatch {
				t.Errorf("expected batch %q, got %q", reportBatch, r.Batch)
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

	t.Run("last-code returns all runs in latest code batch", func(t *testing.T) {
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
			if r.Batch != codeBatch {
				t.Errorf("expected batch %q, got %q", codeBatch, r.Batch)
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

// TestBuildAutoDebugPromptExtraFromFile verifies that --extra-prompt with a
// @filepath reference loads the file's contents and includes them under an
// "Additional Debug Instructions" heading in the assembled debug prompt.
func TestBuildAutoDebugPromptExtraFromFile(t *testing.T) {
	base, _, env := setupTestProject(t)

	extraFile := filepath.Join(base, "extra_debug.txt")
	const extraContent = "investigate the flaky test in suite alpha"
	if err := os.WriteFile(extraFile, []byte(extraContent), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	rows := []calldb.RecentRow{{
		ID: 42, Action: "exec", Role: "test.gaps",
		StartedAt: time.Now().Add(-1 * time.Minute).Format(time.RFC3339),
	}}

	prompt, err := buildAutoDebugPrompt(env, rows, nil, "@"+extraFile, "", "")
	if err != nil {
		t.Fatalf("buildAutoDebugPrompt: %v", err)
	}
	if !strings.Contains(prompt, extraContent) {
		t.Errorf("expected extra prompt content %q in assembled prompt:\n%s", extraContent, prompt)
	}
	if !strings.Contains(prompt, "Additional Debug Instructions") {
		t.Errorf("expected 'Additional Debug Instructions' section in assembled prompt:\n%s", prompt)
	}
}

// TestBuildAutoDebugPromptPrePostWrap verifies that --pre-prompt lands at the
// very front and --post-prompt at the very end of the auto-debug prompt, with
// --extra-prompt between the assembled body and --post-prompt.
func TestBuildAutoDebugPromptPrePostWrap(t *testing.T) {
	_, _, env := setupTestProject(t)
	rows := []calldb.RecentRow{{
		ID: 7, Action: "exec", Role: "test.gaps",
		StartedAt: time.Now().Add(-1 * time.Minute).Format(time.RFC3339),
	}}

	const pre = "PRE-MARKER"
	const post = "POST-MARKER"
	const extra = "EXTRA-MARKER"

	prompt, err := buildAutoDebugPrompt(env, rows, nil, extra, pre, post)
	if err != nil {
		t.Fatalf("buildAutoDebugPrompt: %v", err)
	}

	preIdx := strings.Index(prompt, pre)
	extraIdx := strings.Index(prompt, extra)
	postIdx := strings.Index(prompt, post)
	if preIdx < 0 || extraIdx < 0 || postIdx < 0 {
		t.Fatalf("missing marker(s) in prompt:\n%s", prompt)
	}
	if preIdx >= extraIdx || extraIdx >= postIdx {
		t.Errorf("expected order pre < extra < post (got %d, %d, %d):\n%s", preIdx, extraIdx, postIdx, prompt)
	}
	if preIdx != 0 {
		t.Errorf("expected --pre-prompt at position 0, got %d", preIdx)
	}
	if !strings.HasSuffix(strings.TrimRight(prompt, "\n"), post) {
		t.Errorf("expected --post-prompt at the very end of the prompt:\n%s", prompt)
	}
}
