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

func TestAppendStderrSummaryNoOpWithoutSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stderr.log")
	appendStderrSummary(path, RunSummary{ExitCode: 0}) // ErrorSource empty
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected no file to be created; stat err = %v", err)
	}
}
