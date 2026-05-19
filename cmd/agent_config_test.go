package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/secret"
)

func TestValidateLocalPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("cannot get home dir: %v", err)
	}

	t.Run("home dir rejected", func(t *testing.T) {
		if err := validateLocalPath(home); err == nil {
			t.Error("expected error for home directory, got nil")
		}
	})

	t.Run("dot-claude rejected", func(t *testing.T) {
		claudeDir := filepath.Join(home, ".claude")
		if err := validateLocalPath(claudeDir); err == nil {
			t.Error("expected error for ~/.claude, got nil")
		}
	})

	t.Run("ordinary project path accepted", func(t *testing.T) {
		dir := t.TempDir()
		if err := validateLocalPath(dir); err != nil {
			t.Errorf("expected nil for ordinary path, got: %v", err)
		}
	})

	t.Run("symlink to home rejected", func(t *testing.T) {
		dir := t.TempDir()
		link := filepath.Join(dir, "homelink")
		if err := os.Symlink(home, link); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		if err := validateLocalPath(link); err == nil {
			t.Error("expected error for symlink pointing to home, got nil")
		}
	})
}

// TestRunAgentConfigAudit walks the read-only audit path with empty project
// and org dirs. The function emits a header line, an auth-sources block, and
// either a claude CLI status or a "could not run" notice — none of which
// require Docker or real credentials.
func TestRunAgentConfigAudit(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")

	var runErr error
	out := captureStdout(t, func() {
		runErr = runAgentConfigAudit("", "")
	})
	if runErr != nil {
		t.Fatalf("runAgentConfigAudit: %v", runErr)
	}
	for _, want := range []string{
		"Claude Code Agent Configuration Audit",
		"Config dir:",
		"Active auth:",
		"ANTHROPIC_API_KEY:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in audit output:\n%s", want, out)
		}
	}
}

// TestPrintAuthSourcesMasksPlainOAuthToken pins the secret-masking behavior
// added in commit c8922bb. Without these assertions a refactor of
// printAuthSources can silently print live tokens into bug reports / CI logs.
func TestPrintAuthSourcesMasksPlainOAuthToken(t *testing.T) {
	t.Run("plain CLAUDE_CODE_OAUTH_TOKEN is masked", func(t *testing.T) {
		const tok = "sk-live-abc123"
		t.Setenv("ANTHROPIC_API_KEY", "")
		t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", tok)

		out := captureStdout(t, func() { printAuthSources(agent.AuthStatus{}) })

		if strings.Contains(out, tok) {
			t.Errorf("raw OAuth token leaked into output:\n%s", out)
		}
		if !strings.Contains(out, secret.MaskValue(tok)) {
			t.Errorf("masked OAuth token %q missing from output:\n%s",
				secret.MaskValue(tok), out)
		}
	})

	t.Run("JSON-shaped CLAUDE_CODE_OAUTH_TOKEN does not leak", func(t *testing.T) {
		const tok = `{"accessToken":"sk-live-jsonpayload-987654321"}`
		t.Setenv("ANTHROPIC_API_KEY", "")
		t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", tok)

		out := captureStdout(t, func() { printAuthSources(agent.AuthStatus{}) })

		if strings.Contains(out, "sk-live-jsonpayload") {
			t.Errorf("raw JSON OAuth payload leaked into output:\n%s", out)
		}
		if !strings.Contains(out, "JSON,") {
			t.Errorf("JSON-token branch missing 'JSON,' summary:\n%s", out)
		}
	})

	t.Run("ANTHROPIC_API_KEY is masked", func(t *testing.T) {
		const key = "sk-ant-api-abc123xyz"
		t.Setenv("ANTHROPIC_API_KEY", key)
		t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")

		out := captureStdout(t, func() { printAuthSources(agent.AuthStatus{}) })

		if strings.Contains(out, key) {
			t.Errorf("raw API key leaked into output:\n%s", out)
		}
		if !strings.Contains(out, secret.MaskValue(key)) {
			t.Errorf("masked API key %q missing from output:\n%s",
				secret.MaskValue(key), out)
		}
	})
}
