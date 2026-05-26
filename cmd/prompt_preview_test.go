package cmd

import (
	"os"
	"strings"
	"testing"
)

func TestPromptPreviewSupervisorReview(t *testing.T) {
	// Setup: empty-but-valid .ateam project so resolveEnv succeeds.
	projectDir := setupMinimalAteamProject(t)
	prev, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(prev) })
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(resetPromptFlags)
	promptSupervisor = true
	promptAction = "review"
	promptPreview = true

	out := captureStdout(t, func() {
		if err := runPromptPreview(); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, `Assembly for "review"`) {
		t.Errorf("expected assembly header, got:\n%s", out)
	}
	if !strings.Contains(out, "role_main") {
		t.Errorf("expected role_main row, got:\n%s", out)
	}
	if !strings.Contains(out, "review.prompt.md") {
		t.Errorf("expected review.prompt.md row, got:\n%s", out)
	}
}

func TestPromptPreviewRoleReport(t *testing.T) {
	projectDir := setupMinimalAteamProject(t)
	prev, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(prev) })
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(resetPromptFlags)
	promptRole = "security"
	promptAction = "report"
	promptPreview = true
	promptPreviewContent = true

	out := captureStdout(t, func() {
		if err := runPromptPreview(); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, `"report/security"`) {
		t.Errorf("missing prompt path header, got:\n%s", out)
	}
	if !strings.Contains(out, "report/security.prompt.md") {
		t.Errorf("missing role_main path, got:\n%s", out)
	}
	if !strings.Contains(out, "--- assembled prompt ---") {
		t.Errorf("--content should print the assembled prompt, got:\n%s", out)
	}
}

func TestPromptPreviewFailsOnOrphanFragment(t *testing.T) {
	projectDir := setupMinimalAteamProject(t)
	// A role pre fragment with a typo'd role name and no matching
	// <role>.prompt.md anywhere — an orphan the preview must reject.
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
	promptPreview = true

	err := runPromptPreview()
	if err == nil || !strings.Contains(err.Error(), "orphan") {
		t.Fatalf("expected orphan-fragment error, got %v", err)
	}
}

func TestPromptPreviewBadAction(t *testing.T) {
	t.Cleanup(resetPromptFlags)
	promptRole = "security"
	promptAction = "nonsense"
	promptPreview = true
	_, _, err := promptPathForCurrentFlags()
	if err == nil || !strings.Contains(err.Error(), "invalid action") {
		t.Fatalf("expected invalid-action error, got %v", err)
	}
}

func TestPromptPathForCurrentFlags(t *testing.T) {
	cases := []struct {
		name              string
		role, action      string
		supervisor        bool
		wantPath, wantLbl string
	}{
		{"role report", "security", "report", false, "report/security", "role security"},
		{"role code", "auth.refactor", "code", false, "code/auth.refactor", "role auth.refactor"},
		{"sup review", "", "review", true, "review", "the supervisor"},
		{"sup code", "", "code", true, "code_management", "the supervisor"},
		{"sup verify", "", "verify", true, "code_verify", "the supervisor"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(resetPromptFlags)
			promptRole = tc.role
			promptAction = tc.action
			promptSupervisor = tc.supervisor
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
	promptSupervisor = false
	promptPreview = false
	promptPreviewContent = false
	promptExtraPrompt = ""
	promptNoProjectInfo = false
	promptIgnorePreviousReport = false
	promptFilesOnly = false
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
