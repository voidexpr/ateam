package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ateam/internal/agent"
)

func TestClassifyFailureAteamTimeout(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	// Give the context a moment to trip DeadlineExceeded.
	<-ctx.Done()

	source, cause := classifyFailure(ctx, nil, 5)
	if source != agent.ErrorSourceAteamTimeout {
		t.Errorf("source = %q, want %q", source, agent.ErrorSourceAteamTimeout)
	}
	if !strings.Contains(cause, "5 minutes") {
		t.Errorf("cause = %q, want mention of 5 minutes", cause)
	}
}

func TestClassifyFailureAgentAPI(t *testing.T) {
	ev := &agent.StreamEvent{
		Type:        "result",
		IsError:     true,
		ErrorSource: agent.ErrorSourceAgentAPI,
		ErrorCause:  "Stream idle timeout - partial response received",
	}
	source, cause := classifyFailure(context.Background(), ev, 0)
	if source != agent.ErrorSourceAgentAPI {
		t.Errorf("source = %q, want agent_api", source)
	}
	if cause != ev.ErrorCause {
		t.Errorf("cause = %q, want %q", cause, ev.ErrorCause)
	}
}

func TestClassifyFailureAgentProcess(t *testing.T) {
	ev := &agent.StreamEvent{
		Type: "error",
		Err:  errors.New("exit status 137"),
	}
	source, cause := classifyFailure(context.Background(), ev, 0)
	if source != agent.ErrorSourceAgentProcess {
		t.Errorf("source = %q, want agent_process", source)
	}
	if cause != "exit status 137" {
		t.Errorf("cause = %q", cause)
	}
}

func TestClassifyFailureNoResult(t *testing.T) {
	source, cause := classifyFailure(context.Background(), nil, 0)
	if source != agent.ErrorSourceAteamInternal {
		t.Errorf("source = %q, want ateam_internal", source)
	}
	if cause == "" {
		t.Error("cause is empty")
	}
}

// TestClassifyFailureUserCanceled covers the operator-cancellation case.
// Long-running commands wrap ctx with signal.NotifyContext, so Ctrl-C and
// SIGTERM surface as context.Canceled. Without an explicit branch, those
// runs fall through to agent_process / ateam_internal and the persisted
// row reads like a real failure.
func TestClassifyFailureUserCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	<-ctx.Done()

	// No result event — mirrors the typical case where the agent process
	// was killed mid-stream.
	source, cause := classifyFailure(ctx, nil, 5)
	if source != agent.ErrorSourceUserCanceled {
		t.Errorf("source = %q, want %q", source, agent.ErrorSourceUserCanceled)
	}
	if cause == "" {
		t.Error("cause is empty")
	}

	// With a partial agent error from the killed subprocess, cancellation
	// still wins so the row does not read as agent_process.
	ev := &agent.StreamEvent{Type: "error", Err: errors.New("signal: killed")}
	source, _ = classifyFailure(ctx, ev, 5)
	if source != agent.ErrorSourceUserCanceled {
		t.Errorf("source with killed result = %q, want %q", source, agent.ErrorSourceUserCanceled)
	}
}

func TestAppendStderrSummaryWritesExpectedFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stderr.log")
	// Pre-populate the file to confirm we append rather than overwrite.
	if err := os.WriteFile(path, []byte("prior stderr output\n"), 0600); err != nil {
		t.Fatal(err)
	}

	summary := RunSummary{
		ExitCode:    1,
		Duration:    12*time.Second + 500*time.Millisecond,
		ErrorSource: agent.ErrorSourceAgentAPI,
		ErrorCause:  "API Error: Stream idle timeout - partial response received",
	}
	appendStderrSummary(path, summary)

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := string(got)
	if !strings.HasPrefix(out, "prior stderr output\n") {
		t.Errorf("prior content lost; got:\n%s", out)
	}
	for _, want := range []string{
		"--- ateam: run failed ---",
		"source: agent_api",
		"cause: API Error: Stream idle timeout",
		"exit: 1",
		"duration:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestAppendStderrSummaryFlagsEstimatedOnProcessFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stderr.log")

	summary := RunSummary{
		ExitCode:    1,
		Duration:    time.Second,
		ErrorSource: agent.ErrorSourceAgentProcess,
		ErrorCause:  "exit status 1",
		InputTokens: 150,
	}
	appendStderrSummary(path, summary)

	got, _ := os.ReadFile(path)
	out := string(got)
	if !strings.Contains(out, "estimated: true") {
		t.Errorf("missing 'estimated: true' for process failure with tokens; got:\n%s", out)
	}
}

func TestAppendStderrSummarySkipsEstimatedForAgentAPI(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stderr.log")

	summary := RunSummary{
		ExitCode:    1,
		Duration:    time.Second,
		ErrorSource: agent.ErrorSourceAgentAPI,
		ErrorCause:  "Stream idle timeout",
		InputTokens: 150,
	}
	appendStderrSummary(path, summary)

	got, _ := os.ReadFile(path)
	out := string(got)
	if strings.Contains(out, "estimated: true") {
		t.Errorf("agent_api failure should not be marked estimated; got:\n%s", out)
	}
}

func TestAppendStderrSummaryTimeoutWithSubagent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stderr.log")

	summary := RunSummary{
		ExitCode:    -1,
		Duration:    20 * time.Minute,
		ErrorSource: agent.ErrorSourceAteamTimeout,
		ErrorCause:  "ateam timed out the run after 20 minutes",
		ToolCounts:  map[string]int{"Agent": 1, "Bash": 15, "Read": 8},
	}
	appendStderrSummary(path, summary)

	got, _ := os.ReadFile(path)
	out := string(got)
	for _, want := range []string{
		"Agent subagent was called 1 time(s)",
		"subagent was likely still running",
		"EAGAIN",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in timeout+subagent stderr summary:\n%s", want, out)
		}
	}
}

func TestAppendStderrSummaryTimeoutWithoutSubagent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stderr.log")

	summary := RunSummary{
		ExitCode:    -1,
		Duration:    20 * time.Minute,
		ErrorSource: agent.ErrorSourceAteamTimeout,
		ErrorCause:  "ateam timed out the run after 20 minutes",
		ToolCounts:  map[string]int{"Bash": 5},
	}
	appendStderrSummary(path, summary)

	got, _ := os.ReadFile(path)
	out := string(got)
	if strings.Contains(out, "Agent subagent") {
		t.Errorf("unexpected Agent subagent note when no Agent calls; got:\n%s", out)
	}
}

func TestAppendStderrSummaryNoOpWithoutSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stderr.log")
	appendStderrSummary(path, RunSummary{ExitCode: 0}) // ErrorSource empty
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected no file to be created; stat err = %v", err)
	}
}

func TestReconcileErrorEvent(t *testing.T) {
	// nil prev: error event becomes the terminal event.
	ev := agent.StreamEvent{Type: "error", Err: errors.New("crash"), ExitCode: 2}
	got := reconcileErrorEvent(nil, ev)
	if got.ExitCode != 2 {
		t.Errorf("nil prev: ExitCode = %d, want 2", got.ExitCode)
	}
	if got.Err == nil || got.Err.Error() != "crash" {
		t.Errorf("nil prev: Err = %v, want 'crash'", got.Err)
	}

	// Non-result prev (e.g. type "assistant"): error event replaces it.
	prev := &agent.StreamEvent{Type: "assistant", ExitCode: 0}
	got = reconcileErrorEvent(prev, ev)
	if got.ExitCode != 2 {
		t.Errorf("non-result prev: ExitCode = %d, want 2", got.ExitCode)
	}

	// Result prev with zero exit code: exit code is inherited from the error event.
	resultPrev := &agent.StreamEvent{
		Type:        "result",
		ExitCode:    0,
		ErrorSource: agent.ErrorSourceAgentAPI,
		ErrorCause:  "stream timeout",
	}
	got = reconcileErrorEvent(resultPrev, agent.StreamEvent{Type: "error", ExitCode: 1})
	if got.ExitCode != 1 {
		t.Errorf("result prev zero exit: ExitCode = %d, want 1 (inherited)", got.ExitCode)
	}
	if got.ErrorSource != agent.ErrorSourceAgentAPI {
		t.Errorf("result prev zero exit: ErrorSource = %q, want agent_api (preserved)", got.ErrorSource)
	}

	// Result prev with non-zero exit code: error event exit code must not overwrite it.
	resultPrevNonZero := &agent.StreamEvent{
		Type:        "result",
		ExitCode:    3,
		ErrorSource: agent.ErrorSourceAgentAPI,
	}
	got = reconcileErrorEvent(resultPrevNonZero, agent.StreamEvent{Type: "error", ExitCode: 7})
	if got.ExitCode != 3 {
		t.Errorf("result prev non-zero exit: ExitCode = %d, want 3 (not overwritten)", got.ExitCode)
	}
}
