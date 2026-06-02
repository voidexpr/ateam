package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
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
	r := &runner.AgentExecutor{
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

// TestRunExecPrePostExtraWrap verifies the wrap order on the raw-prompt path:
// --pre-prompt at the very front, then the body, then --extra-prompt under its
// "Additional Instructions" heading, then --post-prompt as the outermost tail.
func TestRunExecPrePostExtraWrap(t *testing.T) {
	orgParent, projPath, _ := setupTestProject(t)

	saved := saveExecGlobals()
	defer saved.restore()
	orgFlag = orgParent
	execDryRun = true
	execQuiet = true
	execAgent = "mock"
	execProfile = ""
	execPrePrompt = "PRE-MARKER"
	execPostPrompt = "POST-MARKER"
	execExtraPrompt = "EXTRA-MARKER"

	const body = "BODY-MARKER"
	var runErr error
	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			runErr = runExec(nil, []string{body})
		})
	})
	if runErr != nil {
		t.Fatalf("runExec dry-run: %v", runErr)
	}

	preIdx := strings.Index(out, "PRE-MARKER")
	bodyIdx := strings.Index(out, body)
	extraIdx := strings.Index(out, "EXTRA-MARKER")
	postIdx := strings.Index(out, "POST-MARKER")
	if preIdx < 0 || bodyIdx < 0 || extraIdx < 0 || postIdx < 0 {
		t.Fatalf("missing marker(s); pre=%d body=%d extra=%d post=%d\n%s", preIdx, bodyIdx, extraIdx, postIdx, out)
	}
	if preIdx >= bodyIdx || bodyIdx >= extraIdx || extraIdx >= postIdx {
		t.Errorf("expected order pre < body < extra < post; got %d, %d, %d, %d\n%s", preIdx, bodyIdx, extraIdx, postIdx, out)
	}
	if !strings.Contains(out, "# Additional Instructions") {
		t.Errorf("expected 'Additional Instructions' heading in dry-run output:\n%s", out)
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

func TestOpenProgressFD(t *testing.T) {
	cases := []struct {
		name    string
		format  string
		fd      int
		want    io.Writer // expected writer identity, or nil when none
		wantErr string
	}{
		{name: "no-format-no-fd-noop", format: "", fd: 0, want: nil},
		{name: "fd-without-format-rejected", format: "", fd: 3, wantErr: "--progress-fd requires --format"},
		{name: "unknown-format-rejected", format: "csv", fd: 3, wantErr: "unknown --format"},
		{name: "jsonl-default-fd-is-stdout", format: "jsonl", fd: 0, want: os.Stdout},
		{name: "jsonl-fd-1-is-stdout", format: "jsonl", fd: 1, want: os.Stdout},
		{name: "jsonl-fd-2-is-stderr", format: "jsonl", fd: 2, want: os.Stderr},
		{name: "jsonl-negative-fd-rejected", format: "jsonl", fd: -1, wantErr: "non-negative"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, closer, err := openProgressFD(tc.format, tc.fd)
			if closer != nil {
				closer.Close()
			}
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err %v missing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if w != tc.want {
				t.Errorf("openProgressFD returned %v, want %v", w, tc.want)
			}
		})
	}
}

// TestRunExecFormatJSONL_EndToEnd locks in the --format jsonl contract that
// orchestrators rely on: stdout is newline-delimited JSON only (no agent-text
// or summary leakage), both bundle and agent events appear, the documented
// bundle lifecycle kinds are emitted, and the on-disk bundle.jsonl mirrors
// the streamed bundle events. Composing these pieces happens only inside
// runExec — neither JSONReporter, BundleLogReporter, nor openProgressFD
// covers the wiring on their own.
func TestRunExecFormatJSONL_EndToEnd(t *testing.T) {
	orgParent, projPath, env := setupTestProject(t)

	saved := saveExecGlobals()
	defer saved.restore()

	orgFlag = orgParent
	execProfile = "test" // mock agent
	execFormat = "jsonl"

	var (
		runErr    error
		stdoutBuf string
	)
	stderrBuf := captureStderr(t, func() {
		stdoutBuf = captureStdout(t, func() {
			withChdir(t, projPath, func() {
				runErr = runExec(nil, []string{"hello mock"})
			})
		})
	})
	if runErr != nil {
		t.Fatalf("runExec: %v", runErr)
	}

	// Every non-empty stdout line must parse as JSON, and the events must
	// carry both bundle and agent sources.
	stdoutKinds, stdoutSources := parseBundleJSONL(t, stdoutBuf, "stdout")
	if !stdoutSources["bundle"] {
		t.Errorf("expected at least one source:bundle event on stdout")
	}
	if !stdoutSources["agent"] {
		t.Errorf("expected at least one source:agent event on stdout")
	}
	assertBundleLifecycleKinds(t, stdoutKinds, "stdout")

	// Agent response text must not leak as a bare plaintext line — that
	// would be the symptom of fmt.Print(result.Output) firing under
	// --format jsonl. (Inside JSON-encoded assistant `content` fields is
	// expected and fine; only standalone lines are a contract bug.)
	for _, line := range strings.Split(strings.TrimRight(stdoutBuf, "\n"), "\n") {
		if strings.TrimSpace(line) == "mock response" {
			t.Errorf("bare plaintext mock response leaked to stdout: %q", line)
		}
	}

	if strings.Contains(stderrBuf, "--- Summary ---") {
		t.Errorf("--- Summary --- leaked to stderr under --format jsonl:\n%s", stderrBuf)
	}
	// PrintProgressLine emits "[role] ..." stream lines. None should fire
	// under --format jsonl since it implies --no-stream.
	if strings.Contains(stderrBuf, "thinking...") ||
		strings.Contains(stderrBuf, "tool: ") ||
		strings.Contains(stderrBuf, "init: ") {
		t.Errorf("runner streaming output leaked to stderr under --format jsonl:\n%s", stderrBuf)
	}

	bundlePath := filepath.Join(env.ProjectDir, "logs", "1", "bundle.jsonl")
	raw, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatalf("read %s: %v", bundlePath, err)
	}
	diskKinds, _ := parseBundleJSONL(t, string(raw), "bundle.jsonl")
	assertBundleLifecycleKinds(t, diskKinds, "bundle.jsonl on disk")
}

// parseBundleJSONL parses each non-empty newline-delimited JSON event in raw
// and returns the set of "kind" and "source" string fields seen. It fatally
// fails t with label and the offending line if any line is not valid JSON.
func parseBundleJSONL(t *testing.T, raw, label string) (kinds, sources map[string]bool) {
	t.Helper()
	kinds, sources = map[string]bool{}, map[string]bool{}
	for i, line := range strings.Split(strings.TrimRight(raw, "\n"), "\n") {
		if line == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("%s line %d not valid JSON: %q: %v", label, i, line, err)
		}
		if k, ok := event["kind"].(string); ok {
			kinds[k] = true
		}
		if s, ok := event["source"].(string); ok {
			sources[s] = true
		}
	}
	return
}

// assertBundleLifecycleKinds fails t for any documented bundle lifecycle kind
// missing from kinds. label is included in the failure message.
func assertBundleLifecycleKinds(t *testing.T, kinds map[string]bool, label string) {
	t.Helper()
	for _, want := range []string{"bundle_start", "agent_exec_start", "agent_exec_end", "bundle_end"} {
		if !kinds[want] {
			t.Errorf("%s missing bundle kind %q; got %v", label, want, kinds)
		}
	}
}

// TestRunExecPrintsExecIDOnStderr verifies that the runner's
// `exec_id=<id>` correlation line lands on stderr in every output mode an
// orchestrator might pick — default human, --quiet, and --format jsonl —
// and that the printed id matches the inserted CallDB row.
func TestRunExecPrintsExecIDOnStderr(t *testing.T) {
	cases := []struct {
		name      string
		configure func()
	}{
		{name: "default", configure: func() {}},
		{name: "quiet", configure: func() { execQuiet = true }},
		{name: "jsonl", configure: func() { execFormat = "jsonl" }},
	}
	re := regexp.MustCompile(`(?m)^exec_id=(\d+)$`)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			orgParent, projPath, env := setupTestProject(t)

			saved := saveExecGlobals()
			defer saved.restore()

			orgFlag = orgParent
			execProfile = "test"
			tc.configure()

			var runErr error
			stderrBuf := captureStderr(t, func() {
				captureStdout(t, func() {
					withChdir(t, projPath, func() {
						runErr = runExec(nil, []string{"hello mock"})
					})
				})
			})
			if runErr != nil {
				t.Fatalf("runExec: %v", runErr)
			}

			m := re.FindStringSubmatch(stderrBuf)
			if m == nil {
				t.Fatalf("stderr missing exec_id=<digits> line:\n%s", stderrBuf)
			}
			printedID := m[1]

			db, err := calldb.Open(env.ProjectDBPath())
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer db.Close()
			rows, err := db.RecentRuns(calldb.RecentFilter{Limit: 10})
			if err != nil {
				t.Fatalf("RecentRuns: %v", err)
			}
			if len(rows) != 1 {
				t.Fatalf("expected 1 DB row, got %d", len(rows))
			}
			wantID := strconv.FormatInt(rows[0].ID, 10)
			if printedID != wantID {
				t.Errorf("exec_id mismatch: stderr printed %q, DB row ID %q", printedID, wantID)
			}
		})
	}
}

func TestOpenProgressFD_RealPipe(t *testing.T) {
	// Pipe one end into openProgressFD via its raw fd and verify a
	// subsequent write reaches the other end. openProgressFD wraps the
	// passed fd in an os.File with a GC finalizer that closes it; we
	// dup pw's fd so the wrapper owns an independent fd. Otherwise the
	// finalizer would later close a recycled fd belonging to a
	// completely unrelated test (e.g. a stdout capture pipe).
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer pr.Close()
	defer pw.Close()

	dupFd, err := syscall.Dup(int(pw.Fd()))
	if err != nil {
		t.Fatalf("dup: %v", err)
	}
	w, closer, err := openProgressFD("jsonl", dupFd)
	if err != nil {
		syscall.Close(dupFd)
		t.Fatalf("openProgressFD: %v", err)
	}
	defer closer.Close()

	if _, err := w.Write([]byte("hi\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	buf := make([]byte, 8)
	done := make(chan struct{})
	var n int
	go func() {
		n, _ = pr.Read(buf)
		close(done)
	}()
	// Close both write ends so pr sees EOF after the buffered "hi".
	closer.Close()
	pw.Close()
	<-done
	if string(buf[:n]) != "hi\n" {
		t.Errorf("read: got %q want hi\\n", buf[:n])
	}
}
