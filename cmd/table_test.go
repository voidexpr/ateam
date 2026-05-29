package cmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
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

func TestApplyMaxBudgetUSD(t *testing.T) {
	tests := []struct {
		name      string
		agent     agent.Agent
		value     string
		action    string
		wantErr   bool
		wantStore string
	}{
		{"empty value is no-op", &agent.ClaudeAgent{}, "", runner.ActionExec, false, ""},
		{"claude exec stores value", &agent.ClaudeAgent{}, "5", runner.ActionExec, false, "5"},
		{"claude code stores value", &agent.ClaudeAgent{}, "10.5", runner.ActionCode, false, "10.5"},
		{"codex parallel warns but ok", &agent.CodexAgent{}, "5", runner.ActionParallel, false, "5"},
		{"codex report warns but ok", &agent.CodexAgent{}, "5", runner.ActionReport, false, "5"},
		{"codex exec errors", &agent.CodexAgent{}, "5", runner.ActionExec, true, "5"},
		{"codex review errors", &agent.CodexAgent{}, "5", runner.ActionReview, true, "5"},
		{"codex code errors", &agent.CodexAgent{}, "5", runner.ActionCode, true, "5"},
		{"codex verify errors", &agent.CodexAgent{}, "5", runner.ActionVerify, true, "5"},
		{"codex-tmux review errors", &agent.CodexTmuxAgent{}, "5", runner.ActionReview, true, "5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &runner.AgentExecutor{Agent: tt.agent}
			err := applyMaxBudgetUSD(r, tt.value, tt.action)
			if (err != nil) != tt.wantErr {
				t.Errorf("err=%v wantErr=%v", err, tt.wantErr)
			}
			switch a := tt.agent.(type) {
			case *agent.ClaudeAgent:
				if a.MaxBudgetUSD != tt.wantStore {
					t.Errorf("claude.MaxBudgetUSD = %q, want %q", a.MaxBudgetUSD, tt.wantStore)
				}
			case *agent.CodexAgent:
				if a.MaxBudgetUSD != tt.wantStore {
					t.Errorf("codex.MaxBudgetUSD = %q, want %q", a.MaxBudgetUSD, tt.wantStore)
				}
			case *agent.CodexTmuxAgent:
				if a.MaxBudgetUSD != tt.wantStore {
					t.Errorf("codex-tmux.MaxBudgetUSD = %q, want %q", a.MaxBudgetUSD, tt.wantStore)
				}
			}
		})
	}
}

func TestApplyModelAndEffort(t *testing.T) {
	t.Run("empty model is no-op on claude", func(t *testing.T) {
		a := &agent.ClaudeAgent{Model: "preset"}
		r := &runner.AgentExecutor{Agent: a}
		applyModel(r, "")
		if a.Model != "preset" {
			t.Errorf("Model = %q, want unchanged %q", a.Model, "preset")
		}
	})

	t.Run("empty effort is no-op on codex", func(t *testing.T) {
		a := &agent.CodexAgent{Effort: "preset"}
		r := &runner.AgentExecutor{Agent: a}
		applyEffort(r, "")
		if a.Effort != "preset" {
			t.Errorf("Effort = %q, want unchanged %q", a.Effort, "preset")
		}
	})

	t.Run("non-empty model populates ClaudeAgent.Model", func(t *testing.T) {
		a := &agent.ClaudeAgent{}
		r := &runner.AgentExecutor{Agent: a}
		applyModel(r, "opus-4")
		if a.Model != "opus-4" {
			t.Errorf("ClaudeAgent.Model = %q, want %q", a.Model, "opus-4")
		}
	})

	t.Run("non-empty model populates CodexAgent.Model", func(t *testing.T) {
		a := &agent.CodexAgent{}
		r := &runner.AgentExecutor{Agent: a}
		applyModel(r, "gpt-5")
		if a.Model != "gpt-5" {
			t.Errorf("CodexAgent.Model = %q, want %q", a.Model, "gpt-5")
		}
	})

	t.Run("non-empty effort populates ClaudeAgent.Effort", func(t *testing.T) {
		a := &agent.ClaudeAgent{}
		r := &runner.AgentExecutor{Agent: a}
		applyEffort(r, "high")
		if a.Effort != "high" {
			t.Errorf("ClaudeAgent.Effort = %q, want %q", a.Effort, "high")
		}
	})

	t.Run("non-empty effort populates CodexAgent.Effort", func(t *testing.T) {
		a := &agent.CodexAgent{}
		r := &runner.AgentExecutor{Agent: a}
		applyEffort(r, "medium")
		if a.Effort != "medium" {
			t.Errorf("CodexAgent.Effort = %q, want %q", a.Effort, "medium")
		}
	})
}

func TestParseBudgetUSD(t *testing.T) {
	tests := []struct {
		in      string
		wantSet bool
		wantVal float64
		wantErr bool
	}{
		{"", false, 0, false},
		{"0", true, 0, false},
		{"5", true, 5, false},
		{"10.5", true, 10.5, false},
		{"-1", false, 0, true},
		{"abc", false, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			v, set, err := parseBudgetUSD(tt.in)
			if (err != nil) != tt.wantErr {
				t.Errorf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if set != tt.wantSet {
				t.Errorf("set=%v want=%v", set, tt.wantSet)
			}
			if v != tt.wantVal && !tt.wantErr {
				t.Errorf("v=%v want=%v", v, tt.wantVal)
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

// TestApplyModelOverrides locks in the precedence rule between --cheaper-model
// and --model: if --model is set it always wins, --cheaper-model alone falls
// back to the cheaper model name, and the helper never touches AgentExecutor.ExtraArgs
// (the previous encoding through ExtraArgs caused dry-run output to disagree
// with what actually ran).
func TestApplyModelOverrides(t *testing.T) {
	tests := []struct {
		name        string
		cheaper     bool
		model       string
		wantModel   string
		wantWarning bool
	}{
		{"both unset is no-op", false, "", "", false},
		{"cheaper only sets cheaper model", true, "", cheaperModelName, false},
		{"model only sets explicit model", false, "opus-4", "opus-4", false},
		{"both set: model wins with warning", true, "opus-4", "opus-4", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &agent.ClaudeAgent{}
			r := &runner.AgentExecutor{
				Agent:     a,
				ExtraArgs: []string{"--existing", "arg"},
			}
			origExtraArgs := append([]string(nil), r.ExtraArgs...)

			stderr := captureStderr(t, func() {
				applyModelOverrides(r, tt.cheaper, tt.model)
			})

			if a.Model != tt.wantModel {
				t.Errorf("agent.Model = %q, want %q", a.Model, tt.wantModel)
			}
			// ExtraArgs must be untouched: the helper sets the agent's model
			// directly, no longer encoding it as a CLI arg.
			if !equalStrings(r.ExtraArgs, origExtraArgs) {
				t.Errorf("ExtraArgs mutated: got %v, want %v", r.ExtraArgs, origExtraArgs)
			}
			hasWarning := strings.Contains(stderr, "Warning:") &&
				strings.Contains(stderr, "--model") &&
				strings.Contains(stderr, "--cheaper-model")
			if hasWarning != tt.wantWarning {
				t.Errorf("warning emitted=%v want=%v (stderr=%q)", hasWarning, tt.wantWarning, stderr)
			}
		})
	}
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
			if err := os.WriteFile(filepath.Join(projectDir, "runtime.hcl"), []byte(hcl), 0644); err != nil {
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
