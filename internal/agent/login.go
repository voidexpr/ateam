package agent

// Per-agent-type facts about the native CLI that ateam needs outside a run:
// where the CLI keeps its config and credentials, how to log in, and which
// env vars shadow a stored login. An empty agent type means claude (the
// default in runtime.hcl).

// ConfigDirEnv returns the environment variable through which the agent CLI
// takes its config directory; the runner sets it from `config_dir`. The
// mock agent stands in for claude in tests and maps the same way.
func ConfigDirEnv(agentType string) string {
	switch agentType {
	case NameClaude, NameMock, "":
		return "CLAUDE_CONFIG_DIR"
	case NameCodex, NameCodexTmux:
		return "CODEX_HOME"
	}
	return ""
}

// DefaultCommand returns the CLI binary for the agent type when the agent
// block sets no `command`.
func DefaultCommand(agentType string) string {
	switch agentType {
	case NameClaude, "":
		return "claude"
	case NameCodex, NameCodexTmux:
		return "codex"
	}
	return ""
}

// LoginArgs returns the agent CLI's interactive login subcommand, nil when
// the agent has none.
func LoginArgs(agentType string) []string {
	switch agentType {
	case NameClaude, "":
		return []string{"auth", "login"}
	case NameCodex, NameCodexTmux:
		return []string{"login"}
	}
	return nil
}

// AuthStatusArgs returns the subcommand that reports the CLI's login state.
func AuthStatusArgs(agentType string) []string {
	switch agentType {
	case NameClaude, "":
		return []string{"auth", "status"}
	case NameCodex, NameCodexTmux:
		return []string{"login", "status"}
	}
	return nil
}

// AuthEnvVars lists the credential env vars that take precedence over a
// login stored in the config dir. An interactive login strips them so the
// CLI writes credentials instead of reporting the env token as logged in.
func AuthEnvVars(agentType string) []string {
	switch agentType {
	case NameClaude, "":
		return []string{"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"}
	case NameCodex, NameCodexTmux:
		return []string{"OPENAI_API_KEY"}
	}
	return nil
}
