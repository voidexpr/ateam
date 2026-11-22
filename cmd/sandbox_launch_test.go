package cmd

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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
