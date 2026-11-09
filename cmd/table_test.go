package cmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/container"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runtime"
)

func TestResolveVolumePath(t *testing.T) {
	tmpDir := t.TempDir()
	absInside := filepath.Join(tmpDir, "data") + ":/container"
	cases := []struct {
		name    string
		vol     string
		wantErr bool
	}{
		{
			name:    "relative path within sourceDir",
			vol:     "subdir/file:/container",
			wantErr: false,
		},
		{
			name:    "path traversal escapes boundary",
			vol:     "../../etc/passwd:/container",
			wantErr: true,
		},
		{
			name:    "absolute path inside allowed dir",
			vol:     absInside,
			wantErr: false,
		},
		{
			name:    "absolute path outside allowed dir",
			vol:     "/etc/passwd:/container",
			wantErr: true,
		},
		{
			name:    "single-part spec passes through",
			vol:     "hostpath",
			wantErr: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolveVolumePath(tc.vol, tmpDir, tmpDir)
			if tc.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// captureStderr swaps os.Stderr for a pipe while fn runs and returns whatever
// fn wrote. Mirrors captureStdout from exec_test.go for the cases where the
// code under test only writes to stderr (warnings, etc.).
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	old := os.Stderr
	os.Stderr = pw
	fn()
	pw.Close()
	os.Stderr = old
	var buf bytes.Buffer
	io.Copy(&buf, pr)
	return buf.String()
}

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
// at cmd/table.go:140-142: codex-tmux must not be bound to a container type
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

			_, err := newRunner(env, profileName, "", false)
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
// container must pass the host-only gate at cmd/table.go:140-142.
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
	r, err := newRunner(env, "codex-tmux", "", false)
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
	r, err := resolveRunnerMinimal(env, "cheap", "")
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
		r, err := resolveRunnerMinimal(env, tc.profile, tc.agentName)
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

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestPrintArtifact verifies --print reads the on-disk artifact rather than
// the agent's stream output, matching the shipped single-file prompts'
// "harness reads it directly, anything you stream as text is discarded"
// contract. Stream fallback fires when the file is missing/empty (the
// prompts' documented "Write failed → emit body" recovery path).
func TestPrintArtifact(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "review.md")
	if err := os.WriteFile(filePath, []byte("# Real Review\n\nP0: ship it\n"), 0644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing.md")

	cases := []struct {
		name    string
		path    string
		stream  string
		want    string
		wantNot string
	}{
		{
			name:    "file exists — prefers file over stream",
			path:    filePath,
			stream:  "Review written to runtime/...",
			want:    "P0: ship it",
			wantNot: "Review written to",
		},
		{
			name:   "file missing — falls back to stream",
			path:   missing,
			stream: "stream body recovery",
			want:   "stream body recovery",
		},
		{
			name: "both empty — quiet no-op",
			path: missing,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := captureStdout(t, func() {
				printArtifact(tc.path, tc.stream)
			})
			if tc.want != "" && !strings.Contains(out, tc.want) {
				t.Errorf("missing %q in output:\n%s", tc.want, out)
			}
			if tc.wantNot != "" && strings.Contains(out, tc.wantNot) {
				t.Errorf("unexpected %q in output:\n%s", tc.wantNot, out)
			}
			if tc.want == "" && tc.stream == "" && out != "" {
				t.Errorf("expected no output, got %q", out)
			}
		})
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

			stderr := captureStderr(t, func() { _, err = newRunner(env, tc.profile, "", false) })

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
