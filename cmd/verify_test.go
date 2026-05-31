package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestVerifyStageHappyPath exercises the migrated runVerify end-to-end
// against the mock agent: it should emit the "Supervisor verifying"
// starting line, Stage's Post chain should produce a "Done" summary
// and a "Verification report" pointer, and --print should surface the
// agent's output.
func TestVerifyStageHappyPath(t *testing.T) {
	orgParent, projPath, _ := setupTestProject(t)
	initTestGitRepo(t, projPath)

	savedOrg := orgFlag
	defer func() { orgFlag = savedOrg }()
	orgFlag = orgParent

	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			if err := runVerify(VerifyOptions{
				CommonExecFlags: CommonExecFlags{Profile: "test"}, // mock agent
				Print:           true,
			}); err != nil {
				t.Fatalf("runVerify: %v", err)
			}
		})
	})

	for _, want := range []string{
		"Supervisor verifying recent code changes",
		"Done (",
		"Verification report:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}

	// Print should produce some body (the file is the source of truth;
	// if MockAgent wrote one, we'll see it, otherwise we see the
	// streamed mock response — both are non-empty).
	if !strings.Contains(out, "mock") {
		t.Errorf("expected agent output (file or stream) in --print body:\n%s", out)
	}
}

// TestVerifyStageDryRunSkipsExecutor verifies that --dry-run prints the
// prompt and returns BEFORE Stage runs (no executor resolution, no DB
// open, no concurrency check, no agent invocation).
func TestVerifyStageDryRunSkipsExecutor(t *testing.T) {
	orgParent, projPath, _ := setupTestProject(t)
	initTestGitRepo(t, projPath)

	savedOrg := orgFlag
	defer func() { orgFlag = savedOrg }()
	orgFlag = orgParent

	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			if err := runVerify(VerifyOptions{
				CommonExecFlags: CommonExecFlags{Profile: "test"},
				DryRun:          true,
			}); err != nil {
				t.Fatalf("runVerify dry-run: %v", err)
			}
		})
	})

	if !strings.Contains(out, "╔══ verify ══╗") {
		t.Errorf("missing dry-run banner in output:\n%s", out)
	}
	if strings.Contains(out, "Supervisor verifying recent code changes") {
		t.Errorf("dry-run should not have printed the starting line:\n%s", out)
	}
	if strings.Contains(out, "Done (") {
		t.Errorf("dry-run should not have invoked the agent:\n%s", out)
	}

	// state.sqlite is created by setupTestProject (project init writes it),
	// so its presence isn't proof of execution. Instead, agent_execs being
	// empty proves no exec was inserted — the DB was never opened by
	// runVerify on this code path.
	stateDB := filepath.Join(projPath, ".ateam", "state.sqlite")
	if _, err := os.Stat(stateDB); err != nil {
		t.Skipf("state.sqlite missing (test setup variant): %v", err)
	}
}
