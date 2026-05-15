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
	"github.com/ateam/internal/runtime"
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

// TestRequireGitRepo_EmptyGitRepoDirErrors checks the post-resolveEnv gate.
// requireGitRepo reads env.GitRepoDir, which resolveEnv populates from the
// final WorkDir — so it validates the path the runner will actually use,
// not whatever cwd was at PreRunE time (the previous bug).
func TestRequireGitRepo_EmptyGitRepoDirErrors(t *testing.T) {
	env := &root.ResolvedEnv{WorkDir: "/tmp/not-a-repo"}
	err := requireGitRepo(env, "report")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "report") {
		t.Errorf("error %q should mention action name", err)
	}
	if !strings.Contains(err.Error(), "git repo") {
		t.Errorf("error %q should mention git repo requirement", err)
	}
}

func TestRequireGitRepo_NonEmptyGitRepoDirPasses(t *testing.T) {
	env := &root.ResolvedEnv{
		WorkDir:    "/repo",
		GitRepoDir: "/repo",
	}
	if err := requireGitRepo(env, "report"); err != nil {
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

// TestPathInside covers the "is child inside parent" check used by
// applyWorkDirFlag to decide between "git-like (use project root)" and
// "remote (use cwd)" defaults.
func TestPathInside(t *testing.T) {
	cases := []struct {
		child, parent string
		want          bool
	}{
		{"/a/b", "/a/b", true},     // identical → inside
		{"/a/b/c", "/a/b", true},   // child path → inside
		{"/a/b/c/d", "/a/b", true}, // deeper child → inside
		{"/a", "/a/b", false},      // parent of "parent" → outside
		{"/a/x", "/a/b", false},    // sibling → outside
		{"/c/d", "/a/b", false},    // unrelated → outside
		{"/a/b..c", "/a/b", false}, // tricky: not actually a descendant
	}
	for _, c := range cases {
		got := pathInside(c.child, c.parent)
		if got != c.want {
			t.Errorf("pathInside(%q, %q) = %v, want %v", c.child, c.parent, got, c.want)
		}
	}
}

// TestApplyWorkDirFlag_GitLikeFromSubdir verifies that when cwd is inside the
// project tree and no --work-dir is set, env.WorkDir is promoted to the
// project root — pre-refactor behavior where `cd subdir && ateam report`
// operates on the whole project.
func TestApplyWorkDirFlag_GitLikeFromSubdir(t *testing.T) {
	saved := workDirFlag
	t.Cleanup(func() { workDirFlag = saved })
	workDirFlag = ""

	projectRoot := "/tmp/myproj"
	env := &root.ResolvedEnv{
		ProjectDir: filepath.Join(projectRoot, ".ateam"),
		WorkDir:    filepath.Join(projectRoot, "sub", "path"), // a subdir
	}
	got, err := applyWorkDirFlag(env)
	if err != nil {
		t.Fatalf("applyWorkDirFlag: %v", err)
	}
	if got.WorkDir != projectRoot {
		t.Errorf("WorkDir = %q, want %q (promoted to project root)", got.WorkDir, projectRoot)
	}
}

// TestApplyWorkDirFlag_RemoteKeepsCwd verifies that when cwd is outside the
// project tree (--project ../foo from elsewhere), env.WorkDir stays at cwd.
func TestApplyWorkDirFlag_RemoteKeepsCwd(t *testing.T) {
	saved := workDirFlag
	t.Cleanup(func() { workDirFlag = saved })
	workDirFlag = ""

	env := &root.ResolvedEnv{
		ProjectDir: "/tmp/myproj/.ateam",
		WorkDir:    "/tmp/elsewhere", // outside /tmp/myproj
	}
	got, err := applyWorkDirFlag(env)
	if err != nil {
		t.Fatalf("applyWorkDirFlag: %v", err)
	}
	if got.WorkDir != "/tmp/elsewhere" {
		t.Errorf("WorkDir = %q, want /tmp/elsewhere (cwd outside project tree)", got.WorkDir)
	}
}

// TestApplyWorkDirFlag_ExplicitFlagWins verifies that --work-dir is the
// authoritative override even when cwd happens to be inside the project tree.
func TestApplyWorkDirFlag_ExplicitFlagWins(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git CLI required (OverrideWorkDir invokes gitutil.TopLevel)")
	}
	saved := workDirFlag
	t.Cleanup(func() { workDirFlag = saved })

	override := t.TempDir()
	workDirFlag = override

	env := &root.ResolvedEnv{
		ProjectDir: "/tmp/myproj/.ateam",
		WorkDir:    "/tmp/myproj/subdir",
	}
	got, err := applyWorkDirFlag(env)
	if err != nil {
		t.Fatalf("applyWorkDirFlag: %v", err)
	}
	if !filepathEqual(got.WorkDir, override) {
		t.Errorf("WorkDir = %q, want %q (explicit --work-dir)", got.WorkDir, override)
	}
}

// TestPreflightContainerSupportsWorkDir guards the deliberate error path:
// container profiles + WorkDir outside the project tree is not yet supported,
// so we fail fast with an actionable message rather than silently produce
// a broken mount layout.
func TestPreflightContainerSupportsWorkDir(t *testing.T) {
	t.Run("none container always allowed", func(t *testing.T) {
		cc := &runtime.ContainerConfig{Type: "none"}
		env := &root.ResolvedEnv{
			ProjectDir: "/proj/.ateam",
			SourceDir:  "/proj",
			WorkDir:    "/elsewhere", // outside project tree
		}
		if err := preflightContainerSupportsWorkDir(cc, env); err != nil {
			t.Errorf("none container should never trip preflight, got: %v", err)
		}
	})

	t.Run("docker container with WorkDir inside project tree allowed", func(t *testing.T) {
		cc := &runtime.ContainerConfig{Type: "docker"}
		env := &root.ResolvedEnv{
			ProjectDir: "/proj/.ateam",
			SourceDir:  "/proj",
			WorkDir:    "/proj", // same as project root
		}
		if err := preflightContainerSupportsWorkDir(cc, env); err != nil {
			t.Errorf("docker + WorkDir==project root should be allowed, got: %v", err)
		}
	})

	t.Run("docker container with WorkDir outside project tree errors", func(t *testing.T) {
		cc := &runtime.ContainerConfig{Type: "docker"}
		env := &root.ResolvedEnv{
			ProjectDir: "/proj/.ateam",
			SourceDir:  "/proj",
			WorkDir:    "/elsewhere",
		}
		err := preflightContainerSupportsWorkDir(cc, env)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "container profile") {
			t.Errorf("error %q should mention container profile", err)
		}
		if !strings.Contains(err.Error(), "--work-dir") {
			t.Errorf("error %q should mention --work-dir", err)
		}
	})
}

// TestShellQuoteSingle covers POSIX shell single-quoting used to keep paths
// with spaces or shell-significant chars intact when they're embedded into
// supervisor prompts (cmd/code.go injects --project with this helper).
func TestShellQuoteSingle(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"simple", `'simple'`},
		{"with space", `'with space'`},
		{"with/slashes", `'with/slashes'`},
		{"", `''`},
		{`it's`, `'it'\''s'`},    // single quote escape
		{`a$b\c"d`, `'a$b\c"d'`}, // $ \ " all safe inside single quotes
		{"/Users/foo/My Project/proj", `'/Users/foo/My Project/proj'`},
	}
	for _, c := range cases {
		got := shellQuoteSingle(c.in)
		if got != c.want {
			t.Errorf("shellQuoteSingle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
