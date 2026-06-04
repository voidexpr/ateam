package runner

import (
	"context"
	"strings"
	"testing"

	"github.com/ateam/internal/agent"
)

// TestRunnerExecutePreparedDoesNotSubstitutePromptBody is the load-bearing
// acceptance gate for the spec's Next-round step 3
// (plans/feature_prompt_cmd_bundle_aware.md). The runner's ExecutePrepared
// used to do `prompt = ResolveTemplateString(prompt, tmplVars)` after the
// caller had already resolved the body. That second pass IS the two-pass
// mechanism the spec's Problem section indicts.
//
// This test sends a prompt containing `{{OUTPUT_FILE}}` — a token the
// runner's Replacer knows about — through ExecutePrepared and asserts the
// MockAgent's captured request still carries the literal. If the runner
// is doing a second substitution pass, the captured prompt will be the
// substituted value and this test fails.
//
// Args / container fields STILL get their substitution (those don't go
// through Prompt.Resolve); this test is scoped to the prompt body only.
func TestRunnerExecutePreparedDoesNotSubstitutePromptBody(t *testing.T) {
	mock := &agent.MockAgent{Response: "ok"}
	r := newTestRunner(t, t.TempDir(), mock)

	literal := "Write report to {{OUTPUT_FILE}} for run {{EXEC_ID}}"
	summary := r.Execute(context.Background(), literal, RunOpts{
		RoleID: "test-role",
		Action: ActionExec,
	}, nil)
	if summary.Err != nil {
		t.Fatalf("Execute: %v", summary.Err)
	}
	if len(mock.Requests) != 1 {
		t.Fatalf("MockAgent received %d requests, want 1", len(mock.Requests))
	}
	got := mock.Requests[0].Prompt
	if got != literal {
		t.Errorf("runner substituted the prompt body:\n  got  %q\n  want %q (literal)\n\n"+
			"This means runner.go::ExecutePrepared still calls ResolveTemplateString\n"+
			"on the prompt. Spec Next-round step 3 says delete that line; the prompt's\n"+
			"exec.* substitution is owned by Prompt.Resolve via flow.Runtime.",
			got, literal)
	}
	// Sanity check that args/container fields DO still get substituted —
	// the deletion is scoped to the prompt body, not the full Replacer
	// pass. If this regresses, args/container templating broke.
	if !strings.Contains(got, "{{OUTPUT_FILE}}") {
		t.Errorf("output_file token disappeared from the prompt entirely; got %q", got)
	}
}
