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
	role, action := promptRole, promptAction
	noPI, ipr := promptNoProjectInfo, promptIgnorePreviousReport
	paths, inline := promptPaths, promptInlinePaths
	pre, post := promptPrePrompt, promptPostPrompt
	raw := promptRaw
	batch := promptBatch
	return func() {
		promptRole = role
		promptAction = action
		promptNoProjectInfo = noPI
		promptIgnorePreviousReport = ipr
		promptPaths = paths
		promptInlinePaths = inline
		promptPrePrompt = pre
		promptPostPrompt = post
		promptRaw = raw
		promptBatch = batch
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

// TestPromptBatchBakesIntoExecBatch verifies the spec line 559 rule
// ("exec.batch renders the real value in ModePreview when known") via
// the `--batch NAME` flag. Operators pipe `ateam prompt --batch X |
// ateam exec --batch X` and expect the preview's `{{exec.batch}}` to
// substitute the literal X, not the AT RUNTIME sentinel.
//
// Without --batch: `{{exec.batch}}` renders as the sentinel
// `{{AT RUNTIME:exec.batch}}` (every other exec.* key continues to do
// the same — they are runner-allocated and unknown at preview time).
//
// With --batch: only `{{exec.batch}}` substitutes the literal;
// {{exec.id}}, {{exec.output_dir}}, etc. still render the sentinel.
func TestPromptBatchBakesIntoExecBatch(t *testing.T) {
	defer savePromptGlobals()()
	projPath := setupPromptProject(t)

	// Drop an inline-text prompt file (no anchor walk; engine runs)
	// that references both exec.batch (operator-pinnable) and exec.id
	// (always deferred). Tests both behaviors in one shot.
	external := filepath.Join(projPath, "ref.md")
	body := "batch={{exec.batch}} id={{exec.id}}"
	if err := os.WriteFile(external, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	t.Run("without-batch-renders-sentinel", func(t *testing.T) {
		defer savePromptGlobals()()
		out := captureStdout(t, func() {
			withChdir(t, projPath, func() {
				if err := runPrompt(nil, []string{"@" + external}); err != nil {
					t.Fatalf("runPrompt: %v", err)
				}
			})
		})
		if !strings.Contains(out, "batch={{AT RUNTIME:exec.batch}}") {
			t.Errorf("expected exec.batch sentinel without --batch, got:\n%s", out)
		}
	})

	t.Run("with-batch-substitutes-literal", func(t *testing.T) {
		defer savePromptGlobals()()
		promptBatch = "batch-xyz"
		out := captureStdout(t, func() {
			withChdir(t, projPath, func() {
				if err := runPrompt(nil, []string{"@" + external}); err != nil {
					t.Fatalf("runPrompt: %v", err)
				}
			})
		})
		if !strings.Contains(out, "batch=batch-xyz") {
			t.Errorf("expected --batch literal substituted, got:\n%s", out)
		}
		// exec.id still renders as sentinel — only batch is pinnable today.
		if !strings.Contains(out, "id={{AT RUNTIME:exec.id}}") {
			t.Errorf("expected exec.id sentinel even with --batch, got:\n%s", out)
		}
	})
}

// TestPromptLiteralFileMode verifies the positional @PATH inline-text
// form: for a file that does NOT end in .prompt.md, the body runs through
// the engine (vars + dynamics) but no anchor walk or framing. Mirrors
// `ateam exec @PATH` semantics for non-prompt-file inputs.
func TestPromptLiteralFileMode(t *testing.T) {
	defer savePromptGlobals()()
	projPath := setupPromptProject(t)

	promptsDir := filepath.Join(projPath, ".ateam", "prompts")
	if err := os.MkdirAll(promptsDir, 0755); err != nil {
		t.Fatal(err)
	}
	const body = "literal {{prompt.name}} body, no framing"
	// Non-.prompt.md extension: keeps the file on the inline-text path
	// (.prompt.md files route through the framing path instead).
	filePath := filepath.Join(promptsDir, "foobar.md")
	if err := os.WriteFile(filePath, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	t.Run("relative-path", func(t *testing.T) {
		defer savePromptGlobals()()

		var runErr error
		out := captureStdout(t, func() {
			withChdir(t, projPath, func() {
				runErr = runPrompt(nil, []string{"@.ateam/prompts/foobar.md"})
			})
		})
		if runErr != nil {
			t.Fatalf("runPrompt: %v", runErr)
		}
		if !strings.Contains(out, "literal foobar.md body") {
			t.Errorf("expected {{prompt.name}} expanded to foobar.md, got:\n%s", out)
		}
		if strings.Contains(out, "# ATeam Project Context") {
			t.Errorf("inline-text mode should not run the assembler; project context should NOT appear:\n%s", out)
		}
	})

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
		if !strings.Contains(out, "literal foobar.md body") {
			t.Errorf("expected {{prompt.name}} expanded, got:\n%s", out)
		}
	})

	// Known namespace + unknown key errors loudly. The user gets a typo-
	// catching message before the prompt reaches an agent.
	t.Run("invalid-var-errors", func(t *testing.T) {
		defer savePromptGlobals()()

		typoPath := filepath.Join(promptsDir, "typo.md")
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
		passPath := filepath.Join(promptsDir, "pass.md")
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

// TestPromptRawSkipsEngine verifies that --raw on the literal-file path
// prints the file verbatim — no engine expansion, no --batch substitution,
// no project-info injection, no anchor-framing composition even when the
// file ends in .prompt.md.
func TestPromptRawSkipsEngine(t *testing.T) {
	defer savePromptGlobals()()
	projPath := setupPromptProject(t)

	promptsDir := filepath.Join(projPath, ".ateam", "prompts")
	if err := os.MkdirAll(promptsDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Body uses a known-namespace + unknown-key directive that would
	// normally error during engine.Render — proves the engine never runs.
	body := "raw: {{prompt.name}} {{exec.work_dir}}"
	filePath := filepath.Join(promptsDir, "raw.prompt.md")
	if err := os.WriteFile(filePath, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	promptRaw = true

	var runErr error
	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			runErr = runPrompt(nil, []string{"@.ateam/prompts/raw.prompt.md"})
		})
	})
	if runErr != nil {
		t.Fatalf("runPrompt --raw: %v", runErr)
	}
	if !strings.Contains(out, body) {
		t.Errorf("expected file body verbatim, got:\n%s", out)
	}
	if strings.Contains(out, "# ATeam Project Context") {
		t.Errorf("--raw must NOT compose framing, got:\n%s", out)
	}
}

// TestPromptExternalPromptFileFraming verifies the Step 9 dispatch rule:
// when @PATH ends in .prompt.md, the file's parent dir is injected as a
// temporary anchor at the front of the chain and the standard framing
// composes around the file's body — root pre-fragments, dir pre/post,
// project context, etc.
func TestPromptExternalPromptFileFraming(t *testing.T) {
	defer savePromptGlobals()()
	projPath := setupPromptProject(t)

	// Drop a free-standing prompt outside any anchor — into the project
	// dir itself, not .ateam/prompts/ (project anchor) — so Step 9's
	// temp-anchor injection is doing the work.
	externalPath := filepath.Join(projPath, "myrole.prompt.md")
	body := "ROLE BODY for {{prompt.name}}"
	if err := os.WriteFile(externalPath, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	var runErr error
	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			runErr = runPrompt(nil, []string{"@" + externalPath})
		})
	})
	if runErr != nil {
		t.Fatalf("runPrompt @prompt.md: %v", runErr)
	}
	if !strings.Contains(out, "ROLE BODY for myrole") {
		t.Errorf("expected role body with {{prompt.name}} expanded, got:\n%s", out)
	}
	// Framing landed: the embedded _pre.context.md (project info) feeds
	// into the composition through the standard anchor chain even though
	// the role file lives outside it.
	if !strings.Contains(out, "# ATeam Project Context") {
		t.Errorf("expected framing (project context block) from inherited anchors, got:\n%s", out)
	}
}

// TestPromptExternalPromptFilePrePostWrap verifies that the positional
// @PATH.prompt.md preview honors --pre-prompt / --post-prompt — matching
// what `ateam exec @PATH.prompt.md --pre-prompt X --post-prompt Y` would
// feed to the agent. Earlier code routed wrappers through buildArgPrompt
// for exec but dropped them on the prompt-preview side, so operators
// inspecting a run saw a body without the wrappers exec actually sends.
func TestPromptExternalPromptFilePrePostWrap(t *testing.T) {
	defer savePromptGlobals()()
	projPath := setupPromptProject(t)

	externalPath := filepath.Join(projPath, "myrole.prompt.md")
	body := "ROLE BODY"
	if err := os.WriteFile(externalPath, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	promptPrePrompt = "PRE-MARKER"
	promptPostPrompt = "POST-MARKER"

	var runErr error
	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			runErr = runPrompt(nil, []string{"@" + externalPath})
		})
	})
	if runErr != nil {
		t.Fatalf("runPrompt @prompt.md --pre/--post: %v", runErr)
	}
	preIdx := strings.Index(out, "PRE-MARKER")
	bodyIdx := strings.Index(out, "ROLE BODY")
	postIdx := strings.Index(out, "POST-MARKER")
	if preIdx < 0 || bodyIdx < 0 || postIdx < 0 {
		t.Fatalf("missing markers: pre=%d body=%d post=%d\n%s",
			preIdx, bodyIdx, postIdx, out)
	}
	if preIdx >= bodyIdx || bodyIdx >= postIdx {
		t.Errorf("expected order PRE < BODY < POST, got pre=%d body=%d post=%d",
			preIdx, bodyIdx, postIdx)
	}
}

// TestPromptInlineTextPrePostWrap verifies the non-.prompt.md positional
// branch also honors --pre-prompt / --post-prompt — buildArgPrompt's
// inline-text branch concatenates the wrappers around the body before
// feeding PromptText, and `ateam prompt @PATH` must mirror that.
func TestPromptInlineTextPrePostWrap(t *testing.T) {
	defer savePromptGlobals()()
	projPath := setupPromptProject(t)

	externalPath := filepath.Join(projPath, "note.md")
	if err := os.WriteFile(externalPath, []byte("INLINE BODY"), 0644); err != nil {
		t.Fatal(err)
	}

	promptPrePrompt = "PRE-MARKER"
	promptPostPrompt = "POST-MARKER"

	var runErr error
	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			runErr = runPrompt(nil, []string{"@" + externalPath})
		})
	})
	if runErr != nil {
		t.Fatalf("runPrompt @file --pre/--post: %v", runErr)
	}
	preIdx := strings.Index(out, "PRE-MARKER")
	bodyIdx := strings.Index(out, "INLINE BODY")
	postIdx := strings.Index(out, "POST-MARKER")
	if preIdx < 0 || bodyIdx < 0 || postIdx < 0 {
		t.Fatalf("missing markers: pre=%d body=%d post=%d\n%s",
			preIdx, bodyIdx, postIdx, out)
	}
	if preIdx >= bodyIdx || bodyIdx >= postIdx {
		t.Errorf("expected order PRE < BODY < POST, got pre=%d body=%d post=%d",
			preIdx, bodyIdx, postIdx)
	}
}

// TestPromptInlinePathsSurfacesReviewReportsSentinel verifies the spec
// invariant (line 388-399) that `dynamic.review_reports` returns its
// preview sentinel under `ateam prompt --action review`. After the
// step-4-5 migration the manifest is woven into role_main via the
// dynamic, not appended as a separate live section — preview output
// shows the sentinel + structural breakdown, not the actual reports.
//
// Operators who want to see actual reports content run `ateam review
// --dry-run` instead, which runs ModeReal and feeds the real manifest.
func TestPromptInlinePathsSurfacesReviewReportsSentinel(t *testing.T) {
	defer savePromptGlobals()()
	projPath := setupPromptProject(t)

	reportDir := filepath.Join(projPath, ".ateam", "shared", "report")
	if err := os.MkdirAll(reportDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(reportDir, "testing_basic.md"), []byte("# Findings\n\nok"), 0644); err != nil {
		t.Fatal(err)
	}

	promptAction = runner.ActionReview
	promptInlinePaths = true

	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			if err := runPromptInlinePaths(); err != nil {
				t.Fatalf("runPromptInlinePaths: %v", err)
			}
		})
	})

	if !strings.Contains(out, "{{AT RUNTIME: review reports manifest}}") {
		t.Errorf("expected review_reports preview sentinel in role_main, got:\n%s", out)
	}
}

// TestPromptFactoryDispatch covers the factory-map dispatch in
// runPromptAction: known actions go through the curated factories; unknown
// actions fall back to assembleAction's anchor-walk so any custom
// `.ateam/prompts/<name>.prompt.md` works without a factory registration.
func TestPromptFactoryDispatch(t *testing.T) {
	defer savePromptGlobals()()
	projPath := setupPromptProject(t)

	// Seed a report so the review factory's prompt assembles cleanly.
	reportDir := filepath.Join(projPath, ".ateam", "shared", "report")
	if err := os.MkdirAll(reportDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(reportDir, "testing_basic.md"), []byte("# Findings\n\nok"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Run("known-action-review", func(t *testing.T) {
		defer savePromptGlobals()()
		promptAction = runner.ActionReview
		promptRole = ""

		var runErr error
		out := captureStdout(t, func() {
			withChdir(t, projPath, func() {
				runErr = runPrompt(nil, nil)
			})
		})
		if runErr != nil {
			t.Fatalf("runPrompt --action review: %v", runErr)
		}
		// The review factory composes the reports block; confirm it
		// landed (proves we hit the factory, not assembleAction's bare
		// review.prompt.md path).
		if !strings.Contains(out, "Reports Under Review") {
			t.Errorf("expected reports manifest from review factory, got:\n%s", out)
		}
	})

	t.Run("unknown-action-falls-back-to-anchor-walk", func(t *testing.T) {
		defer savePromptGlobals()()
		// Drop a custom action prompt — assembleAction's fallback should
		// find it.
		promptsDir := filepath.Join(projPath, ".ateam", "prompts")
		if err := os.MkdirAll(promptsDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(promptsDir, "myaction.prompt.md"),
			[]byte("CUSTOM ACTION: {{prompt.action}}"), 0644,
		); err != nil {
			t.Fatal(err)
		}

		promptAction = "myaction"
		promptRole = ""

		var runErr error
		out := captureStdout(t, func() {
			withChdir(t, projPath, func() {
				runErr = runPrompt(nil, nil)
			})
		})
		if runErr != nil {
			t.Fatalf("runPrompt --action myaction: %v", runErr)
		}
		if !strings.Contains(out, "CUSTOM ACTION: myaction") {
			t.Errorf("expected fallback to find myaction.prompt.md, got:\n%s", out)
		}
	})
}

// TestPromptInlinePathsDoesNotDuplicateCliPostPrompt locks in the
// commit-9e96d4d fix: `ateam prompt --inline-paths --action review
// --post-prompt X` must emit exactly one `cli_post_prompt` section.
// Pre-fix, inspectionDigestsForCurrentFlags appended its own
// addLive("cli_post_prompt") on top of the assembler-emitted slot,
// double-counting the user-supplied wrapper. Symptom would be the
// POST text appearing twice in the digest stream and one extra
// section row at the bottom of --paths.
func TestPromptInlinePathsDoesNotDuplicateCliPostPrompt(t *testing.T) {
	defer savePromptGlobals()()
	projPath := setupPromptProject(t)

	// Seed a report so the review factory composes cleanly (the review
	// bundle reaches for shared/report/<role>.md).
	reportDir := filepath.Join(projPath, ".ateam", "shared", "report")
	if err := os.MkdirAll(reportDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(reportDir, "testing_basic.md"), []byte("# Findings\n\nok"), 0644); err != nil {
		t.Fatal(err)
	}

	promptAction = runner.ActionReview
	promptInlinePaths = true
	promptPostPrompt = "MY-CLI-POST-PROMPT-MARKER"

	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			if err := runPromptInlinePaths(); err != nil {
				t.Fatalf("runPromptInlinePaths: %v", err)
			}
		})
	})

	postMarkerCount := strings.Count(out, "MY-CLI-POST-PROMPT-MARKER")
	// The marker appears in (a) the slot header line that names the
	// section ("slot: cli_post_prompt") — NO, that line only names the
	// slot, not the content; and (b) the rendered body of the section.
	// So we expect the literal marker exactly once across all bodies.
	if postMarkerCount != 1 {
		t.Errorf("expected --post-prompt body to appear exactly once in --inline-paths output, got %d occurrences:\n%s",
			postMarkerCount, out)
	}
	// Sanity: the cli_post_prompt slot header should also appear exactly
	// once. Two slot headers means inspectionDigestsForCurrentFlags is
	// double-appending the synthesized live section on top of the
	// assembler-emitted one.
	slotCount := strings.Count(out, "slot: cli_post_prompt")
	if slotCount != 1 {
		t.Errorf("expected one 'slot: cli_post_prompt' header, got %d:\n%s", slotCount, out)
	}
}
