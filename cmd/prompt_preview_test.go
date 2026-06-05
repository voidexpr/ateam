package cmd

import (
	"os"
	"strings"
	"testing"
)

func TestPromptPathsSupervisorReview(t *testing.T) {
	// Setup: empty-but-valid .ateam project so resolveEnv succeeds.
	projectDir := setupMinimalAteamProject(t)
	prev, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(prev) })
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(resetPromptFlags)
	promptAction = "review"
	promptPaths = true

	out := captureStdout(t, func() {
		if err := runPromptPaths(); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, `Assembly for "review"`) {
		t.Errorf("expected assembly header, got:\n%s", out)
	}
	if !strings.Contains(out, "role_main") {
		t.Errorf("expected role_main row, got:\n%s", out)
	}
	if !strings.Contains(out, "defaults/prompts/review.prompt.md") {
		t.Errorf("expected anchor-prefixed review.prompt.md row, got:\n%s", out)
	}
}

func TestPromptPathsFailsOnOrphanFragment(t *testing.T) {
	projectDir := setupMinimalAteamProject(t)
	// A role pre fragment with a typo'd role name and no matching
	// <role>.prompt.md anywhere — an orphan the inspection modes must reject.
	orphanDir := projectDir + "/.ateam/prompts/report"
	if err := os.MkdirAll(orphanDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(orphanDir+"/securty.pre.scope.md", []byte("oops"), 0o644); err != nil {
		t.Fatal(err)
	}

	prev, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(prev) })
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(resetPromptFlags)
	promptRole = "security"
	promptAction = "report"
	promptPaths = true

	err := runPromptPaths()
	if err == nil || !strings.Contains(err.Error(), "orphan") {
		t.Fatalf("expected orphan-fragment error, got %v", err)
	}
}

// TestPromptPathsAllowsUnrelatedOrphan exercises the NON-BLOCKING half of the
// orphan-filter branch the v1 refactor added to runPromptPaths()/inspectionDigestsForCurrentFlags().
// An orphan fragment that is NOT tied to the previewed prompt (different dir,
// unrelated role) must be surfaced on stderr but must NOT fail the preview —
// the real `ateam report` run never calls FindOrphans and succeeds for the
// previewed role, so the inspection must agree. Reverting the branch (blocking
// on ANY orphan, the way TestPromptPathsFailsOnOrphanFragment expects for a
// tied orphan) makes this test fail.
func TestPromptPathsAllowsUnrelatedOrphan(t *testing.T) {
	projectDir := setupMinimalAteamProject(t)
	// Stray fragment for a role that no longer exists, in an unrelated dir
	// (code/, not the previewed report/). No code/tombstone.prompt.md exists in
	// any anchor, so FindOrphans reports it — but it is not tied to report/security.
	orphanDir := projectDir + "/.ateam/prompts/code"
	if err := os.MkdirAll(orphanDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(orphanDir+"/tombstone.post.cleanup.md", []byte("leftover"), 0o644); err != nil {
		t.Fatal(err)
	}

	prev, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(prev) })
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(resetPromptFlags)
	promptRole = "security"
	promptAction = "report"
	promptPaths = true

	var stderr string
	out := captureStdout(t, func() {
		stderr = captureStderr(t, func() {
			if err := runPromptPaths(); err != nil {
				t.Fatalf("expected nil error for an unrelated orphan, got %v", err)
			}
		})
	})

	// The preview still renders — the orphan did not abort assembly.
	if !strings.Contains(out, `Assembly for "report/security"`) {
		t.Errorf("expected assembly header for report/security, got:\n%s", out)
	}
	// ...but the orphan is still reported, just non-fatally.
	if !strings.Contains(stderr, "orphan fragment") || !strings.Contains(stderr, "tombstone") {
		t.Errorf("expected the stray orphan surfaced on stderr, got:\n%s", stderr)
	}
}

func TestPromptInlinePathsInterleavesHeaders(t *testing.T) {
	projectDir := setupMinimalAteamProject(t)
	prev, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(prev) })
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(resetPromptFlags)
	promptAction = "review"
	promptInlinePaths = true

	out := captureStdout(t, func() {
		if err := runPromptInlinePaths(); err != nil {
			t.Fatal(err)
		}
	})

	// Rule line + anchor/path line + metadata line must appear.
	if !strings.Contains(out, "==================================================================") {
		t.Errorf("missing rule-line markers, got:\n%s", out)
	}
	if !strings.Contains(out, "[embedded] defaults/prompts/_pre.context.md") {
		t.Errorf("missing root_pre header, got:\n%s", out)
	}
	if !strings.Contains(out, "slot: root_pre") || !strings.Contains(out, "tokens:") {
		t.Errorf("missing metadata line, got:\n%s", out)
	}
	if !strings.Contains(out, "slot: role_main") {
		t.Errorf("missing role_main metadata line, got:\n%s", out)
	}
	// Content should follow each header. {{project.info}} expansion happens
	// in root_pre's content; check the expanded header is present.
	if !strings.Contains(out, "ATeam Project Context") {
		t.Errorf("missing rendered content from _pre.context.md, got:\n%s", out)
	}
}

func TestPromptPathsBadAction(t *testing.T) {
	t.Cleanup(resetPromptFlags)
	promptRole = "security"
	promptAction = "nonsense"
	promptPaths = true
	_, _, err := promptPathForCurrentFlags()
	if err == nil || !strings.Contains(err.Error(), "invalid action") {
		t.Fatalf("expected invalid-action error, got %v", err)
	}
}

func TestPromptPathForCurrentFlags(t *testing.T) {
	cases := []struct {
		name              string
		role, action      string
		wantPath, wantLbl string
	}{
		{"role report", "security", "report", "report/security", "role security"},
		{"role code", "auth.refactor", "code", "code/auth.refactor", "role auth.refactor"},
		{"supervisor review", "", "review", "review", "the supervisor"},
		{"supervisor code_management", "", "code_management", "code_management", "the supervisor"},
		{"supervisor verify", "", "verify", "code_verify", "the supervisor"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(resetPromptFlags)
			promptRole = tc.role
			promptAction = tc.action
			got, lbl, err := promptPathForCurrentFlags()
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.wantPath || lbl != tc.wantLbl {
				t.Errorf("got (%q,%q), want (%q,%q)", got, lbl, tc.wantPath, tc.wantLbl)
			}
		})
	}
}

func resetPromptFlags() {
	promptRole = ""
	promptAction = ""
	promptPaths = false
	promptInlinePaths = false
	promptPrePrompt = ""
	promptPostPrompt = ""
	promptNoProjectInfo = false
	promptIgnorePreviousReport = false
}

// setupMinimalAteamProject creates a tempdir with a minimal .ateam/config.toml
// so root.Resolve() succeeds without needing a real project tree.
func setupMinimalAteamProject(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	ateam := tmp + "/.ateam"
	if err := os.MkdirAll(ateam, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "[project]\nname = \"testproj\"\n"
	if err := os.WriteFile(ateam+"/config.toml", []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return tmp
}

// TestPromptInlinePathsRendersPrePostPromptRaw verifies the inspection
// modes include --pre-prompt / --post-prompt and leave template tokens
// inside them VERBATIM. Per the Assembler/PromptFactory split
// (implementer note 2): operator-supplied wrappers are pure text, no
// engine expansion. A `{{ns.key}}` inside a wrapper reaches the agent
// as a literal — rationale: when future Prompt impls ship with
// different template syntaxes, "what does pre/post support" stays
// simple and consistent across every impl.
func TestPromptInlinePathsRendersPrePostPromptRaw(t *testing.T) {
	projectDir := setupMinimalAteamProject(t)
	prev, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(prev) })
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(resetPromptFlags)
	promptAction = "review"
	promptInlinePaths = true
	promptPrePrompt = "PRE for {{project.name}}"
	promptPostPrompt = "POST for {{project.name}}"

	out := captureStdout(t, func() {
		if err := runPromptInlinePaths(); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "cli_pre_prompt") || !strings.Contains(out, "PRE for {{project.name}}") {
		t.Errorf("pre-prompt section missing or unexpectedly rendered, got:\n%s", out)
	}
	if !strings.Contains(out, "cli_post_prompt") || !strings.Contains(out, "POST for {{project.name}}") {
		t.Errorf("post-prompt section missing or unexpectedly rendered, got:\n%s", out)
	}
}
