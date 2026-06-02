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
	noPI, ipr := promptNoProjectInfo, promptIgnorePreviousReport
	paths, inline := promptPaths, promptInlinePaths
	pre, post := promptPrePrompt, promptPostPrompt
	return func() {
		promptRole = role
		promptSupervisor = sup
		promptAction = action
		promptNoProjectInfo = noPI
		promptIgnorePreviousReport = ipr
		promptPaths = paths
		promptInlinePaths = inline
		promptPrePrompt = pre
		promptPostPrompt = post
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

// TestPromptPrePostWrap verifies that --pre-prompt and --post-prompt land at
// the outermost positions of the assembled prompt — pre before any anchor
// content, post after every other section.
func TestPromptPrePostWrap(t *testing.T) {
	defer savePromptGlobals()()
	projPath := setupPromptProject(t)

	promptRole = "testing_basic"
	promptAction = runner.ActionReport
	promptSupervisor = false
	promptPrePrompt = "PRE-MARKER"
	promptPostPrompt = "POST-MARKER"

	var runErr error
	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			runErr = runPrompt(nil, nil)
		})
	})
	if runErr != nil {
		t.Fatalf("runPrompt: %v", runErr)
	}
	preIdx := strings.Index(out, "PRE-MARKER")
	postIdx := strings.Index(out, "POST-MARKER")
	if preIdx < 0 || postIdx < 0 {
		t.Fatalf("missing markers in output: pre=%d post=%d\n%s", preIdx, postIdx, out)
	}
	if preIdx >= postIdx {
		t.Errorf("expected order PRE < POST, got pre=%d post=%d", preIdx, postIdx)
	}
	// PRE should land BEFORE the project-info header from _pre.context.md.
	headerIdx := strings.Index(out, "# ATeam Project Context")
	if headerIdx > 0 && preIdx >= headerIdx {
		t.Errorf("expected PRE before project-info header, got pre=%d header=%d", preIdx, headerIdx)
	}
}

// TestPromptPathsListsAllSources verifies that --paths prints the
// per-section breakdown with anchor, path, mod-time, and token columns,
// plus a TOTAL row.
func TestPromptPathsListsAllSources(t *testing.T) {
	defer savePromptGlobals()()
	projPath := setupPromptProject(t)

	promptRole = "testing_basic"
	promptAction = runner.ActionReport
	promptSupervisor = false
	promptPaths = true

	var runErr error
	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			runErr = runPrompt(nil, nil)
		})
	})
	if runErr != nil {
		t.Fatalf("runPrompt --paths: %v", runErr)
	}
	for _, want := range []string{"SLOT", "ANCHOR", "PATH", "LAST MODIFIED", "EST. TOKENS", "TOTAL"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in --paths output:\n%s", want, out)
		}
	}
}
