package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
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
	// Run the same stress test for each Agent implementation — each one
	// must produce fully-cloned Args slices under concurrent template
	// resolution.
	cases := []struct {
		name  string
		build func() agent.Agent
	}{
		{
			name: "claude",
			build: func() agent.Agent {
				return &agent.ClaudeAgent{
					Command: "claude",
					Args:    []string{"-p", "--name", "{{PROJECT_DIR}}-{{ROLE}}", "--output-format", "stream-json"},
				}
			},
		},
		{
			name: "codex",
			build: func() agent.Agent {
				return &agent.CodexAgent{
					Command: "codex",
					Args:    []string{"--name", "{{PROJECT_DIR}}-{{ROLE}}", "--sandbox", "workspace-write"},
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := tc.build()
			originalArgs := argsOf(a)
			snapshot := append([]string(nil), originalArgs...)

			var wg sync.WaitGroup
			const goroutines = 20
			for i := 0; i < goroutines; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					vars := TemplateVars{ProjectDir: "project", Role: "security"}
					resolved := ResolveAgentTemplateArgs(a, vars)
					resolvedArgs := argsOf(resolved)
					if !strings.Contains(strings.Join(resolvedArgs, " "), "project-security") {
						t.Errorf("resolved args missing expected substitution: %v", resolvedArgs)
					}
				}()
			}
			wg.Wait()

			for i, v := range argsOf(a) {
				if v != snapshot[i] {
					t.Errorf("shared agent Args[%d] mutated: got %q, want %q", i, v, snapshot[i])
				}
			}
		})
	}
}

// argsOf reads the Args slice of any supported Agent impl for assertion.
func argsOf(a agent.Agent) []string {
	switch v := a.(type) {
	case *agent.ClaudeAgent:
		return v.Args
	case *agent.CodexAgent:
		return v.Args
	default:
		return nil
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

// TestRunPoolCompletedChannelDeadlockGuard exercises the up-front refusal
// when the caller hands in an undersized completed channel — without the
// guard, workers would block on `completed <- summary` after maxParallel
// summaries queue up, wedging the pool.
func TestRunPoolCompletedChannelDeadlockGuard(t *testing.T) {
	dir := t.TempDir()
	mock := &agent.MockAgent{Response: "ok"}
	r := &Runner{Agent: mock}

	const numTasks = 4
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

	undersized := make(chan RunSummary, 1) // cap < numTasks
	results := RunPool(context.Background(), r, tasks, 2, nil, undersized)
	if results != nil {
		t.Errorf("RunPool should refuse an undersized completed channel and return nil; got %d results", len(results))
	}
}

// TestRunPoolSharedDockerExecRace mirrors TestRunPoolSharedContainerRace
// but targets DockerExecContainer, whose ResolveTemplates mutates
// ContainerName and WorkDir. The per-task Clone must isolate these writes.
func TestRunPoolSharedDockerExecRace(t *testing.T) {
	dir := t.TempDir()
	mock := &agent.MockAgent{Response: "ok", Delay: 10 * time.Millisecond}
	de := &container.DockerExecContainer{
		ContainerName: "ateam-{{ROLE}}",
		WorkDir:       "/work/{{ROLE}}",
		ForwardEnv:    []string{"PATH"},
		Env:           map[string]string{"ROLE": "{{ROLE}}"},
		// No container actually running — Prepare will fail at docker ps,
		// but ResolveTemplates and Clone paths run first.
	}
	r := &Runner{
		Agent:         mock,
		Container:     de,
		ContainerType: "docker-exec",
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
	_ = RunPool(context.Background(), r, tasks, 3, nil, nil)
}

// TestRunPoolRunnerFieldsUnchanged is a reflection-based guard against
// anyone reintroducing a write to a Runner field during Run. It snapshots
// scalar/string/slice/map fields before and after RunPool and asserts no
// change. Interface/pointer/channel/func fields are deliberately skipped
// — Agent/Container are exercised by their own clone-race tests, and
// comparing pointer identity here would be either trivially-true or
// trivially-false and wouldn't catch the kind of mutation we care about.
func TestRunPoolRunnerFieldsUnchanged(t *testing.T) {
	dir := t.TempDir()
	mock := &agent.MockAgent{Response: "ok", Delay: 5 * time.Millisecond}

	r := &Runner{
		Agent:                mock,
		ProjectName:          "proj",
		ProjectDir:           dir,
		SourceDir:            dir,
		OrgDir:               dir,
		Profile:              "default",
		ProjectID:            "proj-id",
		ContainerType:        "none",
		ContainerName:        "initial-name",
		ContainerNameSource:  ContainerNameSourceConfig,
		LogFile:              filepath.Join(dir, "runner.log"),
		ExtraArgs:            []string{"-p", "--output-format", "stream-json"},
		ArgsInsideContainer:  []string{"--inside"},
		ArgsOutsideContainer: []string{"--outside"},
	}
	r.Sandbox.RWPaths = []string{"/rw"}
	r.Sandbox.ROPaths = []string{"/ro"}
	r.Sandbox.Denied = []string{"/no"}

	before := snapshotRunnerFields(t, r)

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
	_ = RunPool(context.Background(), r, tasks, 3, nil, nil)

	after := snapshotRunnerFields(t, r)

	for name, beforeVal := range before {
		if after[name] != beforeVal {
			t.Errorf("Runner.%s mutated during RunPool: before=%q after=%q", name, beforeVal, after[name])
		}
	}
}

// snapshotRunnerFields hashes every scalar/string/slice/map field of a
// Runner for before/after comparison. Fields of kind Interface, Ptr,
// Chan, Func, and Struct are skipped (they're covered by dedicated clone
// tests or are pointers to shared thread-safe primitives).
func snapshotRunnerFields(t *testing.T, r *Runner) map[string]string {
	t.Helper()
	out := make(map[string]string)
	v := reflect.ValueOf(r).Elem()
	typ := v.Type()
	for i := 0; i < v.NumField(); i++ {
		f := typ.Field(i)
		fv := v.Field(i)
		switch fv.Kind() {
		case reflect.String,
			reflect.Bool,
			reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
			reflect.Float32, reflect.Float64,
			reflect.Slice, reflect.Map, reflect.Array:
			h := sha256.Sum256([]byte(fmt.Sprintf("%v", fv.Interface())))
			out[f.Name] = hex.EncodeToString(h[:8])
		default:
			// Interface, Ptr, Chan, Func, Struct — skip (see doc on caller).
		}
	}
	return out
}

// =============================================================================
// REGRESSION: Cloning the container gave each pool worker a fresh prepareOnce,
// so docker-exec configs with copy_ateam = true or a container-restarting
// precheck would fire their side effects N times per pool run instead of once.
// The fix is a shared PrepareGuard the clones all point at — this test uses a
// fake Container to verify the guard makes it through Clone().
// =============================================================================

type countingContainer struct {
	prepareCalls *atomic.Int64
	guard        *container.PrepareGuard
}

func (c *countingContainer) Type() string { return "docker" }
func (c *countingContainer) Run(context.Context, container.RunOpts) error {
	return nil
}
func (c *countingContainer) DebugCommand(container.RunOpts) string { return "" }
func (c *countingContainer) Prepare(ctx context.Context) error {
	return c.guard.Do(func() error {
		c.prepareCalls.Add(1)
		return nil
	})
}
func (c *countingContainer) CmdFactory() container.CmdFactory   { return nil }
func (c *countingContainer) GetContainerName() string           { return "" }
func (c *countingContainer) TranslatePath(p string) string      { return p }
func (c *countingContainer) ResolveTemplates(*strings.Replacer) {}
func (c *countingContainer) SetSourceWritable(bool)             {}
func (c *countingContainer) SetContainerName(string) bool       { return false }
func (c *countingContainer) ApplyAgentEnv(map[string]string)    {}
func (c *countingContainer) Clone() container.Container {
	cp := *c // shares prepareCalls + guard pointers — exactly what the real containers do
	return &cp
}

func TestRunPoolSharedPrepareGuardRunsOnce(t *testing.T) {
	dir := t.TempDir()

	var calls atomic.Int64
	fake := &countingContainer{
		prepareCalls: &calls,
		guard:        &container.PrepareGuard{},
	}

	mock := &agent.MockAgent{Response: "ok", Delay: 5 * time.Millisecond}
	r := &Runner{
		Agent:         mock,
		Container:     fake,
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
	_ = RunPool(context.Background(), r, tasks, 3, nil, nil)

	if got := calls.Load(); got != 1 {
		t.Errorf("Prepare ran %d times across %d pool workers, want 1 — shared guard is not propagating through Clone()", got, numTasks)
	}
}
