package agent

import (
	"context"
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
		budget string
		want   []string
	}{
		{"no overrides", "", "", "", []string{"-p", "--verbose"}},
		{"model only", "opus", "", "", []string{"-p", "--verbose", "--model", "opus"}},
		{"effort only", "", "high", "", []string{"-p", "--verbose", "--effort", "high"}},
		{"budget only", "", "", "5", []string{"-p", "--verbose", "--max-budget-usd", "5"}},
		{"all three", "opus", "high", "10.5", []string{"-p", "--verbose", "--model", "opus", "--effort", "high", "--max-budget-usd", "10.5"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a.Model = tt.model
			a.Effort = tt.effort
			a.MaxBudgetUSD = tt.budget
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

// TestClaudeRunEmitsToolResultForUserEventBlocks covers the "user" case
// in ClaudeAgent.run: a content block with a tool_use_id produces a
// "tool_result" StreamEvent, and a block without tool_use_id is dropped.
// This guards the stall-watchdog heartbeat for modern Claude CLI runs
// where tool results arrive nested inside user events.
func TestClaudeRunEmitsToolResultForUserEventBlocks(t *testing.T) {
	script := `
printf '{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_kept","content":"kept-result"}]}}\n'
printf '{"type":"user","message":{"role":"user","content":[{"type":"text","text":"dropped-because-no-tool-use-id"}]}}\n'
exit 0
`
	a := &ClaudeAgent{Command: "claude", DefaultModel: "claude-test-model"}

	ch := a.Run(context.Background(), Request{
		Prompt:     "test",
		CmdFactory: shellFactory(script),
	})

	var toolResults []StreamEvent
	for ev := range ch {
		if ev.Type == "tool_result" {
			toolResults = append(toolResults, ev)
		}
	}

	if len(toolResults) != 1 {
		t.Fatalf("expected exactly 1 tool_result event, got %d: %+v", len(toolResults), toolResults)
	}
	if toolResults[0].ToolResult != "kept-result" {
		t.Errorf("ToolResult = %q, want %q", toolResults[0].ToolResult, "kept-result")
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
