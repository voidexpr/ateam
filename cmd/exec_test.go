package cmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
)

func captureStdout(t *testing.T, fn func()) (out string) {
	t.Helper()
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	old := os.Stdout
	defer func() { os.Stdout = old }()
	os.Stdout = pw

	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		io.Copy(&buf, pr)
		close(done)
	}()

	defer func() {
		pw.Close()
		<-done
		out = buf.String()
	}()

	fn()
	return
}

func TestPrintExecDryRun(t *testing.T) {
	r := &runner.Runner{
		Agent:   &agent.MockAgent{},
		Profile: "test",
	}
	env := &root.ResolvedEnv{}

	out := captureStdout(t, func() {
		if err := printExecDryRun(r, env, "hello world", "security", runner.ActionExec, ""); err != nil {
			t.Errorf("printExecDryRun: %v", err)
		}
	})

	for _, want := range []string{"mock", "dry-run", "Profile:", "hello world"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in dry-run output:\n%s", want, out)
		}
	}
}

func TestRunExecDryRunNoExec(t *testing.T) {
	base := t.TempDir()
	orgDir, err := root.InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}
	projPath := filepath.Join(base, "myproj")
	if err := os.MkdirAll(projPath, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := root.InitProject(projPath, orgDir, root.InitProjectOpts{
		Name:         "myproj",
		EnabledRoles: []string{"testing_basic"},
	}); err != nil {
		t.Fatalf("InitProject: %v", err)
	}

	saved := saveExecGlobals()
	defer saved.restore()
	orgFlag = filepath.Dir(orgDir) // --org takes the parent of .ateamorg/
	execDryRun = true
	execQuiet = true
	execAgent = "mock"
	execProfile = ""
	execRole = ""

	var runErr error
	captureStdout(t, func() {
		withChdir(t, projPath, func() {
			runErr = runExec(nil, []string{"test prompt"})
		})
	})

	if runErr != nil {
		t.Fatalf("runExec dry-run: %v", runErr)
	}
}

func TestRunExecDryRunCodexTmuxCheapSettings(t *testing.T) {
	orgParent, projPath, _ := setupTestProject(t)

	saved := saveExecGlobals()
	defer saved.restore()
	orgFlag = orgParent
	execDryRun = true
	execQuiet = true
	execProfile = ""
	execAgent = "codex-tmux"
	execModel = "gpt-5.5"
	execEffort = "low"

	var runErr error
	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			runErr = runExec(nil, []string{"ping"})
		})
	})

	if runErr != nil {
		t.Fatalf("runExec dry-run: %v", runErr)
	}
	for _, want := range []string{
		"Agent:     codex-tmux",
		"--model gpt-5.5",
		"model_reasoning_effort=low",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in dry-run output:\n%s", want, out)
		}
	}
	if strings.Contains(out, "model_reasoning_effort=xhigh") {
		t.Errorf("dry-run retained default expensive reasoning effort:\n%s", out)
	}
}

func TestFormatInitLine(t *testing.T) {
	cases := []struct {
		name string
		in   runner.RunProgress
		want string
	}{
		{
			name: "init with model and session",
			in:   runner.RunProgress{Phase: runner.PhaseInit, Subtype: "init", Model: "claude-opus-4-7", SessionID: "abc123"},
			want: "init: model=claude-opus-4-7 session=abc123",
		},
		{
			name: "init with no payload falls back to placeholder",
			in:   runner.RunProgress{Phase: runner.PhaseInit, Subtype: "init"},
			want: "initializing...",
		},
		{
			name: "compact boundary renders distinctly",
			in:   runner.RunProgress{Phase: runner.PhaseInit, Subtype: "compact_boundary"},
			want: "context compacted",
		},
		{
			name: "unknown subtype is shown verbatim",
			in:   runner.RunProgress{Phase: runner.PhaseInit, Subtype: "rate_limited"},
			want: "init: rate_limited",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := formatInitLine(c.in); got != c.want {
				t.Errorf("formatInitLine = %q, want %q", got, c.want)
			}
		})
	}
}

// TestRunExecAcceptsUnknownRoleAndCreatesDir locks in the behavior introduced
// by fd99869 ("exec: accept any --role name without validation"): runExec must
// accept arbitrary role names without checking them against a known list, and
// must create the role's logs directory as a side effect.
func TestRunExecAcceptsUnknownRoleAndCreatesDir(t *testing.T) {
	cases := []struct {
		name string
		role string
	}{
		{name: "unknown simple role", role: "made_up_role_for_test"},
		{name: "collection-style role", role: "made.up_role"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			orgParent, projPath, env := setupTestProject(t)

			saved := saveExecGlobals()
			defer saved.restore()
			orgFlag = orgParent
			execDryRun = true
			execQuiet = true
			execAgent = "mock"
			execProfile = ""
			execRole = tc.role

			prompt := "exercise role " + tc.role
			var runErr error
			out := captureStdout(t, func() {
				withChdir(t, projPath, func() {
					runErr = runExec(nil, []string{prompt})
				})
			})
			if runErr != nil {
				t.Fatalf("runExec with role %q: %v", tc.role, runErr)
			}

			roleDir := filepath.Join(env.ProjectDir, "logs", "roles", tc.role)
			if _, err := os.Stat(roleDir); err != nil {
				t.Errorf("role logs dir %s not created: %v", roleDir, err)
			}

			if !strings.Contains(out, tc.role) {
				t.Errorf("expected role %q to appear in dry-run output:\n%s", tc.role, out)
			}
		})
	}
}

// TestRunExecScratchModeWritesToOrgDB verifies that `ateam exec` from a cwd
// with no .ateam/ but a discoverable .ateamorg/ records a row in
// <OrgDir>/state.sqlite, writes logs under <OrgDir>/logs/<id>/, and captures
// the absolute cwd in agent_execs.work_dir.
func TestRunExecScratchModeWritesToOrgDB(t *testing.T) {
	base := t.TempDir()
	orgDir, err := root.InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}

	// Scratch cwd: any dir under base that is NOT a project.
	scratchDir := filepath.Join(base, "scratch_workspace")
	if err := os.MkdirAll(scratchDir, 0755); err != nil {
		t.Fatal(err)
	}

	saved := saveExecGlobals()
	defer saved.restore()
	orgFlag = base
	execProfile = "test" // mock agent
	execAction = "audit"
	execQuiet = true
	execNoStream = true

	var runErr error
	captureStdout(t, func() {
		withChdir(t, scratchDir, func() {
			runErr = runExec(nil, []string{"scratch hello"})
		})
	})
	if runErr != nil {
		t.Fatalf("runExec: %v", runErr)
	}

	// state.sqlite must land in the org dir, not a project dir.
	orgDBPath := filepath.Join(orgDir, "state.sqlite")
	if _, err := os.Stat(orgDBPath); err != nil {
		t.Fatalf("org state.sqlite missing: %v", err)
	}

	db, err := calldb.Open(orgDBPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	rows, err := db.RecentRuns(calldb.RecentFilter{Action: "audit", Limit: 10})
	if err != nil {
		t.Fatalf("RecentRuns: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].WorkDir == "" {
		t.Errorf("expected non-empty work_dir on scratch row")
	}
	// resolved cwd may differ from scratchDir via symlinks on macOS — compare
	// suffix to stay tolerant of /private prefixing.
	if !strings.HasSuffix(rows[0].WorkDir, "scratch_workspace") {
		t.Errorf("expected work_dir to end in scratch_workspace, got %q", rows[0].WorkDir)
	}

	// Logs should land under the org dir.
	logsDir := filepath.Join(orgDir, "logs", "1")
	if _, err := os.Stat(filepath.Join(logsDir, "cmd.md")); err != nil {
		t.Errorf("expected cmd.md at %s: %v", logsDir, err)
	}

	// --work-dir filter should match this row.
	psWorkDir = rows[0].WorkDir
	defer func() { psWorkDir = "" }()
	filtered, err := db.RecentRuns(calldb.RecentFilter{WorkDir: rows[0].WorkDir, Limit: 10})
	if err != nil {
		t.Fatalf("RecentRuns filter: %v", err)
	}
	if len(filtered) != 1 {
		t.Errorf("expected 1 row from --work-dir filter, got %d", len(filtered))
	}
}

// TestRunExecRecordsCustomAction verifies the --action flag is plumbed all the
// way through to the CallDB record so `ateam ps --action <name>` can filter on it.
func TestRunExecRecordsCustomAction(t *testing.T) {
	orgParent, projPath, env := setupTestProject(t)

	saved := saveExecGlobals()
	defer saved.restore()
	orgFlag = orgParent
	execProfile = "test" // uses mock agent
	execAction = "audit" // custom action label
	execQuiet = true
	execNoStream = true

	var runErr error
	captureStdout(t, func() {
		withChdir(t, projPath, func() {
			runErr = runExec(nil, []string{"hello mock"})
		})
	})
	if runErr != nil {
		t.Fatalf("runExec: %v", runErr)
	}

	db, err := calldb.Open(env.ProjectDBPath())
	if err != nil {
		t.Fatalf("Open CallDB: %v", err)
	}
	defer db.Close()

	rows, err := db.RecentRuns(calldb.RecentFilter{Action: "audit", Limit: 10})
	if err != nil {
		t.Fatalf("RecentRuns: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row with action=audit, got %d", len(rows))
	}
	if rows[0].Action != "audit" {
		t.Errorf("expected row.Action=audit, got %q", rows[0].Action)
	}
}
