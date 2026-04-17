package agent

import (
	"os"
	"path/filepath"
	"testing"
)

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
