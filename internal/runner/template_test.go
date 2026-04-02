package runner

import (
	"testing"

	"github.com/ateam/internal/agent"
)

func TestResolveTemplateArgs(t *testing.T) {
	vars := TemplateVars{
		ProjectName:     "myproject",
		ProjectFullPath: "/home/user/projects/myproject",
		ProjectDir:      "myproject",
		Role:            "security",
		Action:          "report",
		TaskGroup:       "code-2026-03-31_06-09-39",
		Timestamp:       "2026-03-31_06-09-39",
		Profile:         "docker",
		ExecID:          42,
		Agent:           "claude-docker",
		Model:           "sonnet",
		Container:       "docker",
	}

	args := []string{
		"--name", "{{PROJECT_DIR}}-{{ROLE}}-{{ACTION}}",
		"--verbose",
		"{{PROJECT_NAME}}",
		"--session", "{{EXEC_ID}}-{{AGENT}}-{{MODEL}}",
	}

	got := ResolveTemplateArgs(args, vars)
	want := []string{
		"--name", "myproject-security-report",
		"--verbose",
		"myproject",
		"--session", "42-claude-docker-sonnet",
	}

	if len(got) != len(want) {
		t.Fatalf("len mismatch: got %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("arg[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestResolveTemplateArgsExecIDZero(t *testing.T) {
	vars := TemplateVars{ExecID: 0, Role: "security"}
	args := []string{"--name", "{{EXEC_ID}}-{{ROLE}}"}
	got := ResolveTemplateArgs(args, vars)

	if got[1] != "-security" {
		t.Errorf("expected '-security' for zero EXEC_ID, got %q", got[1])
	}
}

func TestResolveTemplateArgsNoTemplates(t *testing.T) {
	vars := TemplateVars{Role: "security"}
	args := []string{"-p", "--verbose", "--output-format", "stream-json"}
	got := ResolveTemplateArgs(args, vars)

	for i, arg := range args {
		if got[i] != arg {
			t.Errorf("arg[%d] changed unexpectedly: got %q, want %q", i, got[i], arg)
		}
	}
}

func TestResolveTemplateArgsUnknownVar(t *testing.T) {
	vars := TemplateVars{Role: "security"}
	args := []string{"--name", "{{UNKNOWN_VAR}}"}
	got := ResolveTemplateArgs(args, vars)

	if got[1] != "{{UNKNOWN_VAR}}" {
		t.Errorf("unknown var should be preserved: got %q", got[1])
	}
}

func TestResolveAgentTemplateArgs(t *testing.T) {
	a := &agent.ClaudeAgent{
		Command: "claude",
		Args:    []string{"-p", "--name", "{{PROJECT_DIR}}-{{ROLE}}"},
	}
	vars := TemplateVars{ProjectDir: "myapp", Role: "security"}

	resolved := resolveAgentTemplateArgs(a, vars)
	got, ok := resolved.(*agent.ClaudeAgent)
	if !ok {
		t.Fatalf("expected *agent.ClaudeAgent, got %T", resolved)
	}

	if got.Args[2] != "myapp-security" {
		t.Errorf("expected 'myapp-security', got %q", got.Args[2])
	}
	if a.Args[2] != "{{PROJECT_DIR}}-{{ROLE}}" {
		t.Errorf("expected original args to remain templated, got %q", a.Args[2])
	}
}
