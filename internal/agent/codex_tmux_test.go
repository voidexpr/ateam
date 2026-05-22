package agent

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestCodexTmuxAgentDebugCommandArgs(t *testing.T) {
	a := &CodexTmuxAgent{
		Command: "codex",
		Args:    []string{"--no-alt-screen", "--sandbox", "workspace-write"},
		Model:   "gpt-5.5",
		Effort:  "xhigh",
	}

	_, args := a.DebugCommandArgs([]string{"--ask-for-approval", "never"})
	want := []string{
		"--no-alt-screen",
		"--sandbox", "workspace-write",
		"-c", "check_for_update_on_startup=false",
		"--disable", "apps",
		"--disable", "plugins",
		"--model", "gpt-5.5",
		"-c", "model_reasoning_effort=xhigh",
		"--ask-for-approval", "never",
	}
	if !slices.Equal(args, want) {
		t.Errorf("args = %v, want %v", args, want)
	}
}

func TestCodexTmuxAgentCloneCopiesMutableFields(t *testing.T) {
	a := &CodexTmuxAgent{
		Args:             []string{"--name", "{{ROLE}}"},
		Env:              map[string]string{"ROLE": "{{ROLE}}"},
		Pricing:          PricingTable{"m": {InputPerToken: 1}},
		StartTimeout:     time.Second,
		BusyTimeout:      2 * time.Second,
		QuiescenceWindow: 3 * time.Second,
		ProjectDir:       "/tmp/project",
	}
	r := strings.NewReplacer("{{ROLE}}", "security")

	clone := a.CloneWithResolvedTemplates(r).(*CodexTmuxAgent)
	clone.Args[1] = "mutated"
	clone.Env["ROLE"] = "mutated"
	clone.Pricing["m"] = ModelPrice{InputPerToken: 9}

	if a.Args[1] != "{{ROLE}}" {
		t.Errorf("original args mutated: %v", a.Args)
	}
	if a.Env["ROLE"] != "{{ROLE}}" {
		t.Errorf("original env mutated: %v", a.Env)
	}
	if a.Pricing["m"].InputPerToken != 1 {
		t.Errorf("original pricing mutated: %v", a.Pricing)
	}
	if clone.ProjectDir != "/tmp/project" {
		t.Errorf("ProjectDir lost in clone: %q", clone.ProjectDir)
	}
}

// TestCodexTmuxRunRejectsMissingProjectDir ensures the agent fails fast when
// the runner forgot to populate ProjectDir, instead of trying to write the
// tmux socket somewhere unpredictable.
func TestCodexTmuxRunRejectsMissingProjectDir(t *testing.T) {
	a := &CodexTmuxAgent{Command: "/bin/true"}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got := Result(a.Run(ctx, Request{Prompt: "hello", ExecID: 1}))
	if got.Type != "error" {
		t.Fatalf("expected terminal error event, got %+v", got)
	}
	if !strings.Contains(got.ErrorCause, "project context") {
		t.Errorf("error cause = %q, want substring 'project context'", got.ErrorCause)
	}
}

// TestCodexTmuxRunRejectsMissingExecID ensures the agent fails fast without
// an EXEC_ID — without it we'd lose per-run socket isolation.
func TestCodexTmuxRunRejectsMissingExecID(t *testing.T) {
	a := &CodexTmuxAgent{Command: "/bin/true", ProjectDir: t.TempDir()}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got := Result(a.Run(ctx, Request{Prompt: "hello"})) // ExecID = 0
	if got.Type != "error" {
		t.Fatalf("expected terminal error event, got %+v", got)
	}
	if !strings.Contains(got.ErrorCause, "ExecID") {
		t.Errorf("error cause = %q, want substring 'ExecID'", got.ErrorCause)
	}
}
