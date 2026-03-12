package agent

import (
	"testing"
)

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
