package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/spf13/cobra"
)

// filepathEqual compares paths after symlink resolution so macOS
// /var/folders vs /private/var/folders doesn't cause spurious diffs.
func filepathEqual(a, b string) bool {
	ra, _ := filepath.EvalSymlinks(a)
	rb, _ := filepath.EvalSymlinks(b)
	return ra == rb
}

func newDummyCmd(name string) *cobra.Command {
	return &cobra.Command{Use: name}
}

// TestApplyRunnerOverridesEmpty verifies that an empty RunnerOverrides leaves
// the agent's tunables untouched and only marks ContainerNameSource so the
// dry-run display has a value to render.
func TestApplyRunnerOverridesEmpty(t *testing.T) {
	a := &agent.ClaudeAgent{}
	r := &runner.Runner{Agent: a}

	if err := applyRunnerOverrides(r, &root.ResolvedEnv{}, RunnerOverrides{}, runner.ActionExec); err != nil {
		t.Fatalf("applyRunnerOverrides: %v", err)
	}

	if a.Model != "" {
		t.Errorf("Model = %q, want empty", a.Model)
	}
	if a.Effort != "" {
		t.Errorf("Effort = %q, want empty", a.Effort)
	}
	if a.MaxBudgetUSD != "" {
		t.Errorf("MaxBudgetUSD = %q, want empty", a.MaxBudgetUSD)
	}
	if r.ContainerNameSource != runner.ContainerNameSourceConfig {
		t.Errorf("ContainerNameSource = %q, want %q", r.ContainerNameSource, runner.ContainerNameSourceConfig)
	}
}

// TestApplyRunnerOverridesFull verifies that a fully-populated RunnerOverrides
// flows every field through to the agent and runner. CheaperModel is
// intentionally false so Model wins straightforwardly.
func TestApplyRunnerOverridesFull(t *testing.T) {
	a := &agent.ClaudeAgent{}
	r := &runner.Runner{
		Agent:         a,
		ContainerName: "config-container",
	}

	o := RunnerOverrides{
		ContainerName:     "cli-container",
		Model:             "opus-4",
		Effort:            "high",
		MaxBudgetUSD:      "10",
		MaxBudgetUSDBatch: "50",
	}
	if err := applyRunnerOverrides(r, &root.ResolvedEnv{}, o, runner.ActionExec); err != nil {
		t.Fatalf("applyRunnerOverrides: %v", err)
	}

	if a.Model != "opus-4" {
		t.Errorf("Model = %q, want opus-4", a.Model)
	}
	if a.Effort != "high" {
		t.Errorf("Effort = %q, want high", a.Effort)
	}
	if a.MaxBudgetUSD != "10" {
		t.Errorf("MaxBudgetUSD = %q, want 10", a.MaxBudgetUSD)
	}
	if r.ContainerNameSource != runner.ContainerNameSourceCLI {
		t.Errorf("ContainerNameSource = %q, want %q", r.ContainerNameSource, runner.ContainerNameSourceCLI)
	}
}

// TestApplyRunnerOverridesCheaperModel exercises the --cheaper-model branch via
// the helper to make sure it routes through applyModelOverrides (and not the
// older applyModel that ignored --cheaper-model).
func TestApplyRunnerOverridesCheaperModel(t *testing.T) {
	a := &agent.ClaudeAgent{}
	r := &runner.Runner{Agent: a}

	o := RunnerOverrides{CheaperModel: true}
	if err := applyRunnerOverrides(r, &root.ResolvedEnv{}, o, runner.ActionExec); err != nil {
		t.Fatalf("applyRunnerOverrides: %v", err)
	}
	if a.Model != cheaperModelName {
		t.Errorf("Model = %q, want %q", a.Model, cheaperModelName)
	}
}

// TestApplyRunnerOverridesMaxBudgetError verifies the helper propagates the
// codex-on-single-exec error from applyMaxBudgetUSD instead of swallowing it.
func TestApplyRunnerOverridesMaxBudgetError(t *testing.T) {
	r := &runner.Runner{Agent: &agent.CodexAgent{}}
	o := RunnerOverrides{MaxBudgetUSD: "5"}
	if err := applyRunnerOverrides(r, &root.ResolvedEnv{}, o, runner.ActionExec); err == nil {
		t.Fatal("expected error when codex action=exec receives --max-budget-usd, got nil")
	}
}

// TestRequireGitRepoPreRunE_NonRepo verifies the PreRunE rejects work-dirs
// that aren't inside a git repo.
func TestRequireGitRepoPreRunE_NonRepo(t *testing.T) {
	saved := workDirFlag
	t.Cleanup(func() { workDirFlag = saved })

	workDirFlag = t.TempDir() // empty dir, not a git repo

	dummy := newDummyCmd("report")
	err := requireGitRepoPreRunE(dummy, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "report") {
		t.Errorf("error %q should mention command name", err)
	}
	if !strings.Contains(err.Error(), "git repo") {
		t.Errorf("error %q should mention git repo requirement", err)
	}
}

// TestRequireGitRepoPreRunE_InRepo verifies the PreRunE allows real git repos.
func TestRequireGitRepoPreRunE_InRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git CLI required")
	}
	saved := workDirFlag
	t.Cleanup(func() { workDirFlag = saved })

	tmp := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		c := exec.Command("git", args...)
		c.Dir = tmp
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	workDirFlag = tmp

	dummy := newDummyCmd("report")
	if err := requireGitRepoPreRunE(dummy, nil); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestResolveEnvAppliesWorkDirFlag verifies that the persistent --work-dir flag
// is applied BEFORE any cmd code reads env. This is the regression the four
// review findings caught: prompt/sandbox/container all need env.WorkDir to be
// correct at the point they consume env, not after each cmd's local override.
func TestResolveEnvAppliesWorkDirFlag(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git CLI required")
	}
	savedOrg, savedProj, savedWD := orgFlag, projectFlag, workDirFlag
	t.Cleanup(func() {
		orgFlag, projectFlag, workDirFlag = savedOrg, savedProj, savedWD
	})

	// Layout: tmp/project/.ateam + tmp/worktree (a real git repo)
	tmp := t.TempDir()
	projectRoot := filepath.Join(tmp, "project")
	ateam := filepath.Join(projectRoot, ".ateam")
	if err := os.MkdirAll(ateam, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ateam, "config.toml"), []byte("[project]\nname=\"p\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(tmp, "worktree")
	if err := os.MkdirAll(worktree, 0755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		c := exec.Command("git", args...)
		c.Dir = worktree
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	orgFlag = ""
	projectFlag = projectRoot
	workDirFlag = worktree

	env, err := resolveEnv()
	if err != nil {
		t.Fatalf("resolveEnv: %v", err)
	}
	if env.ProjectDir != ateam && !filepathEqual(env.ProjectDir, ateam) {
		t.Errorf("ProjectDir = %q, want %q", env.ProjectDir, ateam)
	}
	if env.WorkDir != worktree && !filepathEqual(env.WorkDir, worktree) {
		t.Errorf("WorkDir = %q, want %q (--work-dir applied)", env.WorkDir, worktree)
	}
	if !filepathEqual(env.GitRepoDir, worktree) {
		t.Errorf("GitRepoDir = %q, want %q (derived from WorkDir, not from project parent)", env.GitRepoDir, worktree)
	}
}
