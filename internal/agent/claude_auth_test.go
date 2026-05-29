package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/secret"
)

const refreshTokenEnv = "CLAUDE_CODE_OAUTH_REFRESH_TOKEN"

// envValue returns the value for key in an os.Environ()-style slice.
func envValue(env []string, key string) (string, bool) {
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		if k == key {
			return v, true
		}
	}
	return "", false
}

func TestParseAuthMethodValid(t *testing.T) {
	tests := []struct {
		input string
		want  AuthMethod
	}{
		{"oauth", AuthOAuth},
		{"api", AuthAPI},
		{"regular", AuthRegular},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, ok := ParseAuthMethod(tt.input)
			if !ok {
				t.Fatalf("ParseAuthMethod(%q) returned ok=false", tt.input)
			}
			if got != tt.want {
				t.Errorf("ParseAuthMethod(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseAuthMethodInvalid(t *testing.T) {
	for _, input := range []string{"", "none", "OAuth", "API", "unknown", "apikey"} {
		t.Run(input, func(t *testing.T) {
			_, ok := ParseAuthMethod(input)
			if ok {
				t.Errorf("ParseAuthMethod(%q) should return ok=false", input)
			}
		})
	}
}

func TestValidateTargetOAuth(t *testing.T) {
	// Missing OAuth token
	msg := ValidateTarget(AuthOAuth, AuthStatus{HasOAuth: false})
	if msg == "" {
		t.Error("expected error for oauth without token")
	}
	// Has OAuth token
	msg = ValidateTarget(AuthOAuth, AuthStatus{HasOAuth: true})
	if msg != "" {
		t.Errorf("unexpected error: %s", msg)
	}
}

func TestValidateTargetAPI(t *testing.T) {
	// Missing API key
	msg := ValidateTarget(AuthAPI, AuthStatus{HasAPIKey: false})
	if msg == "" {
		t.Error("expected error for api without key")
	}
	// Has API key
	msg = ValidateTarget(AuthAPI, AuthStatus{HasAPIKey: true})
	if msg != "" {
		t.Errorf("unexpected error: %s", msg)
	}
}

func TestValidateTargetRegular(t *testing.T) {
	// Regular always passes (no env var required)
	msg := ValidateTarget(AuthRegular, AuthStatus{})
	if msg != "" {
		t.Errorf("regular should always validate, got: %s", msg)
	}
}

func TestExtractRefreshTokenValid(t *testing.T) {
	dir := t.TempDir()
	creds := `{"claudeAiOauth":{"refreshToken":"rt-abc123"}}`
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(creds), 0600); err != nil {
		t.Fatal(err)
	}

	got := ExtractRefreshToken(dir)
	if got != "rt-abc123" {
		t.Errorf("ExtractRefreshToken = %q, want %q", got, "rt-abc123")
	}
}

func TestExtractRefreshTokenMissingFile(t *testing.T) {
	dir := t.TempDir()
	got := ExtractRefreshToken(dir)
	if got != "" {
		t.Errorf("expected empty for missing file, got %q", got)
	}
}

func TestExtractRefreshTokenInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte("not json"), 0600); err != nil {
		t.Fatal(err)
	}

	got := ExtractRefreshToken(dir)
	if got != "" {
		t.Errorf("expected empty for invalid JSON, got %q", got)
	}
}

func TestExtractRefreshTokenNoRefreshField(t *testing.T) {
	dir := t.TempDir()
	creds := `{"claudeAiOauth":{"accessToken":"at-xyz"}}`
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(creds), 0600); err != nil {
		t.Fatal(err)
	}

	got := ExtractRefreshToken(dir)
	if got != "" {
		t.Errorf("expected empty when refreshToken missing, got %q", got)
	}
}

func TestExtractRefreshTokenEmptyObject(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}

	got := ExtractRefreshToken(dir)
	if got != "" {
		t.Errorf("expected empty for empty object, got %q", got)
	}
}

func TestEnsureClaudeStateCreates(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), "cfg")

	res := EnsureClaudeState(configDir, false)
	if res.Action != "done" {
		t.Fatalf("EnsureClaudeState action = %q, want done", res.Action)
	}

	data, err := os.ReadFile(filepath.Join(configDir, ".claude.json"))
	if err != nil {
		t.Fatalf("reading .claude.json: %v", err)
	}
	if !strings.Contains(string(data), "firstStartTime") {
		t.Errorf(".claude.json missing firstStartTime: %s", data)
	}
}

func TestEnsureClaudeStateDryRun(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), "cfg")

	res := EnsureClaudeState(configDir, true)
	if res.Action != "would" {
		t.Errorf("dryRun action = %q, want would", res.Action)
	}
	if _, err := os.Stat(filepath.Join(configDir, ".claude.json")); !os.IsNotExist(err) {
		t.Errorf("dryRun should not create .claude.json")
	}
}

func TestEnsureClaudeStateExisting(t *testing.T) {
	configDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(configDir, ".claude.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}

	res := EnsureClaudeState(configDir, false)
	if res.Action != "skip" {
		t.Errorf("existing state action = %q, want skip", res.Action)
	}
}

// TestCleanupDryRun verifies the file enumeration / preserve logic without
// mutating anything. Cleanup is tested in dryRun mode only: a real run on
// darwin would delete the user's "Claude Code-credentials" keychain entry.
func TestCleanupDryRun(t *testing.T) {
	configDir := t.TempDir()
	files := []string{".claude.json", "settings.json", ".credentials.json", "history.log", "stray"}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(configDir, f), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(configDir, "cache"), 0755); err != nil {
		t.Fatal(err)
	}

	actions := map[string]string{}
	for _, r := range Cleanup(configDir, false, true) {
		actions[r.Description] = r.Action
	}

	// Preserved entries are skipped before reaching the result list.
	for _, preserved := range []string{".claude.json", "settings.json", ".credentials.json"} {
		if _, ok := actions[preserved]; ok {
			t.Errorf("%q should be preserved (absent from results), got action %q", preserved, actions[preserved])
		}
	}
	// Non-preserved entries are reported as "would" in dryRun.
	for _, removed := range []string{"history.log", "stray", "cache/"} {
		if actions[removed] != "would" {
			t.Errorf("%q action = %q, want would", removed, actions[removed])
		}
	}
}

func TestCleanupWipeAllDryRun(t *testing.T) {
	configDir := t.TempDir()
	for _, f := range []string{".claude.json", "settings.json"} {
		if err := os.WriteFile(filepath.Join(configDir, f), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}

	actions := map[string]string{}
	for _, r := range Cleanup(configDir, true, true) {
		actions[r.Description] = r.Action
	}

	// settings.json is preserved even with wipeAll.
	if _, ok := actions["settings.json"]; ok {
		t.Errorf("settings.json should always be preserved, got %q", actions["settings.json"])
	}
	// .claude.json is no longer preserved under wipeAll.
	if actions[".claude.json"] != "would" {
		t.Errorf(".claude.json action = %q, want would (wipeAll)", actions[".claude.json"])
	}
}

func TestCleanupNonexistentDir(t *testing.T) {
	results := Cleanup(filepath.Join(t.TempDir(), "does-not-exist"), false, false)
	if len(results) != 0 {
		t.Errorf("expected no results for missing dir, got %v", results)
	}
}

func TestBuildCleanEnvExcludesConflictingVars(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "tok-test")

	tests := []struct {
		target  AuthMethod
		absent  []string
		present []string
	}{
		{AuthOAuth, []string{"ANTHROPIC_API_KEY"}, []string{"CLAUDE_CODE_OAUTH_TOKEN"}},
		{AuthAPI, []string{"CLAUDE_CODE_OAUTH_TOKEN"}, []string{"ANTHROPIC_API_KEY"}},
		{AuthRegular, []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"}, nil},
	}
	for _, tt := range tests {
		t.Run(string(tt.target), func(t *testing.T) {
			env := BuildCleanEnv(tt.target, "")
			for _, k := range tt.absent {
				if _, ok := envValue(env, k); ok {
					t.Errorf("%s should be excluded for %s", k, tt.target)
				}
			}
			for _, k := range tt.present {
				if _, ok := envValue(env, k); !ok {
					t.Errorf("%s should be retained for %s", k, tt.target)
				}
			}
		})
	}
}

func TestBuildCleanEnvAppendsRefreshToken(t *testing.T) {
	env := BuildCleanEnv(AuthOAuth, "rt-token-123")

	if v, ok := envValue(env, refreshTokenEnv); !ok || v != "rt-token-123" {
		t.Errorf("%s = %q (ok=%v), want %q", refreshTokenEnv, v, ok, "rt-token-123")
	}
	if v, ok := envValue(env, "CLAUDE_CODE_OAUTH_SCOPES"); !ok || v != "user:profile user:inference" {
		t.Errorf("CLAUDE_CODE_OAUTH_SCOPES = %q (ok=%v), want scopes", v, ok)
	}
}

func TestBuildCleanEnvNoRefreshTokenWhenEmpty(t *testing.T) {
	env := BuildCleanEnv(AuthOAuth, "")
	if _, ok := envValue(env, refreshTokenEnv); ok {
		t.Errorf("%s should be absent when no refresh token is supplied", refreshTokenEnv)
	}
}

func TestResolveRefreshTokenFromEnv(t *testing.T) {
	// Env var short-circuits before any store/keychain lookup.
	t.Setenv(refreshTokenEnv, "rt-from-env")
	if got := ResolveRefreshToken(t.TempDir(), "", ""); got != "rt-from-env" {
		t.Errorf("ResolveRefreshToken = %q, want %q", got, "rt-from-env")
	}
}

func TestResolveRefreshTokenFromCredFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // isolate the global secrets.env source
	t.Setenv(refreshTokenEnv, "") // env source absent
	skipIfGlobalRefreshToken(t)

	configDir := t.TempDir()
	creds := `{"claudeAiOauth":{"refreshToken":"rt-from-cred"}}`
	if err := os.WriteFile(filepath.Join(configDir, ".credentials.json"), []byte(creds), 0600); err != nil {
		t.Fatal(err)
	}

	if got := ResolveRefreshToken(configDir, "", ""); got != "rt-from-cred" {
		t.Errorf("ResolveRefreshToken = %q, want %q", got, "rt-from-cred")
	}
}

func TestResolveRefreshTokenAbsent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(refreshTokenEnv, "")
	skipIfGlobalRefreshToken(t)

	if got := ResolveRefreshToken(t.TempDir(), "", ""); got != "" {
		t.Errorf("ResolveRefreshToken = %q, want empty", got)
	}
}

// skipIfGlobalRefreshToken skips when the host has a global-scoped refresh
// token in the keychain or global secrets.env, which would otherwise shadow
// the source under test.
func skipIfGlobalRefreshToken(t *testing.T) {
	t.Helper()
	r := secret.NewResolver("", "", secret.DefaultBackend(), nil).Resolve(refreshTokenEnv)
	if r.Found && r.Source != "env" {
		t.Skipf("host has a global refresh token (%s/%s); skipping", r.Source, r.Backend)
	}
}

func TestCredFileHasTokensWithContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "creds.json")
	if err := os.WriteFile(path, []byte(`{"claudeAiOauth":{"accessToken":"tok"}}`), 0600); err != nil {
		t.Fatal(err)
	}

	if !credFileHasTokens(path) {
		t.Error("expected true for file with content")
	}
}

func TestCredFileHasTokensEmptyObject(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "creds.json")
	if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}

	if credFileHasTokens(path) {
		t.Error("expected false for empty JSON object")
	}
}

func TestCredFileHasTokensEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "creds.json")
	if err := os.WriteFile(path, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}

	if credFileHasTokens(path) {
		t.Error("expected false for empty file")
	}
}

func TestCredFileHasTokensMissingFile(t *testing.T) {
	if credFileHasTokens("/nonexistent/path/creds.json") {
		t.Error("expected false for missing file")
	}
}

func TestCredFileHasTokensInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "creds.json")
	if err := os.WriteFile(path, []byte("not json {"), 0600); err != nil {
		t.Fatal(err)
	}

	if credFileHasTokens(path) {
		t.Error("expected false for invalid JSON")
	}
}
