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

// TestPromptLiteralFileMode verifies the positional @PATH form: the file's
// content is printed verbatim with the --batch override applied. No
// assembler composition — mirrors `ateam exec @PATH` semantics. Covers both
// absolute and project-relative path forms (the docs imply the relative form
// is the common path-from-project-root case).
func TestPromptLiteralFileMode(t *testing.T) {
	defer savePromptGlobals()()
	projPath := setupPromptProject(t)

	promptsDir := filepath.Join(projPath, ".ateam", "prompts")
	if err := os.MkdirAll(promptsDir, 0755); err != nil {
		t.Fatal(err)
	}
	const body = "literal {{BATCH}} body, no framing"
	filePath := filepath.Join(promptsDir, "foobar.prompt.md")
	if err := os.WriteFile(filePath, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	// Relative path from project root works (common case).
	t.Run("relative-path", func(t *testing.T) {
		defer savePromptGlobals()()
		promptBatchSaved := promptBatch
		t.Cleanup(func() { promptBatch = promptBatchSaved })
		promptBatch = "batch-xyz"

		var runErr error
		out := captureStdout(t, func() {
			withChdir(t, projPath, func() {
				runErr = runPrompt(nil, []string{"@.ateam/prompts/foobar.prompt.md"})
			})
		})
		if runErr != nil {
			t.Fatalf("runPrompt: %v", runErr)
		}
		if !strings.Contains(out, "batch-xyz") {
			t.Errorf("expected --batch to replace {{BATCH}}, got:\n%s", out)
		}
		if strings.Contains(out, "{{BATCH}}") {
			t.Errorf("expected {{BATCH}} to be substituted, still present:\n%s", out)
		}
		if strings.Contains(out, "# ATeam Project Context") {
			t.Errorf("literal-file mode should not run the assembler; project context should NOT appear:\n%s", out)
		}
	})

	// Absolute path also works.
	t.Run("absolute-path", func(t *testing.T) {
		defer savePromptGlobals()()

		var runErr error
		out := captureStdout(t, func() {
			withChdir(t, projPath, func() {
				runErr = runPrompt(nil, []string{"@" + filePath})
			})
		})
		if runErr != nil {
			t.Fatalf("runPrompt: %v", runErr)
		}
		if !strings.Contains(out, "literal {{BATCH}} body") {
			t.Errorf("expected file contents (with placeholder), got:\n%s", out)
		}
	})

	// Known namespace + unknown key errors loudly. The user gets a typo-
	// catching message before the prompt reaches an agent.
	t.Run("invalid-var-errors", func(t *testing.T) {
		defer savePromptGlobals()()

		typoPath := filepath.Join(promptsDir, "typo.prompt.md")
		if err := os.WriteFile(typoPath, []byte("{{exec.work_dir}} is not a real key"), 0644); err != nil {
			t.Fatal(err)
		}

		var runErr error
		captureStdout(t, func() {
			withChdir(t, projPath, func() {
				runErr = runPrompt(nil, []string{"@" + typoPath})
			})
		})
		if runErr == nil {
			t.Fatal("expected error for unknown key in exec namespace, got nil")
		}
		if !strings.Contains(runErr.Error(), "unknown key in exec namespace") {
			t.Errorf("expected 'unknown key in exec namespace' in error, got: %v", runErr)
		}
	})

	// Unknown namespace passes through verbatim — leaves agent-emitted
	// braces and arbitrary user identifiers alone.
	t.Run("unknown-namespace-passes-through", func(t *testing.T) {
		defer savePromptGlobals()()

		raw := "leave alone: {{foo.bar}} {{some_user_token}}"
		passPath := filepath.Join(promptsDir, "pass.prompt.md")
		if err := os.WriteFile(passPath, []byte(raw), 0644); err != nil {
			t.Fatal(err)
		}

		var runErr error
		out := captureStdout(t, func() {
			withChdir(t, projPath, func() {
				runErr = runPrompt(nil, []string{"@" + passPath})
			})
		})
		if runErr != nil {
			t.Fatalf("runPrompt: %v", runErr)
		}
		if !strings.Contains(out, "{{foo.bar}}") || !strings.Contains(out, "{{some_user_token}}") {
			t.Errorf("expected unknown-namespace and unknown-ALL_CAPS to pass through, got:\n%s", out)
		}
	})

	// Valid project-namespace vars expand against the resolved env.
	t.Run("project-vars-expand", func(t *testing.T) {
		defer savePromptGlobals()()

		varsPath := filepath.Join(promptsDir, "projvars.prompt.md")
		if err := os.WriteFile(varsPath, []byte("dir={{project.dir}}"), 0644); err != nil {
			t.Fatal(err)
		}

		var runErr error
		out := captureStdout(t, func() {
			withChdir(t, projPath, func() {
				runErr = runPrompt(nil, []string{"@" + varsPath})
			})
		})
		if runErr != nil {
			t.Fatalf("runPrompt: %v", runErr)
		}
		if !strings.Contains(out, "dir=myproj") {
			t.Errorf("expected {{project.dir}} to expand to 'myproj', got:\n%s", out)
		}
	})
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
