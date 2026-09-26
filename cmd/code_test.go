package cmd

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/flow"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
)

// TestPrintCodeSessionSummaryPicksByExecID verifies that printCodeSessionSummary
// selects the directory matching result.ExecID, not the lexicographically last
// entry under shared/code/. Without this, once EXEC_ID >= 10 the summary would
// consistently show stale content (e.g. "9" sorts after "11").
func TestPrintCodeSessionSummaryPicksByExecID(t *testing.T) {
	sharedDir := t.TempDir()
	supervisorDir := t.TempDir() // unused for this scenario but the signature wants it
	codeDir := filepath.Join(sharedDir, "code")
	if err := os.MkdirAll(codeDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create directories 1..11 with distinctive execution_report.md content.
	for i := 1; i <= 11; i++ {
		d := filepath.Join(codeDir, strconv.Itoa(i))
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
		report := []byte("REPORT-" + strconv.Itoa(i) + "\n")
		if err := os.WriteFile(filepath.Join(d, "execution_report.md"), report, 0644); err != nil {
			t.Fatal(err)
		}
	}

	out := captureStdout(t, func() {
		printCodeSessionSummary(sharedDir, supervisorDir, 11, false, "")
	})

	if !strings.Contains(out, "REPORT-11") {
		t.Errorf("expected REPORT-11 in output, got:\n%s", out)
	}
	// Guard against the old lexicographic behavior: "9" sorts after "11", so
	// the buggy implementation would surface REPORT-9 instead of REPORT-11.
	if strings.Contains(out, "REPORT-9\n") {
		t.Errorf("output should not contain REPORT-9 when execID=11:\n%s", out)
	}
	wantSession := filepath.Join("code", "11")
	if !strings.Contains(out, wantSession) {
		t.Errorf("expected session path containing %q in output:\n%s", wantSession, out)
	}
}

// TestCodeDryRunAgentInjection locks in that --agent on `ateam code` lands
// in the supervisor prompt via {{exec.subrun_args}}. Dry-run pre-resolves
// the placeholder so the operator sees the sub-run flags in the printed
// prompt body.
func TestCodeDryRunAgentInjection(t *testing.T) {
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

	var runErr error
	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			runErr = runCode(CodeOptions{
				CommonExecFlags: CommonExecFlags{Agent: "mock"},
				DryRun:          true,
				Review:          "# Test Review\n\nsome tasks",
			})
		})
	})

	if runErr != nil {
		t.Fatalf("runCode dry-run with agent override: %v", runErr)
	}
	// Spec-aligned dry-run renders in ModePreview — exec.* keys emit
	// the AT RUNTIME sentinel pattern instead of pre-substituting
	// (legacy behavior). Operators wanting to see the resolved sub-run
	// args run the live `ateam code` and inspect the rendered prompt
	// via the bundle log, OR check `ateam exec`'s --agent flag is
	// propagated via the runner.RunOpts wire (unit-tested separately).
	if !strings.Contains(out, "{{AT RUNTIME:exec.subrun_args}}") {
		t.Errorf("expected exec.subrun_args preview sentinel, got:\n%s", out)
	}
}

func TestCodeDryRunSupervisorAgentOverride(t *testing.T) {
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
	orgFlag = filepath.Dir(orgDir) // --org takes the parent of .ateamorg/

	// With both supervisor-agent and agent overrides, dry-run should succeed
	// and return before invoking the supervisor runner.
	var runErr error
	captureStdout(t, func() {
		withChdir(t, projPath, func() {
			runErr = runCode(CodeOptions{
				CommonExecFlags: CommonExecFlags{Agent: "mock"},
				DryRun:          true,
				Review:          "# Test Review\n\nsome tasks",
				SupervisorAgent: "mock",
			})
		})
	})

	if runErr != nil {
		t.Fatalf("runCode dry-run with supervisor-agent override: %v", runErr)
	}
}

// TestCodeStageHappyPath exercises the migrated runCode end-to-end
// against the mock agent: starting line, Stage Post chain emits Done +
// session summary (via printCodeSessionAction).
func TestCodeStageHappyPath(t *testing.T) {
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

	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			if err := runCode(CodeOptions{
				CommonExecFlags:   CommonExecFlags{Profile: "test"},
				Review:            "# Test Review\n\nsome tasks",
				SupervisorProfile: "test", // mock agent
			}); err != nil {
				t.Fatalf("runCode: %v", err)
			}
		})
	})

	for _, want := range []string{
		"Code management supervisor running",
		"Done (",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestSupervisorAgentEnv(t *testing.T) {
	cases := []struct {
		subRun, supervisor int
		want               string // "" = nil env
	}{
		{60, 120, "4200000"},
		{0, 120, "7800000"},
		{0, 0, ""},
	}
	for _, c := range cases {
		got := supervisorAgentEnv(c.subRun, c.supervisor)
		if c.want == "" {
			if got != nil {
				t.Errorf("(%d,%d): want nil env, got %v", c.subRun, c.supervisor, got)
			}
			continue
		}
		for _, k := range []string{"BASH_DEFAULT_TIMEOUT_MS", "BASH_MAX_TIMEOUT_MS"} {
			if got[k] != c.want {
				t.Errorf("(%d,%d): %s = %q, want %q", c.subRun, c.supervisor, k, got[k], c.want)
			}
		}
	}
}

func TestCheckBatchOutcomeAction(t *testing.T) {
	const batch = "code-test"
	base := t.TempDir()
	db, err := calldb.Open(filepath.Join(base, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now()
	insert := func(role string) int64 {
		id, err := db.InsertCall(&calldb.Call{Action: "code", Role: role, Batch: batch, StartedAt: now})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	end := func(id int64, isError bool, msg string) {
		if err := db.UpdateCall(id, &calldb.CallResult{EndedAt: now, IsError: isError, ErrorMessage: msg}); err != nil {
			t.Fatal(err)
		}
	}
	supervisor := insert("supervisor")
	end(supervisor, false, "")
	okChild := insert("")
	end(okChild, false, "")

	sharedDir := filepath.Join(base, "shared")
	sessionDir := filepath.Join(sharedDir, "code", strconv.FormatInt(supervisor, 10))
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeReport := func(body string) {
		if err := os.WriteFile(filepath.Join(sessionDir, "execution_report.md"), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	run := func() flow.Flow {
		action := checkBatchOutcomeAction{Batch: batch, SharedDir: sharedDir}
		res := &flow.Result{Summary: &runner.RunSummary{ExecID: supervisor}}
		return action.Run(flow.RunCtx{DB: db}, flow.RuntimeEnv{}, res)
	}

	writeReport("## Summary\n- **Total**: 1\n- **Completed**: 1\n- **Failed**: 0\n- **Incomplete after retry**: 0\n- **Not attempted**: 0\n")
	if f := run(); f.State != flow.StateContinue {
		t.Fatalf("clean batch: state=%v err=%v", f.State, f.Err)
	}

	writeReport("## Summary\n- **Total**: 3\n- **Completed**: 1\n- **Failed**: 1\n- **Incomplete after retry**: 0\n- **Not attempted**: 1\n")
	f := run()
	if f.State != flow.StateError || f.Err == nil {
		t.Fatalf("report failures: state=%v err=%v", f.State, f.Err)
	}
	for _, want := range []string{"Failed=1", "Not attempted=1"} {
		if !strings.Contains(f.Err.Error(), want) {
			t.Errorf("missing %q in %v", want, f.Err)
		}
	}
	if strings.Contains(f.Err.Error(), "Incomplete") {
		t.Errorf("zero counter reported: %v", f.Err)
	}

	writeReport("mock response")
	killed := insert("")
	end(killed, true, "[parent_terminated] run received SIGTERM")
	running := insert("")
	f = run()
	if f.State != flow.StateError || f.Err == nil {
		t.Fatalf("db failures: state=%v err=%v", f.State, f.Err)
	}
	for _, want := range []string{
		"exec " + strconv.FormatInt(killed, 10) + " failed: [parent_terminated]",
		"exec " + strconv.FormatInt(running, 10) + " still running",
	} {
		if !strings.Contains(f.Err.Error(), want) {
			t.Errorf("missing %q in %v", want, f.Err)
		}
	}
}
