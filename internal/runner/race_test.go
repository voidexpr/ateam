package runner

import (
	"sync"
	"testing"

	"github.com/ateam/internal/agent"
)

// =============================================================================
// REGRESSION: ResolveAgentTemplateArgs used to mutate shared Agent.Args without synchronization
// File: template.go, func ResolveAgentTemplateArgs
// Called from: runner.go:201 inside Run()
//
// RunPool calls r.Run() in parallel goroutines sharing the same Runner (and
// thus the same r.Agent). ResolveAgentTemplateArgs writes to agent.Args
// (t.Args = ResolveTemplateArgs(t.Args, vars)), creating a data race when
// multiple goroutines resolve templates concurrently.
//
// Run with: go test -race ./internal/runner/ -run TestResolveAgentTemplateArgsConcurrentRace
// =============================================================================

func TestResolveAgentTemplateArgsConcurrentRace(t *testing.T) {
	// Shared agent — same instance used by all goroutines, mimicking RunPool.
	a := &agent.ClaudeAgent{
		Command: "claude",
		Args:    []string{"-p", "--name", "{{PROJECT_DIR}}-{{ROLE}}", "--output-format", "stream-json"},
	}

	// Save original args for verification.
	originalArgs := make([]string, len(a.Args))
	copy(originalArgs, a.Args)

	var wg sync.WaitGroup
	const goroutines = 20

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			vars := TemplateVars{
				ProjectDir: "project",
				Role:       "security",
			}
			resolved := ResolveAgentTemplateArgs(a, vars)
			clone, ok := resolved.(*agent.ClaudeAgent)
			if !ok {
				t.Errorf("resolved agent type = %T, want *agent.ClaudeAgent", resolved)
				return
			}
			if clone.Args[2] != "project-security" {
				t.Errorf("resolved args[%d] = %q, want %q", 2, clone.Args[2], "project-security")
			}
		}(i)
	}

	wg.Wait()

	if a.Args[2] != originalArgs[2] {
		t.Logf("After concurrent ResolveAgentTemplateArgs, agent.Args = %v", a.Args)
		t.Logf("Original agent.Args = %v", originalArgs)
		t.Errorf("ResolveAgentTemplateArgs mutated the shared agent's Args — templates should remain unresolved on the shared agent")
	}
}

func TestResolveAgentTemplateArgsDoesNotMutateSharedAgent(t *testing.T) {
	// Even without concurrency, the mutation is a problem:
	// After resolving for role "security", a second resolve for role "testing"
	// sees already-resolved args and produces wrong results.

	a := &agent.ClaudeAgent{
		Command: "claude",
		Args:    []string{"--name", "{{ROLE}}-agent"},
	}

	// First resolve: security
	first := ResolveAgentTemplateArgs(a, TemplateVars{Role: "security"})
	firstAgent := first.(*agent.ClaudeAgent)
	if firstAgent.Args[1] != "security-agent" {
		t.Fatalf("first resolve: got %q, want %q", firstAgent.Args[1], "security-agent")
	}

	// Second resolve with different role: should get "testing-agent"
	second := ResolveAgentTemplateArgs(a, TemplateVars{Role: "testing"})
	secondAgent := second.(*agent.ClaudeAgent)

	if secondAgent.Args[1] != "testing-agent" {
		t.Errorf("second resolve: got %q, want %q", secondAgent.Args[1], "testing-agent")
	}
	if a.Args[1] != "{{ROLE}}-agent" {
		t.Errorf("shared agent args mutated: got %q, want %q", a.Args[1], "{{ROLE}}-agent")
	}
}
