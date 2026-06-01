package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/root"
)

func saveTailGlobals() func() {
	reports, coding, last, verbose, nc, fm := tailReports, tailCoding, tailLast, tailVerbose, tailNoColor, tailFinalMessage
	return func() {
		tailReports = reports
		tailCoding = coding
		tailLast = last
		tailVerbose = verbose
		tailNoColor = nc
		tailFinalMessage = fm
	}
}

// TestTailNoRunningExits exercises the graceful empty-state path: with no
// rows in the project DB, `ateam tail --coding` should return a "no coding
// session" error rather than hanging on the 30-second discovery wait.
func TestTailNoRunningExits(t *testing.T) {
	defer saveTailGlobals()()

	base := t.TempDir()
	orgDir, err := root.InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}
	projPath := filepath.Join(base, "myproj")
	if err := os.MkdirAll(projPath, 0755); err != nil {
		t.Fatal(err)
	}
	initTestGitRepo(t, projPath)
	if _, err := root.InitProject(projPath, orgDir, root.InitProjectOpts{
		Name:         "myproj",
		EnabledRoles: []string{"testing_basic"},
	}); err != nil {
		t.Fatalf("InitProject: %v", err)
	}

	savedOrg := orgFlag
	defer func() { orgFlag = savedOrg }()
	orgFlag = filepath.Dir(orgDir)

	tailCoding = true
	tailReports = false
	tailLast = false

	var runErr error
	withChdir(t, projPath, func() {
		runErr = runTail(nil, nil)
	})

	if runErr == nil {
		t.Fatal("expected error for empty coding session, got nil")
	}
	if !strings.Contains(runErr.Error(), "no coding session") {
		t.Errorf("expected 'no coding session' message, got: %v", runErr)
	}
}

// TestTailFinalMessageCLI exercises the full `ateam tail ID --final-message`
// path: a project DB is seeded with two completed runs (one success, one
// error), the runTail entry point is called, and stdout must contain one
// JSONL line per run with the expected final assistant text and metadata.
// The function also asserts that AnyError surfaces as a non-zero error
// return when at least one run ended with is_error=true.
func TestTailFinalMessageCLI(t *testing.T) {
	defer saveTailGlobals()()

	_, projPath, env := setupTestProject(t)

	logsDir := filepath.Join(env.ProjectDir, "logs", "roles", "testing_basic")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	writeStream := func(name, finalText string, isErr bool) string {
		rel := filepath.Join("logs", "roles", "testing_basic", name)
		abs := filepath.Join(env.ProjectDir, rel)
		errFlag := "false"
		if isErr {
			errFlag = "true"
		}
		body := strings.Join([]string{
			`{"type":"system","subtype":"init","session_id":"sid","model":"opus"}`,
			`{"type":"user"}`,
			`{"type":"assistant","message":{"content":[{"type":"text","text":` + strconv.Quote(finalText) + `}]}}`,
			`{"type":"result","total_cost_usd":0.10,"is_error":` + errFlag + `,"duration_ms":4000,"num_turns":1,"usage":{"input_tokens":80,"output_tokens":40,"cache_read_input_tokens":0}}`,
		}, "\n") + "\n"
		if err := os.WriteFile(abs, []byte(body), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		return rel
	}

	relA := writeStream("a.jsonl", "PASSED: green", false)
	relB := writeStream("b.jsonl", "FAILED: timeout", true)

	db, err := calldb.Open(env.ProjectDBPath())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	insert := func(role string, rel string, isErr bool) int64 {
		now := time.Now()
		id, err := db.InsertCall(&calldb.Call{
			ProjectID: env.ProjectID(),
			Agent:     "claude",
			Action:    "exec",
			Role:      role,
			Model:     "opus",
			AgentFile: rel,
			StartedAt: now.Add(-time.Minute),
		})
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
		errMsg := ""
		if isErr {
			errMsg = "synthetic"
		}
		if err := db.UpdateCall(id, &calldb.CallResult{
			EndedAt: now, DurationMS: 4000, IsError: isErr, ErrorMessage: errMsg,
			CostUSD: 0.10, InputTokens: 80, OutputTokens: 40, Turns: 1,
		}); err != nil {
			t.Fatalf("update: %v", err)
		}
		return id
	}

	idA := insert("testing_basic", relA, false)
	idB := insert("testing_basic", relB, true)

	savedOrg := orgFlag
	defer func() { orgFlag = savedOrg }()
	orgFlag = filepath.Dir(env.OrgDir)

	tailReports = false
	tailCoding = false
	tailLast = false
	tailFinalMessage = true
	tailNoColor = true

	var runErr error
	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			runErr = runTail(nil, []string{strconv.FormatInt(idA, 10), strconv.FormatInt(idB, 10)})
		})
	})

	if runErr == nil {
		t.Fatal("expected non-nil error because one run ended with is_error=true")
	}
	if !strings.Contains(runErr.Error(), "ended in error") {
		t.Errorf("expected 'ended in error' in: %v", runErr)
	}

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 JSONL lines, got %d:\n%s", len(lines), out)
	}

	got := map[int64]map[string]any{}
	for _, line := range lines {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("invalid JSON line %q: %v", line, err)
		}
		idF, _ := rec["exec_id"].(float64)
		got[int64(idF)] = rec
	}

	if rec := got[idA]; rec == nil {
		t.Fatalf("missing line for idA=%d", idA)
	} else {
		if rec["final_message"] != "PASSED: green" {
			t.Errorf("idA final_message: got %v", rec["final_message"])
		}
		if rec["is_error"] != false {
			t.Errorf("idA is_error: got %v", rec["is_error"])
		}
	}
	if rec := got[idB]; rec == nil {
		t.Fatalf("missing line for idB=%d", idB)
	} else {
		if rec["final_message"] != "FAILED: timeout" {
			t.Errorf("idB final_message: got %v", rec["final_message"])
		}
		if rec["is_error"] != true {
			t.Errorf("idB is_error: got %v", rec["is_error"])
		}
		if rec["error_message"] != "synthetic" {
			t.Errorf("idB error_message: got %v", rec["error_message"])
		}
	}
}
