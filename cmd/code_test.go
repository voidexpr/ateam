package cmd

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ateam/internal/root"
)

// TestPrintCodeSessionSummaryPicksByExecID verifies that printCodeSessionSummary
// selects the directory matching result.ExecID, not the lexicographically last
// entry under <supervisorDir>/code/. Without this, once EXEC_ID >= 10 the
// summary would consistently show stale content (e.g. "9" sorts after "11").
func TestPrintCodeSessionSummaryPicksByExecID(t *testing.T) {
	supervisorDir := t.TempDir()
	codeDir := filepath.Join(supervisorDir, "code")
	if err := os.MkdirAll(codeDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create directories 1..11 with distinctive execution_report.md content.
	for i := 1; i <= 11; i++ {
		d := filepath.Join(codeDir, strconv.Itoa(i))
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
		report := []byte("REPORT-" + strconv.Itoa(i) + "\n")
		if err := os.WriteFile(filepath.Join(d, "execution_report.md"), report, 0644); err != nil {
			t.Fatal(err)
		}
	}

	out := captureStdout(t, func() {
		printCodeSessionSummary(supervisorDir, 11, false, "")
	})

	if !strings.Contains(out, "REPORT-11") {
		t.Errorf("expected REPORT-11 in output, got:\n%s", out)
	}
	// Guard against the old lexicographic behavior: "9" sorts after "11", so
	// the buggy implementation would surface REPORT-9 instead of REPORT-11.
	if strings.Contains(out, "REPORT-9\n") {
		t.Errorf("output should not contain REPORT-9 when execID=11:\n%s", out)
	}
	wantSession := filepath.Join("code", "11")
	if !strings.Contains(out, wantSession) {
		t.Errorf("expected session path containing %q in output:\n%s", wantSession, out)
	}
}

func TestCodeDryRunAgentInjection(t *testing.T) {
	base := t.TempDir()
	orgDir, err := root.InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}
	projPath := filepath.Join(base, "myproj")
	if err := os.MkdirAll(projPath, 0755); err != nil {
		t.Fatal(err)
	}
	initTestGitRepo(t, projPath)
	if _, err := root.InitProject(projPath, orgDir, root.InitProjectOpts{
		Name:         "myproj",
		EnabledRoles: []string{"testing_basic"},
	}); err != nil {
		t.Fatalf("InitProject: %v", err)
	}

	savedOrg := orgFlag
	defer func() { orgFlag = savedOrg }()
	orgFlag = filepath.Dir(orgDir) // --org takes the parent of .ateamorg/

	// Passing review content directly avoids needing a review.md on disk.
	var runErr error
	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			runErr = runCode(CodeOptions{
				DryRun: true,
				Review: "# Test Review\n\nsome tasks",
				Agent:  "mock",
			})
		})
	})

	if runErr != nil {
		t.Fatalf("runCode dry-run with agent override: %v", runErr)
	}
	// The agent override must be injected into the sub-run flags section.
	if !strings.Contains(out, "--agent mock") {
		t.Errorf("expected '--agent mock' in code management output:\n%s", out)
	}
}

func TestCodeDryRunSupervisorAgentOverride(t *testing.T) {
	base := t.TempDir()
	orgDir, err := root.InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}
	projPath := filepath.Join(base, "myproj")
	if err := os.MkdirAll(projPath, 0755); err != nil {
		t.Fatal(err)
	}
	initTestGitRepo(t, projPath)
	if _, err := root.InitProject(projPath, orgDir, root.InitProjectOpts{
		Name:         "myproj",
		EnabledRoles: []string{"testing_basic"},
	}); err != nil {
		t.Fatalf("InitProject: %v", err)
	}

	savedOrg := orgFlag
	defer func() { orgFlag = savedOrg }()
	orgFlag = filepath.Dir(orgDir) // --org takes the parent of .ateamorg/

	// With both supervisor-agent and agent overrides, dry-run should succeed
	// and return before invoking the supervisor runner.
	var runErr error
	captureStdout(t, func() {
		withChdir(t, projPath, func() {
			runErr = runCode(CodeOptions{
				DryRun:          true,
				Review:          "# Test Review\n\nsome tasks",
				SupervisorAgent: "mock",
				Agent:           "mock",
			})
		})
	})

	if runErr != nil {
		t.Fatalf("runCode dry-run with supervisor-agent override: %v", runErr)
	}
}
