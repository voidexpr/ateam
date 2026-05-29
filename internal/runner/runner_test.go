package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/calldb"
)

func TestRunnerWithMockAgent(t *testing.T) {
	dir := t.TempDir()

	mock := &agent.MockAgent{Response: "test output from mock", Cost: 0.01}
	r := newTestRunner(t, dir, mock)

	summary := r.Run(context.Background(), "test prompt", RunOpts{
		RoleID: "test-role",
		Action: ActionExec,
	}, nil)

	if summary.Err != nil {
		t.Fatalf("unexpected error: %v", summary.Err)
	}
	if summary.Output != "test output from mock" {
		t.Errorf("expected output 'test output from mock', got %q", summary.Output)
	}
	if summary.Cost != 0.01 {
		t.Errorf("expected cost 0.01, got %f", summary.Cost)
	}
	if summary.RoleID != "test-role" {
		t.Errorf("expected role 'test-role', got %q", summary.RoleID)
	}
	if summary.ExecID <= 0 {
		t.Errorf("expected ExecID > 0, got %d", summary.ExecID)
	}
	if _, err := os.Stat(summary.StreamFilePath); err != nil {
		t.Errorf("stream file not found: %v", err)
	}
	// Stream file must be inside logs/<exec_id>/
	want := filepath.Join(dir, "logs")
	if !strings.HasPrefix(summary.StreamFilePath, want) {
		t.Errorf("stream file %q does not live under %s", summary.StreamFilePath, want)
	}
}

func TestRunnerWithMockAgentError(t *testing.T) {
	dir := t.TempDir()

	mock := &agent.MockAgent{Err: os.ErrPermission}
	r := newTestRunner(t, dir, mock)

	summary := r.Run(context.Background(), "fail prompt", RunOpts{
		RoleID: "fail-role",
		Action: ActionExec,
	}, nil)

	if summary.Err == nil {
		t.Fatal("expected error from mock agent")
	}
	if summary.RoleID != "fail-role" {
		t.Errorf("expected role 'fail-role', got %q", summary.RoleID)
	}
}

func TestRunnerRequiresStateDir(t *testing.T) {
	r := &Runner{Agent: &agent.MockAgent{Response: "x"}}
	summary := r.Run(context.Background(), "p", RunOpts{Action: ActionExec}, nil)
	if summary.Err == nil {
		t.Fatal("expected error when CallDB is missing")
	}
	if !strings.Contains(summary.Err.Error(), "state directory required") {
		t.Errorf("expected 'state directory required' error, got: %v", summary.Err)
	}
}

func TestRunnerPromotesRuntimeFiles(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "roles", "security")
	if err := os.MkdirAll(dest, 0755); err != nil {
		t.Fatal(err)
	}

	// Mock writes report.md to OUTPUT_FILE inside runtime/<id>/.
	mock := &agent.MockAgent{
		Response:          "report written to {{OUTPUT_FILE}}",
		WriteAtOutputFile: []byte("# Security Report\n\nTotal issues: 3"),
	}
	r := newTestRunner(t, dir, mock)

	summary := r.Run(context.Background(), "scan {{OUTPUT_FILE}}", RunOpts{
		RoleID:           "security",
		Action:           ActionReport,
		OutputKind:       OutputKindReport,
		CanonicalDestDir: dest,
	}, nil)

	if summary.Err != nil {
		t.Fatalf("unexpected error: %v", summary.Err)
	}
	canonical := filepath.Join(dest, "report.md")
	got, err := os.ReadFile(canonical)
	if err != nil {
		t.Fatalf("canonical report.md missing: %v", err)
	}
	if !strings.Contains(string(got), "Security Report") {
		t.Errorf("canonical report.md missing expected content, got: %s", got)
	}
	// Source still in runtime/<id>/.
	src := filepath.Join(dir, "runtime", strings.TrimPrefix(filepath.Base(filepath.Dir(summary.StreamFilePath)), ""))
	srcReport := filepath.Join(src, "report.md")
	if _, err := os.Stat(srcReport); err != nil {
		t.Errorf("runtime/<id>/report.md missing: %v", err)
	}

	// output_file must hold the immutable runtime path so per-run history
	// links keep working after subsequent runs overwrite the canonical copy.
	rows, err := r.CallDB.RecentRuns(calldb.RecentFilter{Action: ActionReport, Limit: 1})
	if err != nil || len(rows) != 1 {
		t.Fatalf("RecentRuns: %v rows=%d", err, len(rows))
	}
	wantRel, _ := filepath.Rel(dir, srcReport)
	if rows[0].OutputFile != wantRel {
		t.Errorf("output_file = %q, want runtime path %q (canonical-path bug regressed)", rows[0].OutputFile, wantRel)
	}
}

// TestRunnerRecordsOutputFileEvenWhenCloneFails pins the fix from commit
// f615104: promoteRuntimeFiles must record the runtime path on the call row
// even when the canonical clone fails. Without this, `ateam cat` / `ateam
// inspect` would report no output for a successful run (output_file stays
// empty) — a P1 data-loss issue in the DB.
func TestRunnerRecordsOutputFileEvenWhenCloneFails(t *testing.T) {
	dir := t.TempDir()
	// Pre-create the canonical dest path as a regular file. fsclone.Clone
	// calls MkdirAll on filepath.Dir(dst); when that path exists as a file
	// the call fails and Clone returns an error before any byte is copied.
	dest := filepath.Join(dir, "roles", "security")
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("not a directory"), 0644); err != nil {
		t.Fatal(err)
	}

	mock := &agent.MockAgent{
		Response:          "report written to {{OUTPUT_FILE}}",
		WriteAtOutputFile: []byte("# Security Report\n\nTotal issues: 3"),
	}
	r := newTestRunner(t, dir, mock)

	summary := r.Run(context.Background(), "scan {{OUTPUT_FILE}}", RunOpts{
		RoleID:           "security",
		Action:           ActionReport,
		OutputKind:       OutputKindReport,
		CanonicalDestDir: dest,
	}, nil)

	// The agent succeeded; clone failures inside finalize are logged but
	// must not flip the run to error.
	if summary.Err != nil {
		t.Fatalf("unexpected run error: %v", summary.Err)
	}
	if summary.IsError {
		t.Fatalf("clone failure must not surface as run error (summary.IsError=true)")
	}

	// runtime/<exec_id>/report.md must still exist on disk — the runner's
	// streamed-text fallback (or the mock's OUTPUT_FILE write) put it there
	// before promote ran.
	runtimeReport := filepath.Join(dir, "runtime", strconv.FormatInt(summary.ExecID, 10), "report.md")
	if _, err := os.Stat(runtimeReport); err != nil {
		t.Errorf("runtime report missing after failed promote: %v", err)
	}

	// agent_execs.output_file must point at the immutable runtime copy even
	// though the canonical clone failed.
	rows, err := r.CallDB.RecentRuns(calldb.RecentFilter{Action: ActionReport, Limit: 1})
	if err != nil || len(rows) != 1 {
		t.Fatalf("RecentRuns: %v rows=%d", err, len(rows))
	}
	wantRel, _ := filepath.Rel(dir, runtimeReport)
	if rows[0].OutputFile == "" {
		t.Errorf("output_file empty after clone failure (primaryRuntime regression)")
	}
	if rows[0].OutputFile != wantRel {
		t.Errorf("output_file = %q, want %q", rows[0].OutputFile, wantRel)
	}
}

func TestRunnerSkipsPromptFilesDuringPromote(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "roles", "security")
	if err := os.MkdirAll(dest, 0755); err != nil {
		t.Fatal(err)
	}

	mock := &agent.MockAgent{
		Response:          "wrote",
		WriteAtOutputFile: []byte("ok"),
		ExtraRuntimeFiles: map[string][]byte{
			"draft_prompt.md": []byte("agent should not clobber"),
			"notes.md":        []byte("supporting notes"),
		},
	}
	r := newTestRunner(t, dir, mock)

	summary := r.Run(context.Background(), "write to {{OUTPUT_FILE}}", RunOpts{
		RoleID:           "security",
		Action:           ActionReport,
		OutputKind:       OutputKindReport,
		CanonicalDestDir: dest,
	}, nil)

	if summary.Err != nil {
		t.Fatalf("unexpected error: %v", summary.Err)
	}
	if _, err := os.Stat(filepath.Join(dest, "report.md")); err != nil {
		t.Errorf("report.md was not promoted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "notes.md")); err != nil {
		t.Errorf("notes.md was not promoted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "draft_prompt.md")); err == nil {
		t.Errorf("draft_prompt.md should be excluded from promote")
	}

	// cmd.md must record the decisions.
	cmdMD := filepath.Join(filepath.Dir(summary.StreamFilePath), "cmd.md")
	body, err := os.ReadFile(cmdMD)
	if err != nil {
		t.Fatalf("read cmd.md: %v", err)
	}
	if !strings.Contains(string(body), "# Files Copy") {
		t.Errorf("cmd.md missing # Files Copy section: %s", body)
	}
	if !strings.Contains(string(body), "draft_prompt.md") {
		t.Errorf("cmd.md should mention skipped draft_prompt.md: %s", body)
	}
}

func TestRunnerWritesPromptFile(t *testing.T) {
	dir := t.TempDir()
	mock := &agent.MockAgent{Response: "ok"}
	r := newTestRunner(t, dir, mock)

	summary := r.Run(context.Background(), "the prompt body", RunOpts{
		RoleID: "any",
		Action: ActionExec,
	}, nil)
	if summary.Err != nil {
		t.Fatalf("unexpected error: %v", summary.Err)
	}
	promptFile := filepath.Join(filepath.Dir(summary.StreamFilePath), "prompt.md")
	body, err := os.ReadFile(promptFile)
	if err != nil {
		t.Fatalf("prompt.md missing: %v", err)
	}
	if string(body) != "the prompt body" {
		t.Errorf("prompt.md content mismatch: got %q", body)
	}
}

func TestRunnerProgress(t *testing.T) {
	dir := t.TempDir()
	mock := &agent.MockAgent{Response: "progress test"}
	r := newTestRunner(t, dir, mock)

	progress := make(chan RunProgress, 64)
	_ = r.Run(context.Background(), "test", RunOpts{RoleID: "progress-role", Action: ActionExec}, progress)
	close(progress)

	var phases []string
	for p := range progress {
		phases = append(phases, p.Phase)
	}
	if len(phases) == 0 {
		t.Error("expected progress events")
	}
	if last := phases[len(phases)-1]; last != PhaseDone {
		t.Errorf("expected last phase 'done', got %q", last)
	}
}

func TestRunnerProgressInitCarriesAgentInfo(t *testing.T) {
	dir := t.TempDir()
	mock := &agent.MockAgent{Response: "init test"}
	r := newTestRunner(t, dir, mock)

	progress := make(chan RunProgress, 64)
	_ = r.Run(context.Background(), "p", RunOpts{RoleID: "init-role", Action: ActionExec}, progress)
	close(progress)

	var inits []RunProgress
	for p := range progress {
		if p.Phase == PhaseInit {
			inits = append(inits, p)
		}
	}
	if len(inits) != 1 {
		t.Fatalf("expected exactly one PhaseInit event, got %d", len(inits))
	}
	// MockAgent reports a SessionID; the runner must propagate it onto
	// the progress event so the live UI can show something useful.
	if inits[0].SessionID != "mock-session" {
		t.Errorf("expected SessionID=mock-session on PhaseInit, got %q", inits[0].SessionID)
	}
}

func TestRunnerProgressIncludesExecID(t *testing.T) {
	dir := t.TempDir()
	mock := &agent.MockAgent{Response: "progress test"}
	r := newTestRunner(t, dir, mock)

	progress := make(chan RunProgress, 64)
	summary := r.Run(context.Background(), "test", RunOpts{RoleID: "progress-role", Action: ActionExec}, progress)
	close(progress)

	if summary.ExecID <= 0 {
		t.Fatalf("expected non-zero exec id, got %d", summary.ExecID)
	}
	for p := range progress {
		if p.ExecID != summary.ExecID {
			t.Fatalf("expected progress exec id %d, got %d", summary.ExecID, p.ExecID)
		}
	}
}

func TestRunnerConfigDirSetsEnv(t *testing.T) {
	dir := t.TempDir()

	mock := &agent.MockAgent{Response: "ok"}
	r := newTestRunner(t, dir, mock)
	r.ConfigDir = ".claude"

	_ = r.Run(context.Background(), "test", RunOpts{RoleID: "iso-role", Action: ActionExec}, nil)

	if len(mock.Requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(mock.Requests))
	}
	want := filepath.Join(dir, ".claude")
	if mock.Requests[0].Env["CLAUDE_CONFIG_DIR"] != want {
		t.Errorf("expected CLAUDE_CONFIG_DIR=%q, got %v", want, mock.Requests[0].Env)
	}
}

func TestRunnerConfigDirAbsolute(t *testing.T) {
	dir := t.TempDir()
	absConfig := filepath.Join(dir, "abs-claude-config")

	mock := &agent.MockAgent{Response: "ok"}
	r := newTestRunner(t, dir, mock)
	r.ConfigDir = absConfig

	_ = r.Run(context.Background(), "test", RunOpts{RoleID: "abs-role", Action: ActionExec}, nil)

	if len(mock.Requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(mock.Requests))
	}
	if mock.Requests[0].Env["CLAUDE_CONFIG_DIR"] != absConfig {
		t.Errorf("expected CLAUDE_CONFIG_DIR=%q, got %v", absConfig, mock.Requests[0].Env)
	}
}

func TestRenderSettingsSandboxExtra(t *testing.T) {
	sandbox := `{
		"sandbox": {
			"filesystem": {
				"allowWrite": ["/base/write"],
				"allowRead": ["/base/read"]
			},
			"network": {
				"allowedDomains": ["base.example.com"]
			}
		},
		"permissions": {}
	}`

	r := &Runner{
		Sandbox: SandboxConfig{
			Settings:     sandbox,
			ExtraWrite:   []string{"/extra/write"},
			ExtraRead:    []string{"/extra/read"},
			ExtraDomains: []string{"extra.example.com"},
		},
	}

	data, err := r.RenderSettings("/work")
	if err != nil {
		t.Fatalf("RenderSettings: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("parse: %v", err)
	}
	fs := result["sandbox"].(map[string]any)["filesystem"].(map[string]any)
	net := result["sandbox"].(map[string]any)["network"].(map[string]any)
	contains := func(list []any, want string) bool {
		for _, v := range list {
			if v.(string) == want {
				return true
			}
		}
		return false
	}
	for _, want := range []string{"/base/write", "/extra/write"} {
		if !contains(fs["allowWrite"].([]any), want) {
			t.Errorf("allowWrite missing %q", want)
		}
	}
	for _, want := range []string{"/base/read", "/extra/read"} {
		if !contains(fs["allowRead"].([]any), want) {
			t.Errorf("allowRead missing %q", want)
		}
	}
	for _, want := range []string{"base.example.com", "extra.example.com"} {
		if !contains(net["allowedDomains"].([]any), want) {
			t.Errorf("allowedDomains missing %q", want)
		}
	}
}

func TestRunnerStallEmitsWarning(t *testing.T) {
	dir := t.TempDir()

	stallWarn := 75 * time.Millisecond
	r := newTestRunner(t, dir, &agent.MockAgent{HoldAfterSystem: 4 * stallWarn})
	r.StallWarnAfter = stallWarn

	progress := make(chan RunProgress, 64)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = r.Run(ctx, "stall test", RunOpts{RoleID: "stalled", Action: ActionExec}, progress)
	close(progress)

	var stalls int
	var lastContent string
	for p := range progress {
		if p.Phase == PhaseStall {
			stalls++
			lastContent = p.Content
		}
	}
	if stalls == 0 {
		t.Fatalf("expected at least one PhaseStall progress event")
	}
	if !strings.Contains(lastContent, "no agent events") {
		t.Errorf("stall content missing expected message: %q", lastContent)
	}
}

func TestRunnerPreservesResultWhenAgentExitsNonZero(t *testing.T) {
	dir := t.TempDir()
	r := newTestRunner(t, dir, &agent.MockAgent{
		Response:               "partial output before stream stalled",
		Cost:                   0.55,
		ResultIsError:          true,
		ProcessExitAfterResult: 1,
	})

	summary := r.Run(context.Background(), "test", RunOpts{
		RoleID: "stalled-api",
		Action: ActionExec,
	}, nil)

	if summary.Cost != 0.55 {
		t.Errorf("expected cost 0.55 from result event, got %v", summary.Cost)
	}
	if !summary.IsError {
		t.Errorf("expected IsError=true from result event")
	}
	if summary.ExitCode != 1 {
		t.Errorf("expected ExitCode=1 from process error, got %d", summary.ExitCode)
	}
	if summary.ErrorSource != agent.ErrorSourceAgentAPI {
		t.Errorf("expected ErrorSource=%q, got %q", agent.ErrorSourceAgentAPI, summary.ErrorSource)
	}

	row, err := r.CallDB.GetRunByID(summary.ExecID)
	if err != nil || row == nil {
		t.Fatalf("GetRunByID(%d): row=%v err=%v", summary.ExecID, row, err)
	}
	if row.CostUSD != 0.55 {
		t.Errorf("DB row cost = %v, want 0.55", row.CostUSD)
	}
}

func TestWriteCmdFileIncludesRunDetailsAndFilesCopy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cmd.md")

	writeCmdFile(path, cmdFileInfo{
		StartedAt:     time.Date(2026, 5, 2, 9, 8, 4, 0, time.UTC),
		EndedAt:       time.Date(2026, 5, 2, 9, 8, 30, 0, time.UTC),
		ExecID:        191,
		Agent:         "claude",
		AgentDef:      "agent \"claude\" {}\n",
		Profile:       "isolated",
		ProfileDef:    "profile \"isolated\" {}\n",
		ContainerType: "docker-exec",
		ContainerName: "myctr",
		Action:        "report",
		Role:          "database_schema",
		Batch:         "code-2026-05-01_23-53-28",
		Model:         "claude-sonnet-4-6",
		Cwd:           "/work",
		CLI:           "claude -p --verbose",
		SpecifiedEnv:  map[string]string{"CLAUDE_CONFIG_DIR": "/custom/cfg", "FOO": "bar", "CLAUDECODE": ""},
		SettingsJSON:  []byte(`{"x":1}`),
		ExitCode:      0,
		Status:        "ok",
		FileCopy: []fileCopyEntry{
			{Source: "runtime/191/report.md", Dest: "roles/database_schema/report.md", Note: "cloned"},
			{Source: "runtime/191/draft_prompt.md", Note: "SKIPPED (*_prompt.md exclusion)"},
		},
	})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(data)

	for _, want := range []string{
		"# Runtime",
		"* profile: isolated",
		"* agent: claude",
		"* model: claude-sonnet-4-6",
		"## profile definition",
		"## agent definition",
		"# Run details",
		"* Exit Code: 0",
		"* Status: ok",
		"# Command",
		"* exec_id: 191",
		"* container: docker-exec (myctr)",
		"* role: database_schema",
		"* batch: code-2026-05-01_23-53-28",
		"## Specified",
		"unsets CLAUDECODE",
		"CLAUDE_CONFIG_DIR=/custom/cfg",
		"FOO=bar",
		"# Settings",
		"# Files Copy",
		"runtime/191/report.md → roles/database_schema/report.md",
		"draft_prompt.md  SKIPPED",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("cmd file missing %q\n--- file ---\n%s", want, got)
		}
	}
	// Ensure dropped section is gone.
	if strings.Contains(got, "# Prompt\n") {
		t.Errorf("cmd.md should not include # Prompt section anymore")
	}
}

func TestResolveExecModel(t *testing.T) {
	configured := &agent.ClaudeAgent{DefaultModel: "claude-opus-4-7"}
	noConfig := &agent.ClaudeAgent{}

	tests := []struct {
		name string
		ev   *agent.StreamEvent
		ag   agent.Agent
		want string
	}{
		{"stream wins over config", &agent.StreamEvent{Model: "claude-sonnet-4-6"}, configured, "claude-sonnet-4-6"},
		{"normalizes dated stream model", &agent.StreamEvent{Model: "claude-sonnet-4-6-2026-01-15"}, noConfig, "claude-sonnet-4-6"},
		{"falls back to configured when stream empty", &agent.StreamEvent{Model: ""}, configured, "claude-opus-4-7"},
		{"falls back to configured when no result event", nil, configured, "claude-opus-4-7"},
		{"empty when nothing is known", nil, noConfig, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveExecModel(tt.ev, tt.ag); got != tt.want {
				t.Errorf("resolveExecModel = %q, want %q", got, tt.want)
			}
		})
	}
}

// avoid unused-import error if calldb is not referenced directly above.
var _ = calldb.Open
