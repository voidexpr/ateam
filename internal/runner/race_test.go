package runner

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/container"
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

// =============================================================================
// REGRESSION: RunPool goroutines shared r.Container, whose ResolveTemplates
// mutates ExtraArgs/ExtraVolumes/Env in place. Concurrent writes to the same
// slice indices + map reassignment produce heap corruption, surfacing as
// SIGSEGV in later allocations (observed in the wild inside strings.Replacer
// trie build).
//
// File: internal/container/docker.go, func (*DockerContainer).ResolveTemplates
// Call site: internal/runner/runner.go, inside Run() via resolveContainerTemplates
//
// Run with: go test -race ./internal/runner -run TestRunPoolSharedContainerRace
// =============================================================================

func TestRunPoolSharedContainerRace(t *testing.T) {
	dir := t.TempDir()

	// Delay keeps each goroutine alive past template resolution so the first
	// batch of maxParallel workers overlap. Race detector catches unsynchronized
	// access regardless of exact overlap, but this widens the window.
	mock := &agent.MockAgent{Response: "ok", Delay: 10 * time.Millisecond}

	// Shared container with multiple templated entries — more slice indices =
	// more collision surface for concurrent writes.
	dc := &container.DockerContainer{
		ExtraArgs:    []string{"--hostname", "ateam-{{ROLE}}", "--label", "exec={{EXEC_ID}}"},
		ExtraVolumes: []string{"/data/{{ROLE}}:/data", "/cache/{{ROLE}}:/cache"},
		Env:          map[string]string{"ROLE": "{{ROLE}}", "SESSION": "{{EXEC_ID}}"},
		// Dockerfile is intentionally empty: setupContainer.Prepare will fail,
		// but the race in resolveContainerTemplates runs earlier in Run().
	}

	r := &Runner{
		Agent:         mock,
		Container:     dc,
		ContainerType: "docker",
		ProjectName:   "test",
		SourceDir:     dir,
	}

	const numTasks = 8
	tasks := make([]PoolTask, numTasks)
	for i := range tasks {
		tasks[i] = PoolTask{
			Prompt: "p",
			RunOpts: RunOpts{
				RoleID:  fmt.Sprintf("role-%d", i),
				Action:  ActionRun,
				LogsDir: makeTaskLogsDir(dir, i),
			},
		}
	}

	// We don't assert on results — each task is expected to fail at Prepare
	// (no Dockerfile). We only care that -race reports no DATA RACE.
	_ = RunPool(context.Background(), r, tasks, 3, nil, nil)
}

func TestRunPoolSharedContainerDoesNotMutateTemplate(t *testing.T) {
	// Independently of concurrency, Runner.Run should not leave the shared
	// container's templated fields resolved — subsequent runs for different
	// roles need the placeholders intact.
	dir := t.TempDir()

	mock := &agent.MockAgent{Response: "ok"}
	dc := &container.DockerContainer{
		ExtraArgs:    []string{"--hostname", "ateam-{{ROLE}}"},
		ExtraVolumes: []string{"/data/{{ROLE}}:/data"},
		Env:          map[string]string{"ROLE": "{{ROLE}}"},
	}
	r := &Runner{
		Agent:         mock,
		Container:     dc,
		ContainerType: "docker",
		ProjectName:   "test",
		SourceDir:     dir,
	}

	tasks := []PoolTask{
		{Prompt: "p", RunOpts: RunOpts{RoleID: "alpha", Action: ActionRun, LogsDir: makeTaskLogsDir(dir, 0)}},
		{Prompt: "p", RunOpts: RunOpts{RoleID: "beta", Action: ActionRun, LogsDir: makeTaskLogsDir(dir, 1)}},
	}
	_ = RunPool(context.Background(), r, tasks, 1, nil, nil) // serial: isolate mutation issue from the race

	if dc.ExtraArgs[1] != "ateam-{{ROLE}}" {
		t.Errorf("shared container ExtraArgs[1] mutated: got %q, want %q", dc.ExtraArgs[1], "ateam-{{ROLE}}")
	}
	if dc.ExtraVolumes[0] != "/data/{{ROLE}}:/data" {
		t.Errorf("shared container ExtraVolumes[0] mutated: got %q, want %q", dc.ExtraVolumes[0], "/data/{{ROLE}}:/data")
	}
	if dc.Env["ROLE"] != "{{ROLE}}" {
		t.Errorf("shared container Env[ROLE] mutated: got %q, want %q", dc.Env["ROLE"], "{{ROLE}}")
	}
}
