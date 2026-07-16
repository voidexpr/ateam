package agent

import (
	"os"
	"path/filepath"
)

// CodexHome returns CODEX_HOME or $HOME/.codex.
func CodexHome() string {
	if d := os.Getenv("CODEX_HOME"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex")
}

// HasLocalCodexAuth reports whether Codex has usable auth configured on this
// machine independent of the process env — i.e. a populated auth.json under
// CODEX_HOME (~/.codex/ by default). Used to suppress required_env warnings
// when running inside a pre-configured container.
func HasLocalCodexAuth() bool {
	home := CodexHome()
	if home == "" {
		return false
	}
	return credFileHasTokens(filepath.Join(home, "auth.json"))
}

// HasLocalAgentAuth reports whether the given agent type has locally
// configured auth (credentials file / auth.json). Returns false for types
// with no known local-auth mechanism (mock, unknown).
func HasLocalAgentAuth(agentType string) bool {
	switch agentType {
	case NameClaude, "":
		return HasLocalClaudeAuth()
	case NameCodex, NameCodexTmux:
		return HasLocalCodexAuth()
	}
	return false
}
