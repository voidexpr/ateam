package cmd

import (
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/fencesandbox/fence/pkg/fence"
)

// The embedded fence must turn a rendered configuration into the Seatbelt
// rules the policy means: a rule fence reads differently than ateam writes
// it would be a rule dropped. The profile is only generated here, not run.
func TestFenceSeatbeltRulesOfRenderedConfig(t *testing.T) {
	if goruntime.GOOS != "darwin" {
		t.Skip("the fence backend is macOS only for now")
	}
	work := launchFixture(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(work, ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := &launchFlags{profiles: []string{"sandbox/fence"}, write: []string{"~/extra"}, domains: []string{"example.com"}}
	plan, err := planLaunch(f, modeRun, []string{"true"})
	if err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(t.TempDir(), "fence.json")
	if _, err := plan.wrapped(t.Context(), modeRun, configFile); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadFenceConfig(configFile)
	if err != nil {
		t.Fatal(err)
	}
	manager := fence.NewManager(cfg, false, false)
	defer manager.Cleanup()
	wrapped, err := manager.WrapCommandInDir("true", work)
	if err != nil {
		t.Skipf("fence cannot prepare a sandbox here: %v", err)
	}
	// The profile is one shell-quoted argument: its double quotes are plain.
	rule := func(action, path string) string {
		return "(" + action + "\n  (subpath \"" + path + "\")"
	}
	ssh := filepath.Join(home, ".ssh")
	for _, want := range []string{
		"(deny default",
		rule("allow file-read-data", home),
		rule("deny file-read*", ssh),
		rule("deny file-write*", ssh),
		rule("allow file-write*", work),
		rule("allow file-write*", filepath.Join(home, "extra")),
		rule("allow file-read-data", filepath.Join(home, "extra")),
		rule("deny file-write*", filepath.Join(work, ".git", "hooks")),
		rule("deny file-write*", filepath.Join(work, ".git", "config")),
		// the folder above a read-only path cannot be renamed away
		"(deny file-write-unlink\n  (literal \"" + filepath.Join(work, ".git") + "\")",
		`(allow network-outbound (local ip "localhost:*"))`,
	} {
		if !strings.Contains(wrapped, want) {
			t.Errorf("the Seatbelt profile lacks:\n%s", want)
		}
	}
	for _, never := range []string{"(allow file-read*)", "(allow network*)\n"} {
		if strings.Contains(wrapped, never) {
			t.Errorf("the Seatbelt profile must not hold %q", never)
		}
	}
}

// An excluded path reached through a link (~/.claude kept in a synced
// folder) is denied where it really is: the check before a launch must not
// take the link's own spelling for an open path.
func TestFenceCheckFollowsLinks(t *testing.T) {
	if goruntime.GOOS != "darwin" {
		t.Skip("the fence backend is macOS only for now")
	}
	launchFixture(t)
	home := os.Getenv("HOME")
	real := filepath.Join(home, "synced", ".claude")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, ".credentials.json"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(home, ".claude")); err != nil {
		t.Fatal(err)
	}
	plan, err := planLaunch(&launchFlags{profiles: []string{"sandbox/fence"}}, modeRun, []string{"true"})
	if err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(t.TempDir(), "fence.json")
	if _, err := plan.wrapped(t.Context(), modeRun, configFile); err != nil {
		t.Fatalf("an excluded link must not stop the launch: %v", err)
	}
	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), filepath.Join(real, ".credentials.json")) {
		t.Errorf("the link's target must be denied:\n%s", data)
	}
}
