package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"
	"testing"

	"github.com/ateam/internal/container"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runtime"
)

// launchFixture gives a launch a scratch home, ateam home and working
// folder, and returns the working folder.
func launchFixture(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv(runtime.HomeEnv, filepath.Join(home, ".ateam"))
	work := filepath.Join(home, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(work)
	return work
}

func TestLaunchCommand(t *testing.T) {
	saved := &runtime.ProfileConfig{Name: "build", Command: []string{"make", "test"}}
	bare := &runtime.ProfileConfig{Name: "bare"}
	ac := &runtime.AgentConfig{Name: "claude", Type: "claude"}

	got, err := launchCommand(modeRun, saved, ac, []string{"-run", "X"})
	if err != nil || !slices.Equal(got, []string{"make", "test", "-run", "X"}) {
		t.Errorf("saved command + args = %v, %v", got, err)
	}
	got, err = launchCommand(modeRun, bare, ac, []string{"ls", "-l"})
	if err != nil || !slices.Equal(got, []string{"ls", "-l"}) {
		t.Errorf("command after -- = %v, %v", got, err)
	}
	if _, err := launchCommand(modeRun, bare, ac, nil); err == nil {
		t.Error("no saved command and none given must be an error")
	}
	got, err = launchCommand(modeChat, saved, ac, []string{"--continue"})
	if err != nil || !slices.Equal(got, []string{"claude", "--continue"}) {
		t.Errorf("chat ignores the saved command: %v, %v", got, err)
	}
	t.Setenv("SHELL", "/bin/zsh")
	got, _ = launchCommand(modeShell, saved, ac, nil)
	if !slices.Equal(got, []string{"/bin/zsh"}) {
		t.Errorf("shell = %v", got)
	}
}

func TestPlanLaunchRefusesUnenforcedRules(t *testing.T) {
	launchFixture(t)
	_, err := planLaunch(&launchFlags{profiles: []string{"dev/go"}}, modeRun, []string{"true"})
	if err == nil || !strings.Contains(err.Error(), "enforces none") {
		t.Errorf("rules on a none container must be refused, got %v", err)
	}
	_, err = planLaunch(&launchFlags{write: []string{"/tmp/x"}}, modeRun, []string{"true"})
	if err == nil {
		t.Error("a --write without a sandbox backend must be refused")
	}
	if _, err := planLaunch(&launchFlags{profiles: []string{"docker"}}, modeRun, []string{"true"}); err == nil {
		t.Error("docker is not supported by run yet: must be refused, not run unsandboxed")
	}
	plan, err := planLaunch(&launchFlags{}, modeRun, []string{"true"})
	if err != nil || plan.policy != nil {
		t.Errorf("no profile, no rule: runs unsandboxed; got policy %v, err %v", plan, err)
	}
}

func TestPlanLaunchNono(t *testing.T) {
	work := launchFixture(t)
	home := os.Getenv("HOME")
	f := &launchFlags{profiles: []string{"sandbox/nono", "dev/go"}, write: []string{"~/extra"}}
	plan, err := planLaunch(f, modeRun, []string{"make"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.policy == nil || plan.policy.WorkDir != work {
		t.Fatalf("policy = %+v", plan.policy)
	}
	var write, exclude []string
	for _, r := range plan.policy.Write {
		write = append(write, r.Path)
	}
	for _, r := range plan.policy.Exclude {
		exclude = append(exclude, r.Path)
	}
	if !slices.Contains(write, filepath.Join(home, "extra")) || !slices.Contains(write, filepath.Join(home, "go/pkg")) {
		t.Errorf("write = %v", write)
	}
	if slices.Contains(write, filepath.Join(home, ".claude")) {
		t.Error("run must not get the agent's own files")
	}
	if !slices.Contains(exclude, filepath.Join(home, ".ateam")) || !slices.Contains(exclude, filepath.Join(home, ".ssh")) {
		t.Errorf("exclude lacks ateam's home or ~/.ssh: %v", exclude)
	}

	chat, err := planLaunch(f, modeChat, nil)
	if err != nil {
		t.Fatal(err)
	}
	var chatWrite []string
	for _, r := range chat.policy.Write {
		chatWrite = append(chatWrite, r.Path)
	}
	if !slices.Contains(chatWrite, filepath.Join(home, ".claude")) || !slices.Contains(chat.policy.Domains, "api.anthropic.com") {
		t.Errorf("chat adds agent/claude: write %v domains %v", chatWrite, chat.policy.Domains)
	}
	if chat.argv[0] != "claude" {
		t.Errorf("chat argv = %v", chat.argv)
	}
}

func TestSaveProfileRoundTrip(t *testing.T) {
	launchFixture(t)
	f := &launchFlags{profiles: []string{"sandbox/nono", "dev/go"}, write: []string{"~/dotfiles"}, command: "make test"}
	if err := saveProfile("myproj", f); err != nil {
		t.Fatal(err)
	}
	if err := saveProfile("myproj", f); err == nil {
		t.Error("an existing profile must not be replaced without --force")
	}
	plan, err := planLaunch(&launchFlags{profiles: []string{"myproj"}}, modeRun, []string{"-v"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(plan.argv, []string{"make", "test", "-v"}) || plan.container.Type != runtime.ContainerTypeNono {
		t.Errorf("argv %v container %q", plan.argv, plan.container.Type)
	}
	file := filepath.Join(os.Getenv(runtime.HomeEnv), "profiles", "myproj.hcl")
	byPath, err := planLaunch(&launchFlags{profiles: []string{file}}, modeRun, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(byPath.argv, []string{"make", "test"}) {
		t.Errorf("by path: argv %v", byPath.argv)
	}
	if err := saveProfile("bad", &launchFlags{profiles: []string{"nope"}}); err == nil {
		t.Error("a profile that does not load must not be saved")
	}
	if _, err := os.Stat(filepath.Join(os.Getenv(runtime.HomeEnv), "profiles", "bad.hcl")); err == nil {
		t.Error("the refused profile was left on disk")
	}
}

func TestRunGuardedReportsHookChanges(t *testing.T) {
	work := t.TempDir()
	if err := os.MkdirAll(filepath.Join(work, ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, ".git", "config"), []byte("[core]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "git.1")
	if code := runGuarded([]string{"/bin/sh", "-c", "exit 0"}, work, backup); code != 0 {
		t.Errorf("a clean run returns the command's code, got %d", code)
	}
	if _, err := os.Stat(backup); err == nil {
		t.Error("the backup of an unchanged repository must be removed")
	}
	if code := runGuarded([]string{"/bin/sh", "-c", "exit 7"}, work, backup); code != 7 {
		t.Errorf("exit code = %d, want 7", code)
	}
	plant := "echo evil > " + filepath.Join(work, ".git", "hooks", "post-checkout")
	if code := runGuarded([]string{"/bin/sh", "-c", plant}, work, backup); code != 3 {
		t.Errorf("a planted hook must fail the launch with 3, got %d", code)
	}
	if data, err := os.ReadFile(filepath.Join(backup, ".git", "config")); err != nil || string(data) != "[core]\n" {
		t.Errorf("the backup must be kept after a change: %q, %v", data, err)
	}
}

func TestConfigDirGrant(t *testing.T) {
	for _, tc := range []struct{ dir, want string }{
		{"", ""},
		{"/abs/claude", "/abs/claude"},
		{"agents/claude", "/state/agents/claude"},
		{"agents/{{ROLE}}", "/state/agents"},
		{"agents/claude-{{ROLE}}/cfg", "/state/agents"},
	} {
		if got := configDirGrant(tc.dir, "/state"); got != tc.want {
			t.Errorf("configDirGrant(%q) = %q, want %q", tc.dir, got, tc.want)
		}
	}
}

func TestRejectUnenforcedPolicy(t *testing.T) {
	rules := &runtime.ProfileConfig{Name: "p", Write: []string{"/x"}}
	none := &runtime.ContainerConfig{Name: "none", Type: "none"}
	nono := &runtime.ContainerConfig{Name: "nono", Type: runtime.ContainerTypeNono}
	if err := rejectUnenforcedPolicy(rules, none, "p"); err == nil {
		t.Error("rules on a none container must be refused")
	}
	if err := rejectUnenforcedPolicy(rules, nono, "p"); err != nil {
		t.Errorf("nono enforces the rules: %v", err)
	}
	if err := rejectUnenforcedPolicy(&runtime.ProfileConfig{Name: "plain"}, none, "plain"); err != nil {
		t.Errorf("no rule, nothing to refuse: %v", err)
	}
}

// An unattended run on a nono container wraps the agent in `nono run` with
// a profile holding the chat rules plus what the run itself writes.
func TestBuildNonoContainerForUnattendedRun(t *testing.T) {
	if _, err := exec.LookPath("nono"); err != nil {
		t.Skip("nono not installed")
	}
	if goruntime.GOOS != "darwin" {
		t.Skip("the nono backend is macOS only for now")
	}
	work := launchFixture(t)
	cfg, err := runtime.Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	composed, err := cfg.ComposeProfiles([]string{"sandbox/nono", "dev/go"}, runtime.ProfileConfig{})
	if err != nil {
		t.Fatal(err)
	}
	prof, ac, cc, err := cfg.ResolveComposed(composed)
	if err != nil {
		t.Fatal(err)
	}
	ac.ConfigDir = "agents/claude"
	env := &root.ResolvedEnv{HomeDir: os.Getenv(runtime.HomeEnv), WorkDir: work}
	ct, err := buildNonoContainer(env, cfg, prof, ac, cc.Name)
	if err != nil {
		t.Skipf("nono cannot be queried here: %v", err)
	}
	nc, ok := ct.(*container.NonoContainer)
	if !ok || nc.WorkDir != work {
		t.Fatalf("container = %#v", ct)
	}
	cmd := nc.CmdFactory()(t.Context(), "claude", "-p")
	if !slices.Contains(cmd.Args, "--silent") || cmd.Args[len(cmd.Args)-2] != "claude" || cmd.Dir != work {
		t.Errorf("command = %v in %q", cmd.Args, cmd.Dir)
	}
	i := slices.Index(cmd.Args, "--profile")
	data, err := os.ReadFile(cmd.Args[i+1])
	if err != nil {
		t.Fatal(err)
	}
	home := os.Getenv("HOME")
	for _, want := range []string{
		filepath.Join(home, ".claude"),                          // agent/claude
		filepath.Join(home, "go/pkg"),                           // dev/go
		filepath.Join(env.HomeDir, "state", "agents", "claude"), // the agent's config dir
		filepath.Join(env.HomeDir, "state", "runtime"),          // the run's output
		"api.anthropic.com",
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("the nono profile lacks %s", want)
		}
	}
}
