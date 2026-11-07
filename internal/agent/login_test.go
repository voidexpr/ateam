package agent

import "testing"

func TestPerAgentTypeLoginFacts(t *testing.T) {
	cases := []struct {
		agentType, envVar, command string
		login                      []string
	}{
		{"", "CLAUDE_CONFIG_DIR", "claude", []string{"auth", "login"}},
		{NameClaude, "CLAUDE_CONFIG_DIR", "claude", []string{"auth", "login"}},
		{NameCodex, "CODEX_HOME", "codex", []string{"login"}},
		{NameCodexTmux, "CODEX_HOME", "codex", []string{"login"}},
		{NameMock, "CLAUDE_CONFIG_DIR", "", nil},
		{"builtin", "", "", nil},
	}
	for _, c := range cases {
		if got := ConfigDirEnv(c.agentType); got != c.envVar {
			t.Errorf("ConfigDirEnv(%q) = %q, want %q", c.agentType, got, c.envVar)
		}
		if got := DefaultCommand(c.agentType); got != c.command {
			t.Errorf("DefaultCommand(%q) = %q, want %q", c.agentType, got, c.command)
		}
		got := LoginArgs(c.agentType)
		if len(got) != len(c.login) {
			t.Errorf("LoginArgs(%q) = %v, want %v", c.agentType, got, c.login)
			continue
		}
		for i := range got {
			if got[i] != c.login[i] {
				t.Errorf("LoginArgs(%q) = %v, want %v", c.agentType, got, c.login)
			}
		}
		if (AuthStatusArgs(c.agentType) == nil) != (c.login == nil) {
			t.Errorf("AuthStatusArgs(%q) presence must match LoginArgs", c.agentType)
		}
		if (AuthEnvVars(c.agentType) == nil) != (c.login == nil) {
			t.Errorf("AuthEnvVars(%q) presence must match LoginArgs", c.agentType)
		}
	}
}
