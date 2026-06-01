package flow

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ateam/internal/runner"
)

// readBundleLog parses every JSONL line of bundle.jsonl into a slice of
// maps. Fails the test on parse error.
func readBundleLog(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("parse line %q: %v", sc.Text(), err)
		}
		out = append(out, m)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return out
}

// kinds returns the kind field of each event in order.
func kinds(events []map[string]any) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i], _ = e["kind"].(string)
	}
	return out
}

// preparingExec is a fakeExecutor that uses a deterministic LogsDir
// rooted at TempDir so tests can read the resulting bundle.jsonl.
type preparingExec struct {
	logsRoot string
	id       int64
}

func (p *preparingExec) Prepare(opts runner.RunOpts, prompt string) (*runner.PreparedRun, error) {
	p.id++
	logsDir := filepath.Join(p.logsRoot, "logs", fmt.Sprintf("%s-%d", opts.RoleID, p.id))
	return &runner.PreparedRun{
		ExecID:      p.id,
		LogsDir:     logsDir,
		CmdFile:     filepath.Join(logsDir, "cmd.md"),
		Model:       "test-model",
		PromptBytes: len(prompt),
		Opts:        opts,
	}, nil
}

func (p *preparingExec) ExecutePrepared(_ context.Context, prep *runner.PreparedRun, _ string, onProgress func(runner.RunProgress)) runner.RunSummary {
	// Simulate the runner creating its logs dir + cmd.md (so the
	// BundleLogReporter's cmd.md append has a file to append to).
	_ = os.MkdirAll(prep.LogsDir, 0700)
	_ = os.WriteFile(prep.CmdFile, []byte("# Runtime\n"), 0600)
	if onProgress != nil {
		onProgress(runner.RunProgress{ExecID: prep.ExecID, Phase: "init"})
	}
	return runner.RunSummary{
		ExecID:       prep.ExecID,
		RoleID:       prep.Opts.RoleID,
		Duration:     10 * time.Millisecond,
		Cost:         0.0042,
		InputTokens:  100,
		OutputTokens: 200,
		ExitCode:     0,
	}
}

func TestBundleLogReporter_FullSequence(t *testing.T) {
	dir := t.TempDir()
	exec := &preparingExec{logsRoot: dir}
	rep := &BundleLogReporter{}

	bundle := PromptBundle{
		Name:    "exec",
		Render:  func(RuntimeEnv) (string, error) { return "hello prompt", nil },
		RunOpts: func(RuntimeEnv) runner.RunOpts { return runner.RunOpts{RoleID: "tester"} },
	}
	env := RuntimeEnv{Executor: exec, Role: "tester", Action: "exec", WorkDir: "/tmp/wd", Batch: "b1"}
	rc := RunCtx{Ctx: context.Background(), Reporter: rep}

	res := RunBundle(bundle, env, rc)
	if res.Flow.State != StateContinue {
		t.Fatalf("expected StateContinue, got %v (%q)", res.Flow.State, res.Flow.Reason)
	}

	logPath := filepath.Join(res.Summary.RoleID, "") // placeholder
	_ = logPath

	// The preparingExec's LogsDir contains exec_id 1.
	bundlePath := filepath.Join(dir, "logs", "tester-1", "bundle.jsonl")
	events := readBundleLog(t, bundlePath)
	got := kinds(events)
	want := []string{"bundle_start", "agent_exec_start", "agent_exec_end", "bundle_end"}
	if !equalStrings(got, want) {
		t.Errorf("event kinds mismatch\n got: %v\nwant: %v", got, want)
	}

	// Spot-check payloads.
	if events[0]["work_dir"] != "/tmp/wd" {
		t.Errorf("bundle_start work_dir: got %v want /tmp/wd", events[0]["work_dir"])
	}
	if events[0]["batch"] != "b1" {
		t.Errorf("bundle_start batch: got %v want b1", events[0]["batch"])
	}
	if int64(events[1]["exec_id"].(float64)) != 1 {
		t.Errorf("agent_exec_start exec_id: got %v want 1", events[1]["exec_id"])
	}
	if events[1]["prompt_bytes"].(float64) != float64(len("hello prompt")) {
		t.Errorf("agent_exec_start prompt_bytes: got %v want %d", events[1]["prompt_bytes"], len("hello prompt"))
	}
	if events[2]["cost_usd"].(float64) != 0.0042 {
		t.Errorf("agent_exec_end cost_usd: got %v want 0.0042", events[2]["cost_usd"])
	}
	if events[3]["state"] != "continue" {
		t.Errorf("bundle_end state: got %v want continue", events[3]["state"])
	}
}

func TestBundleLogReporter_PreExecAndPostExecActions(t *testing.T) {
	dir := t.TempDir()
	exec := &preparingExec{logsRoot: dir}
	rep := &BundleLogReporter{}

	pre := &recordingAction{name: "pre", state: StateContinue}
	post := &recordingAction{name: "post", state: StateContinue}
	bundle := PromptBundle{
		Name:     "exec",
		Render:   func(RuntimeEnv) (string, error) { return "x", nil },
		RunOpts:  func(RuntimeEnv) runner.RunOpts { return runner.RunOpts{RoleID: "r"} },
		PreExec:  []Action{pre},
		PostExec: []Action{post},
	}
	env := RuntimeEnv{Executor: exec, Role: "r", Action: "exec"}
	rc := RunCtx{Ctx: context.Background(), Reporter: rep}
	_ = RunBundle(bundle, env, rc)

	events := readBundleLog(t, filepath.Join(dir, "logs", "r-1", "bundle.jsonl"))
	got := kinds(events)
	want := []string{
		"bundle_start",
		"pre_exec_start", "pre_exec_end",
		"agent_exec_start", "agent_exec_end",
		"post_exec_start", "post_exec_end",
		"bundle_end",
	}
	if !equalStrings(got, want) {
		t.Errorf("event kinds mismatch\n got: %v\nwant: %v", got, want)
	}

	if events[1]["action_type"] != "recordingAction" {
		t.Errorf("pre_exec_start action_type: got %v want recordingAction", events[1]["action_type"])
	}
	if events[2]["state"] != "continue" {
		t.Errorf("pre_exec_end state: got %v want continue", events[2]["state"])
	}
}

func TestBundleLogReporter_PreExecSkipsDropsBundle(t *testing.T) {
	// When Pre returns Skip, Prepare never runs — there's no exec_id
	// directory to write into. The reporter must not panic and must not
	// produce a stray bundle.jsonl.
	dir := t.TempDir()
	exec := &preparingExec{logsRoot: dir}
	rep := &BundleLogReporter{}

	skipper := &recordingAction{name: "skip", state: StateSkip, reason: "no work"}
	bundle := PromptBundle{
		Name:    "exec",
		Render:  func(RuntimeEnv) (string, error) { return "x", nil },
		RunOpts: func(RuntimeEnv) runner.RunOpts { return runner.RunOpts{RoleID: "r"} },
		PreExec: []Action{skipper},
	}
	env := RuntimeEnv{Executor: exec, Role: "r", Action: "exec"}
	rc := RunCtx{Ctx: context.Background(), Reporter: rep}
	res := RunBundle(bundle, env, rc)
	if res.Flow.State != StateSkip {
		t.Fatalf("expected Skip, got %v", res.Flow.State)
	}

	// No exec_id directory should exist.
	entries, _ := os.ReadDir(filepath.Join(dir, "logs"))
	if len(entries) != 0 {
		t.Errorf("expected no logs/ entries on Pre-skip; got %d", len(entries))
	}
}

func TestBundleLogReporter_AppendsCmdMD(t *testing.T) {
	dir := t.TempDir()
	exec := &preparingExec{logsRoot: dir}
	rep := &BundleLogReporter{}

	bundle := PromptBundle{
		Name:    "exec",
		Render:  func(RuntimeEnv) (string, error) { return "x", nil },
		RunOpts: func(RuntimeEnv) runner.RunOpts { return runner.RunOpts{RoleID: "r", WorkDir: "/wd", Batch: "b9"} },
	}
	env := RuntimeEnv{Executor: exec, Role: "r", Action: "exec", WorkDir: "/wd", Batch: "b9"}
	rc := RunCtx{Ctx: context.Background(), Reporter: rep}
	_ = RunBundle(bundle, env, rc)

	cmd, err := os.ReadFile(filepath.Join(dir, "logs", "r-1", "cmd.md"))
	if err != nil {
		t.Fatalf("read cmd.md: %v", err)
	}
	content := string(cmd)
	if !strings.Contains(content, "## Bundle") {
		t.Errorf("cmd.md missing `## Bundle` section: %q", content)
	}
	for _, want := range []string{"name: exec", "role: r", "action: exec", "work_dir: /wd", "batch: b9", "state: continue"} {
		if !strings.Contains(content, want) {
			t.Errorf("cmd.md missing %q: %s", want, content)
		}
	}
}

func TestBundleLogReporter_PrepareErrorNoFile(t *testing.T) {
	// Prepare-failure path: no AgentExecStart fires; bundle.jsonl must
	// not be created (no exec_id available).
	dir := t.TempDir()
	exec := &errPrepExec{err: errors.New("db down")}
	rep := &BundleLogReporter{}

	bundle := PromptBundle{
		Name:    "exec",
		Render:  func(RuntimeEnv) (string, error) { return "x", nil },
		RunOpts: func(RuntimeEnv) runner.RunOpts { return runner.RunOpts{RoleID: "r"} },
	}
	env := RuntimeEnv{Executor: exec, Role: "r", Action: "exec"}
	rc := RunCtx{Ctx: context.Background(), Reporter: rep}
	res := RunBundle(bundle, env, rc)
	if res.Flow.State != StateError {
		t.Fatalf("expected Error, got %v", res.Flow.State)
	}

	// No directories should have been created at all.
	if _, err := os.Stat(filepath.Join(dir, "logs")); !os.IsNotExist(err) {
		t.Errorf("expected no logs/ dir after Prepare failure; err: %v", err)
	}
}

// recordingAction is a no-op Action that returns a configured Flow state.
// Used by the tests above; named to verify actionTypeName picks up
// "recordingAction" in the action_type payload field.
type recordingAction struct {
	name   string
	state  FlowState
	reason string
}

func (a *recordingAction) Run(RunCtx, RuntimeEnv, *Result) Flow {
	return Flow{State: a.state, Reason: a.reason}
}

// errPrepExec returns a fixed error from Prepare without touching disk.
type errPrepExec struct{ err error }

func (e *errPrepExec) Prepare(runner.RunOpts, string) (*runner.PreparedRun, error) {
	return nil, e.err
}
func (e *errPrepExec) ExecutePrepared(context.Context, *runner.PreparedRun, string, func(runner.RunProgress)) runner.RunSummary {
	panic("ExecutePrepared should not be called when Prepare fails")
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
