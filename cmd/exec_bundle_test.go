package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/flow"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
)

func TestStaticBundle_Shape(t *testing.T) {
	opts := runner.RunOpts{RoleID: "tester", Action: runner.ActionExec, WorkDir: "/wd"}
	b := staticBundle("exec", "tester", runner.ActionExec, "hello", opts)

	if b.Name != "exec" || b.Role != "tester" || b.Action != runner.ActionExec {
		t.Errorf("identity fields wrong: %+v", b)
	}

	got, err := b.Prompt.Resolve(nil)
	if err != nil {
		t.Fatalf("Prompt.Resolve: %v", err)
	}
	if got != "hello" {
		t.Errorf("Prompt.Resolve: got %q want hello", got)
	}

	gotOpts := b.RunOpts(flow.RuntimeEnv{})
	if gotOpts.RoleID != opts.RoleID || gotOpts.WorkDir != opts.WorkDir {
		t.Errorf("RunOpts: got %+v want %+v", gotOpts, opts)
	}
}

func TestStaticBundle_PromptIsRawText(t *testing.T) {
	// Demonstrates the helper's "static" semantics: Prompt.Resolve returns
	// the captured prompt with no further expansion or env dependence.
	b := staticBundle("x", "r", "exec", "captured", runner.RunOpts{})
	got, err := b.Prompt.Resolve(nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "captured" {
		t.Errorf("got %q want captured", got)
	}
}

func TestBuildRunner_ScratchModeDefaultProfile(t *testing.T) {
	// With no project context and neither --profile nor --agent set,
	// scratch mode falls back to profile="default". The Agent ends up
	// being the agent the "default" profile resolves to.
	base := t.TempDir()
	orgDir, err := root.InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}
	env := &root.ResolvedEnv{OrgDir: orgDir}

	r, err := buildRunner(env, RunnerSpec{Action: runner.ActionExec})
	if err != nil {
		t.Fatalf("buildRunner: %v", err)
	}
	if r == nil || r.Agent == nil {
		t.Fatal("expected non-nil runner with resolved Agent")
	}
}

func TestBuildRunner_ScratchModeExplicitAgent(t *testing.T) {
	base := t.TempDir()
	orgDir, err := root.InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}
	env := &root.ResolvedEnv{OrgDir: orgDir}

	r, err := buildRunner(env, RunnerSpec{
		Agent:  "mock",
		Action: runner.ActionExec,
	})
	if err != nil {
		t.Fatalf("buildRunner: %v", err)
	}
	if r == nil || r.Agent == nil {
		t.Fatal("expected non-nil runner with Agent")
	}
	if got := r.Agent.Name(); got != "mock" {
		t.Errorf("Agent.Name(): got %q want mock", got)
	}
}

func TestBuildRunner_ProjectModeWithProfile(t *testing.T) {
	base := t.TempDir()
	orgDir, err := root.InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}
	projPath := filepath.Join(base, "myproj")
	if err := os.MkdirAll(projPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := root.InitProject(projPath, orgDir, root.InitProjectOpts{
		Name:         "myproj",
		EnabledRoles: []string{"testing_basic"},
	}); err != nil {
		t.Fatalf("InitProject: %v", err)
	}

	env, err := root.Resolve(filepath.Dir(orgDir), projPath)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	r, err := buildRunner(env, RunnerSpec{
		Profile: "default",
		Action:  runner.ActionExec,
	})
	if err != nil {
		t.Fatalf("buildRunner: %v", err)
	}
	if r == nil {
		t.Fatal("expected non-nil runner")
	}
}

func TestBuildRunner_ConflictingProfileAndAgent(t *testing.T) {
	base := t.TempDir()
	orgDir, err := root.InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}
	env := &root.ResolvedEnv{OrgDir: orgDir}

	// --profile + --agent is a user-facing error.
	_, err = buildRunner(env, RunnerSpec{
		Profile: "default",
		Agent:   "mock",
		Action:  runner.ActionExec,
	})
	if err == nil {
		t.Fatal("expected error from conflicting --profile + --agent")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error message: got %q want substring 'mutually exclusive'", err)
	}
}
