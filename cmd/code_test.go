package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/root"
)

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
