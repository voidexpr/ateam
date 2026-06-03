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
// entry under shared/code/. Without this, once EXEC_ID >= 10 the summary would
// consistently show stale content (e.g. "9" sorts after "11").
func TestPrintCodeSessionSummaryPicksByExecID(t *testing.T) {
	sharedDir := t.TempDir()
	supervisorDir := t.TempDir() // unused for this scenario but the signature wants it
	codeDir := filepath.Join(sharedDir, "code")
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
		printCodeSessionSummary(sharedDir, supervisorDir, 11, false, "")
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

// TestCodeDryRunAgentInjection locks in that --agent on `ateam code` lands
// in the supervisor prompt via {{exec.profile_args}}. Dry-run pre-resolves
// the placeholder so the operator sees the sub-run flags in the printed
// prompt body.
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
	orgFlag = filepath.Dir(orgDir)

	var runErr error
	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			runErr = runCode(CodeOptions{
				CommonExecFlags: CommonExecFlags{Agent: "mock"},
				DryRun:          true,
				Review:          "# Test Review\n\nsome tasks",
			})
		})
	})

	if runErr != nil {
		t.Fatalf("runCode dry-run with agent override: %v", runErr)
	}
	if !strings.Contains(out, "--agent mock") {
		t.Errorf("expected '--agent mock' in code management output (via {{exec.profile_args}}):\n%s", out)
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
				CommonExecFlags: CommonExecFlags{Agent: "mock"},
				DryRun:          true,
				Review:          "# Test Review\n\nsome tasks",
				SupervisorAgent: "mock",
			})
		})
	})

	if runErr != nil {
		t.Fatalf("runCode dry-run with supervisor-agent override: %v", runErr)
	}
}

// TestCodeStageHappyPath exercises the migrated runCode end-to-end
// against the mock agent: starting line, Stage Post chain emits Done +
// session summary (via printCodeSessionAction).
func TestCodeStageHappyPath(t *testing.T) {
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
	orgFlag = filepath.Dir(orgDir)

	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			if err := runCode(CodeOptions{
				CommonExecFlags:   CommonExecFlags{Profile: "test"},
				Review:            "# Test Review\n\nsome tasks",
				SupervisorProfile: "test", // mock agent
			}); err != nil {
				t.Fatalf("runCode: %v", err)
			}
		})
	})

	for _, want := range []string{
		"Code management supervisor running",
		"Done (",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}
