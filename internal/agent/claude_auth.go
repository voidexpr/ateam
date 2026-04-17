package agent

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/ateam/internal/secret"
)

// AuthMethod represents a Claude Code auth method.
type AuthMethod string

const (
	AuthOAuth   AuthMethod = "oauth"
	AuthAPI     AuthMethod = "api"
	AuthRegular AuthMethod = "regular"
	AuthNone    AuthMethod = "none"
)

// ParseAuthMethod validates and returns an AuthMethod.
func ParseAuthMethod(s string) (AuthMethod, bool) {
	switch s {
	case "oauth":
		return AuthOAuth, true
	case "api":
		return AuthAPI, true
	case "regular":
		return AuthRegular, true
	default:
		return "", false
	}
}

// AuthSource describes a detected auth source.
type AuthSource struct {
	Name   string // "apikey-env", "oauth-env", "credentials-file", "macos-keychain"
	Detail string // masked value or description
}

// AuthStatus holds the full auth state detection result.
type AuthStatus struct {
	ConfigDir       string
	Active          AuthMethod
	Sources         []AuthSource
	HasAPIKey       bool
	HasOAuth        bool
	HasCredFile     bool
	HasKeychain     bool
	HasSecretAPI    bool   // ANTHROPIC_API_KEY saved via ateam secret
	HasSecretOAuth  bool   // CLAUDE_CODE_OAUTH_TOKEN saved via ateam secret
	SecretAPIInfo   string // e.g. "ateam secret (global/keychain)"
	SecretOAuthInfo string // e.g. "ateam secret (org/file)"

	// Refresh token sources (for bootstrapping interactive sessions)
	HasRefreshTokenEnv    bool   // CLAUDE_CODE_OAUTH_REFRESH_TOKEN in env
	HasRefreshTokenSecret bool   // saved via ateam secret
	HasRefreshTokenCred   bool   // extractable from .credentials.json
	RefreshTokenInfo      string // description of where it was found
}

// ClaudeConfigDir returns CLAUDE_CONFIG_DIR or $HOME/.claude.
func ClaudeConfigDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}

// DetectAuth scans env vars, ateam secret store, and config dir for all auth sources.
// projectDir and orgDir may be empty (detection works with global scope only).
func DetectAuth(projectDir, orgDir string) AuthStatus {
	configDir := ClaudeConfigDir()
	s := AuthStatus{ConfigDir: configDir}

	if val := os.Getenv("ANTHROPIC_API_KEY"); val != "" {
		s.HasAPIKey = true
		s.Sources = append(s.Sources, AuthSource{Name: "apikey-env", Detail: secret.MaskValue(val)})
	}

	if val := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); val != "" {
		s.HasOAuth = true
		if strings.HasPrefix(val, "{") {
			s.Sources = append(s.Sources, AuthSource{Name: "oauth-env", Detail: "JSON, " + itoa(len(val)) + " chars"})
		} else {
			s.Sources = append(s.Sources, AuthSource{Name: "oauth-env", Detail: secret.MaskValue(val)})
		}
	}

	// Check ateam secret store (skips env, which we already checked above).
	resolver := secret.NewResolver(projectDir, orgDir, secret.DefaultBackend(), nil)
	if !s.HasAPIKey {
		if r := resolver.Resolve("ANTHROPIC_API_KEY"); r.Found && r.Source != "env" {
			s.HasSecretAPI = true
			s.SecretAPIInfo = "ateam secret (" + r.Source + "/" + r.Backend + ")"
			s.HasAPIKey = true
			s.Sources = append(s.Sources, AuthSource{Name: "apikey-secret", Detail: s.SecretAPIInfo})
		}
	}
	if !s.HasOAuth {
		if r := resolver.Resolve("CLAUDE_CODE_OAUTH_TOKEN"); r.Found && r.Source != "env" {
			s.HasSecretOAuth = true
			s.SecretOAuthInfo = "ateam secret (" + r.Source + "/" + r.Backend + ")"
			s.HasOAuth = true
			s.Sources = append(s.Sources, AuthSource{Name: "oauth-secret", Detail: s.SecretOAuthInfo})
		}
	}

	credPath := filepath.Join(configDir, ".credentials.json")
	if credFileHasTokens(credPath) {
		s.HasCredFile = true
		s.Sources = append(s.Sources, AuthSource{Name: "credentials-file", Detail: credPath})
	}

	// Refresh token: check env, ateam secret, and .credentials.json
	if os.Getenv("CLAUDE_CODE_OAUTH_REFRESH_TOKEN") != "" {
		s.HasRefreshTokenEnv = true
		s.RefreshTokenInfo = "env"
		s.Sources = append(s.Sources, AuthSource{Name: "refresh-token-env", Detail: "set"})
	}
	if !s.HasRefreshTokenEnv {
		if r := resolver.Resolve("CLAUDE_CODE_OAUTH_REFRESH_TOKEN"); r.Found && r.Source != "env" {
			s.HasRefreshTokenSecret = true
			s.RefreshTokenInfo = "ateam secret (" + r.Source + "/" + r.Backend + ")"
			s.Sources = append(s.Sources, AuthSource{Name: "refresh-token-secret", Detail: s.RefreshTokenInfo})
		}
	}
	if !s.HasRefreshTokenEnv && !s.HasRefreshTokenSecret {
		if ExtractRefreshToken(configDir) != "" {
			s.HasRefreshTokenCred = true
			s.RefreshTokenInfo = ".credentials.json"
			s.Sources = append(s.Sources, AuthSource{Name: "refresh-token-cred", Detail: "extractable from .credentials.json"})
		}
	}

	if goruntime.GOOS == "darwin" && hasKeychainEntry() {
		s.HasKeychain = true
		s.Sources = append(s.Sources, AuthSource{Name: "macos-keychain", Detail: "Claude Code-credentials"})
	}

	// Active method follows Claude's priority order.
	// Ateam secrets are injected into env before agent launch, so they
	// count the same as env vars for priority purposes.
	switch {
	case s.HasAPIKey:
		s.Active = AuthAPI
	case s.HasOAuth:
		s.Active = AuthOAuth
	case s.HasCredFile || s.HasKeychain:
		s.Active = AuthRegular
	default:
		s.Active = AuthNone
	}

	return s
}

// ValidateTarget checks that the required env var for the target method
// is present (in env or ateam secret store). Returns "" if valid, or an error message.
func ValidateTarget(target AuthMethod, status AuthStatus) string {
	switch target {
	case AuthOAuth:
		if !status.HasOAuth {
			return "CLAUDE_CODE_OAUTH_TOKEN must be set for oauth method (env var or ateam secret)"
		}
	case AuthAPI:
		if !status.HasAPIKey {
			return "ANTHROPIC_API_KEY must be set for api method (env var or ateam secret)"
		}
	}
	return ""
}

// Conflicts returns warning strings about env vars that conflict
// with the target method.
func Conflicts(target AuthMethod) []string {
	var warnings []string
	switch target {
	case AuthOAuth:
		if os.Getenv("ANTHROPIC_API_KEY") != "" {
			warnings = append(warnings, "ANTHROPIC_API_KEY is set and takes priority over oauth — will be removed from exec env")
		}
	case AuthAPI:
		if os.Getenv("CLAUDE_CODE_OAUTH_TOKEN") != "" {
			warnings = append(warnings, "CLAUDE_CODE_OAUTH_TOKEN is set (lower priority than api key)")
		}
	case AuthRegular:
		if os.Getenv("ANTHROPIC_API_KEY") != "" {
			warnings = append(warnings, "ANTHROPIC_API_KEY is set and takes priority over interactive login — will be removed from exec env")
		}
		if os.Getenv("CLAUDE_CODE_OAUTH_TOKEN") != "" {
			warnings = append(warnings, "CLAUDE_CODE_OAUTH_TOKEN is set and takes priority over interactive login — will be removed from exec env")
		}
	}
	return warnings
}

// CleanupResult describes one cleanup action taken or skipped.
type CleanupResult struct {
	Description string
	Action      string // "done", "skip", "would"
}

// EnsureClaudeState creates configDir/.claude.json if it doesn't exist.
// Without this file Claude Code treats the install as fresh and prompts
// for auth method selection even when a token is in the environment.
func EnsureClaudeState(configDir string, dryRun bool) CleanupResult {
	statePath := filepath.Join(configDir, ".claude.json")
	if _, err := os.Stat(statePath); err == nil {
		return CleanupResult{".claude.json", "skip"}
	}
	if dryRun {
		return CleanupResult{".claude.json (create with firstStartTime)", "would"}
	}
	_ = os.MkdirAll(configDir, 0700)
	content := `{"firstStartTime":"` + time.Now().UTC().Format(time.RFC3339Nano) + `"}` + "\n"
	if err := os.WriteFile(statePath, []byte(content), 0600); err != nil {
		return CleanupResult{".claude.json (create failed: " + err.Error() + ")", "skip"}
	}
	return CleanupResult{".claude.json (created)", "done"}
}

// preserveEntries lists files/dirs in configDir to keep during cleanup.
// Everything else is removed to ensure a clean auth state.
var preserveEntries = map[string]bool{
	".claude.json":      true,
	".credentials.json": true,
	"settings.json":     true,
	"plugins":           true,
	"skills":            true,
}

// Cleanup removes everything from configDir except preserveEntries.
// If wipeAll is true, only settings.json is preserved.
// If dryRun is true, reports what would happen without making changes.
func Cleanup(configDir string, wipeAll, dryRun bool) []CleanupResult {
	var results []CleanupResult

	entries, err := os.ReadDir(configDir)
	if err != nil {
		if os.IsNotExist(err) {
			return results
		}
		results = append(results, CleanupResult{"read " + configDir + ": " + err.Error(), "skip"})
		return results
	}

	for _, e := range entries {
		name := e.Name()
		if !wipeAll && preserveEntries[name] {
			continue
		}
		// Always keep settings.json even with --wipe-config-clean.
		if name == "settings.json" {
			continue
		}
		path := filepath.Join(configDir, name)
		if e.IsDir() {
			results = append(results, removeDirAction(path, name+"/", dryRun))
		} else {
			results = append(results, removeFileAction(path, name, dryRun))
		}
	}

	if goruntime.GOOS == "darwin" {
		if hasKeychainEntry() {
			if dryRun {
				results = append(results, CleanupResult{"macOS Keychain (Claude Code-credentials)", "would"})
			} else {
				_ = exec.Command("security", "delete-generic-password", "-s", "Claude Code-credentials").Run()
				results = append(results, CleanupResult{"macOS Keychain (Claude Code-credentials)", "done"})
			}
		}
	}

	return results
}

// BuildCleanEnv returns os.Environ() filtered to remove env vars
// that conflict with the target method. If refreshToken is non-empty,
// it's added as CLAUDE_CODE_OAUTH_REFRESH_TOKEN with the required scopes.
func BuildCleanEnv(target AuthMethod, refreshToken string) []string {
	exclude := map[string]bool{}
	switch target {
	case AuthOAuth:
		exclude["ANTHROPIC_API_KEY"] = true
	case AuthAPI:
		exclude["CLAUDE_CODE_OAUTH_TOKEN"] = true
	case AuthRegular:
		exclude["ANTHROPIC_API_KEY"] = true
		exclude["CLAUDE_CODE_OAUTH_TOKEN"] = true
	}

	env := os.Environ()
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		key, _, _ := strings.Cut(e, "=")
		if !exclude[key] {
			filtered = append(filtered, e)
		}
	}

	if refreshToken != "" {
		filtered = append(filtered,
			"CLAUDE_CODE_OAUTH_REFRESH_TOKEN="+refreshToken,
			"CLAUDE_CODE_OAUTH_SCOPES=user:profile user:inference",
		)
	}

	return filtered
}

// HasRefreshToken returns true if a refresh token is available from any source.
func (s *AuthStatus) HasRefreshToken() bool {
	return s.HasRefreshTokenEnv || s.HasRefreshTokenSecret || s.HasRefreshTokenCred
}

// ExtractRefreshToken reads the refresh token from configDir/.credentials.json.
// Returns empty string if not found or not parseable.
func ExtractRefreshToken(configDir string) string {
	data, err := os.ReadFile(filepath.Join(configDir, ".credentials.json"))
	if err != nil {
		return ""
	}
	var creds struct {
		ClaudeAiOauth struct {
			RefreshToken string `json:"refreshToken"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(data, &creds); err != nil {
		return ""
	}
	return creds.ClaudeAiOauth.RefreshToken
}

// ResolveRefreshToken returns the refresh token from the best available source:
// env var > ateam secret > .credentials.json.
func ResolveRefreshToken(configDir, projectDir, orgDir string) string {
	if val := os.Getenv("CLAUDE_CODE_OAUTH_REFRESH_TOKEN"); val != "" {
		return val
	}
	resolver := secret.NewResolver(projectDir, orgDir, secret.DefaultBackend(), nil)
	if r := resolver.Resolve("CLAUDE_CODE_OAUTH_REFRESH_TOKEN"); r.Found && r.Source != "env" {
		return r.Value
	}
	return ExtractRefreshToken(configDir)
}

func credFileHasTokens(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return false
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return false
	}
	return len(m) > 0
}

func hasKeychainEntry() bool {
	return exec.Command("security", "find-generic-password", "-s", "Claude Code-credentials").Run() == nil
}

func removeFileAction(path, desc string, dryRun bool) CleanupResult {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return CleanupResult{desc, "skip"}
	}
	if dryRun {
		return CleanupResult{desc, "would"}
	}
	if err := os.Remove(path); err != nil {
		return CleanupResult{desc + " (remove failed: " + err.Error() + ")", "skip"}
	}
	return CleanupResult{desc, "done"}
}

func removeDirAction(path, desc string, dryRun bool) CleanupResult {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return CleanupResult{desc, "skip"}
	}
	if dryRun {
		return CleanupResult{desc, "would"}
	}
	if err := os.RemoveAll(path); err != nil {
		return CleanupResult{desc + " (remove failed: " + err.Error() + ")", "skip"}
	}
	return CleanupResult{desc, "done"}
}

func itoa(n int) string {
	// Avoid importing strconv for a single int conversion.
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
