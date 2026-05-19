package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
)

// resetPromptGlobals zeroes the package-level flag vars that runPrompt reads.
// Tests must restore them on exit to avoid leaking state across subtests.
func savePromptGlobals() func() {
	role, sup, action := promptRole, promptSupervisor, promptAction
	extra, noPI, ipr := promptExtraPrompt, promptNoProjectInfo, promptIgnorePreviousReport
	files := promptFilesOnly
	return func() {
		promptRole = role
		promptSupervisor = sup
		promptAction = action
		promptExtraPrompt = extra
		promptNoProjectInfo = noPI
		promptIgnorePreviousReport = ipr
		promptFilesOnly = files
	}
}

func setupPromptProject(t *testing.T) string {
	t.Helper()
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
	t.Cleanup(func() { orgFlag = savedOrg })
	orgFlag = filepath.Dir(orgDir)
	return projPath
}

// TestPromptRoleDryRun verifies that `ateam prompt --role ROLE --action report`
// assembles a prompt and prints it to stdout without error. The "dry-run" here
// is implicit: runPrompt never touches the runner or DB — it only resolves and
// prints.
func TestPromptRoleDryRun(t *testing.T) {
	defer savePromptGlobals()()
	projPath := setupPromptProject(t)

	promptRole = "testing_basic"
	promptAction = runner.ActionReport
	promptSupervisor = false
	promptFilesOnly = false

	var runErr error
	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			runErr = runPrompt(nil, nil)
		})
	})
	if runErr != nil {
		t.Fatalf("runPrompt: %v", runErr)
	}
	if strings.TrimSpace(out) == "" {
		t.Errorf("expected assembled prompt on stdout, got empty output")
	}
}

// TestPromptFilesOnlyListsAllSources verifies that --files-only prints the
// list of prompt sources (with a TOTAL row) to stdout instead of the prompt
// content. This is a read-only smoke test.
func TestPromptFilesOnlyListsAllSources(t *testing.T) {
	defer savePromptGlobals()()
	projPath := setupPromptProject(t)

	promptRole = "testing_basic"
	promptAction = runner.ActionReport
	promptSupervisor = false
	promptFilesOnly = true

	var runErr error
	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			runErr = runPrompt(nil, nil)
		})
	})
	if runErr != nil {
		t.Fatalf("runPrompt --files-only: %v", runErr)
	}
	for _, want := range []string{"PATH", "EST. TOKENS", "TOTAL"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in --files-only output:\n%s", want, out)
		}
	}
}
