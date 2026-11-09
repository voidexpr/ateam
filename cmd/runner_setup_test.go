package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/container"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runtime"
)

// TestRejectCodexTmuxWithoutStateDir covers the guard that prevents
// resolveRunnerMinimal from constructing a codex-tmux runner when neither
// .ateam/ nor .ateamorg/ is resolved. With a state dir present (project or
// org), codex-tmux is allowed and its socket lives under <stateDir>/cache/tmux/.
func TestRejectCodexTmuxWithoutStateDir(t *testing.T) {
	cases := []struct {
		name      string
		typ       string
		stateDir  string
		wantError bool
	}{
		{"codex-tmux is rejected without state dir", agent.NameCodexTmux, "", true},
		{"codex-tmux is allowed with org state dir", agent.NameCodexTmux, "/tmp/org", false},
		{"claude is allowed without state dir", agent.NameClaude, "", false},
		{"codex is allowed without state dir", agent.NameCodex, "", false},
		{"empty type is allowed", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ac := &runtime.AgentConfig{Type: tc.typ}
			err := rejectCodexTmuxWithoutStateDir(ac, tc.stateDir)
			if (err != nil) != tc.wantError {
				t.Fatalf("err=%v want error=%v", err, tc.wantError)
			}
			if tc.wantError && !strings.Contains(err.Error(), "state directory") {
				t.Errorf("error message missing 'state directory': %v", err)
			}
		})
	}
}

// TestCodexTmuxRejectedWithNonNoneContainer covers the host-only constraint
// in buildRunnerFromBlocks: codex-tmux must not be bound to a container type
// other than "none". This is a different code path and a different error
// message than TestRejectCodexTmuxWithoutProject (which guards the
// no-project-context case).
func TestCodexTmuxRejectedWithNonNoneContainer(t *testing.T) {
	cases := []struct {
		name          string
		containerType string
	}{
		{"docker", "docker"},
		{"docker-exec", "docker-exec"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			projectDir := filepath.Join(dir, "project", ".ateam")
			if err := os.MkdirAll(projectDir, 0755); err != nil {
				t.Fatal(err)
			}
			profileName := "codex-tmux-on-" + tc.containerType
			hcl := `
container "ct-test" {
  type             = "` + tc.containerType + `"
  docker_container = "user-managed"
}

profile "` + profileName + `" {
  agent     = "codex-tmux"
  container = "ct-test"
}
`
			if err := os.WriteFile(filepath.Join(projectDir, root.ConfigFile), []byte(hcl), 0644); err != nil {
				t.Fatal(err)
			}

			env := &root.ResolvedEnv{
				ProjectDir: projectDir,
				SourceDir:  filepath.Dir(projectDir),
				WorkDir:    filepath.Dir(projectDir),
			}

			_, _, err := newRunner(env, profileName, "", false)
			if err == nil {
				t.Fatalf("expected host-only rejection error, got nil")
			}
			msg := err.Error()
			if !strings.Contains(msg, "codex-tmux") {
				t.Errorf("error message missing 'codex-tmux': %v", err)
			}
			if !strings.Contains(msg, tc.containerType) {
				t.Errorf("error message missing container type %q: %v", tc.containerType, err)
			}
		})
	}
}

// TestCodexTmuxAllowedWithNoneContainer is the happy-path companion to
// TestCodexTmuxRejectedWithNonNoneContainer: codex-tmux bound to a "none"
// container must pass the host-only gate in buildRunnerFromBlocks.
func TestCodexTmuxAllowedWithNoneContainer(t *testing.T) {
	dir := t.TempDir()
	projectDir := filepath.Join(dir, "project", ".ateam")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}

	env := &root.ResolvedEnv{
		ProjectDir: projectDir,
		SourceDir:  filepath.Dir(projectDir),
		WorkDir:    filepath.Dir(projectDir),
	}

	// The embedded defaults define profile "codex-tmux" with container "none".
	r, _, err := newRunner(env, "codex-tmux", "", false)
	if err != nil {
		t.Fatalf("newRunner: unexpected error: %v", err)
	}
	if r == nil {
		t.Fatal("newRunner returned nil runner")
	}
	if r.Agent == nil || r.Agent.Name() != agent.NameCodexTmux {
		t.Errorf("unexpected agent: got %v, want %s", r.Agent, agent.NameCodexTmux)
	}
	if r.ContainerType != "none" {
		t.Errorf("ContainerType = %q, want %q", r.ContainerType, "none")
	}
	if a, ok := r.Agent.(*agent.CodexTmuxAgent); !ok || a.Model != "gpt-6.1-sol" || a.Effort != "xhigh" {
		t.Errorf("profile pins not applied: got %+v, want model gpt-6.1-sol effort xhigh", r.Agent)
	}
}

// TestScratchRunnerAppliesProfilePins covers resolveRunnerMinimal (no
// project): the profile's model/budget pins reach the agent.
func TestScratchRunnerAppliesProfilePins(t *testing.T) {
	env := &root.ResolvedEnv{HomeDir: t.TempDir()}
	r, _, err := resolveRunnerMinimal(env, "cheap", "")
	if err != nil {
		t.Fatal(err)
	}
	a, ok := r.Agent.(*agent.ClaudeAgent)
	if !ok {
		t.Fatalf("agent = %T, want *agent.ClaudeAgent", r.Agent)
	}
	if a.Model != "sonnet" || a.MaxBudgetUSD != "0.50" {
		t.Errorf("model=%q budget=%q, want sonnet / 0.50", a.Model, a.MaxBudgetUSD)
	}
}

// TestScratchRunnerAppliesContextBlock covers resolveRunnerMinimal (no
// project): codex on the host gets its outside_container sandbox args,
// through both --profile and --agent.
func TestScratchRunnerAppliesContextBlock(t *testing.T) {
	if container.IsInContainer() {
		t.Skip("running inside a container: the inside_container block applies")
	}
	env := &root.ResolvedEnv{HomeDir: t.TempDir()}
	for _, tc := range []struct{ profile, agentName string }{{"codex", ""}, {"", "codex"}} {
		r, _, err := resolveRunnerMinimal(env, tc.profile, tc.agentName)
		if err != nil {
			t.Fatal(err)
		}
		a, ok := r.Agent.(*agent.CodexAgent)
		if !ok {
			t.Fatalf("agent = %T, want *agent.CodexAgent", r.Agent)
		}
		args := strings.Join(a.Args, " ")
		if !strings.Contains(args, "--sandbox workspace-write --skip-git-repo-check") {
			t.Errorf("profile=%q agent=%q: args %q missing outside_container args", tc.profile, tc.agentName, args)
		}
	}
}

// TestRequiredEnvPolicyMatrix covers how newRunner treats a missing
// required_env: ignored on the host with container "none", a warning when
// ateam itself runs in a container with "none", a hard error when the
// profile launches a container.
func TestRequiredEnvPolicyMatrix(t *testing.T) {
	const hcl = `
project {
  name = "myproj"
}

agent "envtest" {
  base         = "claude"
  required_env = ["ATEAM_TEST_REQ_A|ATEAM_TEST_REQ_B"]
}

container "ct-docker" {
  type = "docker"
}

profile "envtest-none" {
  agent     = "envtest"
  container = "none"
}

profile "envtest-docker" {
  agent     = "envtest"
  container = "ct-docker"
}
`
	cases := []struct {
		name        string
		profile     string
		inContainer bool
		wantErr     bool
		wantWarning bool
	}{
		{"host with none container", "envtest-none", false, false, false},
		{"in container with none container", "envtest-none", true, false, true},
		{"docker container", "envtest-docker", false, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, k := range []string{"ATEAM_TEST_REQ_A", "ATEAM_TEST_REQ_B"} {
				t.Setenv(k, "")
				os.Unsetenv(k) // a set-but-empty var counts as present
			}
			t.Setenv("ATEAM_IN_SANDBOX", "")
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			if tc.inContainer {
				t.Setenv("ATEAM_IN_CONTAINER", "1")
				if agent.HasLocalAgentAuth(agent.NameClaude) {
					t.Skip("local claude auth found: the in-container warning is suppressed")
				}
			} else {
				t.Setenv("ATEAM_IN_CONTAINER", "")
				if container.IsInContainer() {
					t.Skip("running inside a container: the host case does not apply")
				}
			}

			_, projPath, _ := setupFixture(t)
			projFile := filepath.Join(projPath, ".ateam", root.ConfigFile)
			if err := os.WriteFile(projFile, []byte(hcl), 0644); err != nil {
				t.Fatal(err)
			}
			env, err := root.LookupFrom(projPath)
			if err != nil {
				t.Fatal(err)
			}

			stderr := captureStderr(t, func() { _, _, err = newRunner(env, tc.profile, "", false) })

			if tc.wantErr {
				if err == nil {
					t.Fatal("want missing-secret error, got nil")
				}
				if !strings.Contains(err.Error(), "ATEAM_TEST_REQ_A or ATEAM_TEST_REQ_B") {
					t.Errorf("error does not name both alternatives: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("newRunner: %v", err)
			}
			hasWarning := strings.Contains(stderr, "Warning: Missing required secret: ATEAM_TEST_REQ_A or ATEAM_TEST_REQ_B")
			if hasWarning != tc.wantWarning {
				t.Errorf("warning emitted=%v want=%v (stderr=%q)", hasWarning, tc.wantWarning, stderr)
			}
			if !tc.wantWarning && strings.Contains(stderr, "Warning:") {
				t.Errorf("unexpected warning: %q", stderr)
			}
		})
	}
}
