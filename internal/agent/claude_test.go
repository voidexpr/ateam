package agent

import (
	"slices"
	"testing"
)

func TestClaudeAgentDebugCommandArgs(t *testing.T) {
	a := &ClaudeAgent{
		Command: "claude",
		Args:    []string{"-p", "--verbose"},
	}
	tests := []struct {
		name   string
		model  string
		effort string
		want   []string
	}{
		{"no overrides", "", "", []string{"-p", "--verbose"}},
		{"model only", "opus", "", []string{"-p", "--verbose", "--model", "opus"}},
		{"effort only", "", "high", []string{"-p", "--verbose", "--effort", "high"}},
		{"both", "opus", "high", []string{"-p", "--verbose", "--model", "opus", "--effort", "high"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a.Model = tt.model
			a.Effort = tt.effort
			_, args := a.DebugCommandArgs(nil)
			if !slices.Equal(args, tt.want) {
				t.Errorf("args = %v, want %v", args, tt.want)
			}
		})
	}
}

func TestResolveConfigDir(t *testing.T) {
	tests := []struct {
		name     string
		agentEnv map[string]string
		reqEnv   map[string]string
		want     string
	}{
		{"empty", nil, nil, ""},
		{"agent env", map[string]string{"CLAUDE_CONFIG_DIR": "/a/.claude"}, nil, "/a/.claude"},
		{"req env", nil, map[string]string{"CLAUDE_CONFIG_DIR": "/b/.claude"}, "/b/.claude"},
		{"req overrides agent", map[string]string{"CLAUDE_CONFIG_DIR": "/a"}, map[string]string{"CLAUDE_CONFIG_DIR": "/b"}, "/b"},
		{"empty string excluded", map[string]string{"CLAUDE_CONFIG_DIR": ""}, nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveConfigDir(tt.agentEnv, tt.reqEnv)
			if got != tt.want {
				t.Errorf("resolveConfigDir() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHasSettingsArg(t *testing.T) {
	if hasSettingsArg(nil) {
		t.Error("expected false for nil args")
	}
	if hasSettingsArg([]string{"-p", "--verbose"}) {
		t.Error("expected false for args without --settings")
	}
	if !hasSettingsArg([]string{"-p", "--settings", "/tmp/s.json"}) {
		t.Error("expected true for args with --settings")
	}
}
