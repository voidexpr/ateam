package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ateam-poc/internal/agent"
)

func TestRunnerWithMockAgent(t *testing.T) {
	dir := t.TempDir()
	logsDir := filepath.Join(dir, "logs")

	mock := &agent.MockAgent{
		Response: "test output from mock",
		Cost:     0.01,
	}

	r := &Runner{
		Agent: mock,
	}

	opts := RunOpts{
		RoleID:  "test-role",
		Action:  ActionRun,
		LogsDir: logsDir,
	}

	ctx := context.Background()
	summary := r.Run(ctx, "test prompt", opts, nil)

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
	if summary.Turns != 1 {
		t.Errorf("expected 1 turn, got %d", summary.Turns)
	}

	// Verify stream file was created
	if summary.StreamFilePath == "" {
		t.Error("expected non-empty stream file path")
	}
	if _, err := os.Stat(summary.StreamFilePath); err != nil {
		t.Errorf("stream file not found: %v", err)
	}
}

func TestRunnerWithMockAgentError(t *testing.T) {
	dir := t.TempDir()
	logsDir := filepath.Join(dir, "logs")
	errFile := filepath.Join(dir, "error.md")

	mock := &agent.MockAgent{
		Err: os.ErrPermission,
	}

	r := &Runner{Agent: mock}

	opts := RunOpts{
		RoleID:               "fail-role",
		Action:               ActionRun,
		LogsDir:              logsDir,
		ErrorMessageFilePath: errFile,
	}

	summary := r.Run(context.Background(), "fail prompt", opts, nil)

	if summary.Err == nil {
		t.Fatal("expected error from mock agent")
	}
	if summary.RoleID != "fail-role" {
		t.Errorf("expected role 'fail-role', got %q", summary.RoleID)
	}
}

func TestRunnerWritesOutputFile(t *testing.T) {
	dir := t.TempDir()
	logsDir := filepath.Join(dir, "logs")
	outputFile := filepath.Join(dir, "output.md")

	mock := &agent.MockAgent{Response: "report content here"}

	r := &Runner{Agent: mock}

	opts := RunOpts{
		RoleID:              "writer-role",
		Action:              ActionReport,
		LogsDir:             logsDir,
		LastMessageFilePath: outputFile,
	}

	summary := r.Run(context.Background(), "write me a report", opts, nil)

	if summary.Err != nil {
		t.Fatalf("unexpected error: %v", summary.Err)
	}

	data, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatalf("cannot read output file: %v", err)
	}
	if string(data) != "report content here" {
		t.Errorf("expected 'report content here', got %q", string(data))
	}
}

func TestRunnerProgress(t *testing.T) {
	dir := t.TempDir()
	logsDir := filepath.Join(dir, "logs")

	mock := &agent.MockAgent{Response: "progress test"}

	r := &Runner{Agent: mock}

	opts := RunOpts{
		RoleID:  "progress-role",
		Action:  ActionRun,
		LogsDir: logsDir,
	}

	progress := make(chan RunProgress, 64)
	_ = r.Run(context.Background(), "test", opts, progress)
	close(progress)

	var phases []string
	for p := range progress {
		phases = append(phases, p.Phase)
	}

	if len(phases) == 0 {
		t.Error("expected progress events")
	}

	// Should end with "done"
	last := phases[len(phases)-1]
	if last != PhaseDone {
		t.Errorf("expected last phase 'done', got %q", last)
	}
}

func TestRunnerConfigDirSetsEnv(t *testing.T) {
	dir := t.TempDir()
	logsDir := filepath.Join(dir, "logs")
	projectDir := filepath.Join(dir, ".ateam")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}

	mock := &agent.MockAgent{Response: "ok"}

	r := &Runner{
		Agent:      mock,
		ProjectDir: projectDir,
		ConfigDir:  ".claude",
	}

	opts := RunOpts{
		RoleID:  "iso-role",
		Action:  ActionRun,
		LogsDir: logsDir,
	}

	_ = r.Run(context.Background(), "test", opts, nil)

	if len(mock.Requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(mock.Requests))
	}
	req := mock.Requests[0]
	want := filepath.Join(projectDir, ".claude")
	if req.Env == nil || req.Env["CLAUDE_CONFIG_DIR"] != want {
		t.Errorf("expected CLAUDE_CONFIG_DIR=%q in request env, got %v", want, req.Env)
	}
}

func TestRunnerConfigDirAbsolute(t *testing.T) {
	dir := t.TempDir()
	logsDir := filepath.Join(dir, "logs")
	absConfigDir := filepath.Join(dir, "abs-claude-config")

	mock := &agent.MockAgent{Response: "ok"}

	r := &Runner{
		Agent:     mock,
		ConfigDir: absConfigDir,
		// no ProjectDir needed for absolute paths
	}

	opts := RunOpts{
		RoleID:  "abs-role",
		Action:  ActionRun,
		LogsDir: logsDir,
	}

	_ = r.Run(context.Background(), "test", opts, nil)

	if len(mock.Requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(mock.Requests))
	}
	req := mock.Requests[0]
	if req.Env == nil || req.Env["CLAUDE_CONFIG_DIR"] != absConfigDir {
		t.Errorf("expected CLAUDE_CONFIG_DIR=%q, got %v", absConfigDir, req.Env)
	}
}

func TestRunnerConfigDirRelativeRequiresProject(t *testing.T) {
	dir := t.TempDir()
	logsDir := filepath.Join(dir, "logs")

	mock := &agent.MockAgent{Response: "ok"}

	r := &Runner{
		Agent:     mock,
		ConfigDir: ".claude",
		// no ProjectDir
	}

	opts := RunOpts{
		RoleID:  "no-proj",
		Action:  ActionRun,
		LogsDir: logsDir,
	}

	summary := r.Run(context.Background(), "test", opts, nil)
	if summary.Err == nil {
		t.Fatal("expected error when relative config_dir is set without project context")
	}
}

func TestRunnerArchivesPrompt(t *testing.T) {
	dir := t.TempDir()
	logsDir := filepath.Join(dir, "logs")
	historyDir := filepath.Join(dir, "history")

	mock := &agent.MockAgent{Response: "ok"}

	r := &Runner{Agent: mock}

	opts := RunOpts{
		RoleID:     "archive-role",
		Action:     ActionRun,
		LogsDir:    logsDir,
		HistoryDir: historyDir,
		PromptName: "test_prompt.md",
	}

	_ = r.Run(context.Background(), "prompt to archive", opts, nil)

	entries, err := os.ReadDir(historyDir)
	if err != nil {
		t.Fatalf("cannot read history dir: %v", err)
	}
	if len(entries) == 0 {
		t.Error("expected archived prompt in history dir")
	}
}
