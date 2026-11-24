package cmd

import (
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/ateam/internal/runtime"
	"github.com/ateam/internal/sandboxpolicy"
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
	f := &launchFlags{profiles: []string{"sandbox/fence"}, write: []string{"~/extra"}, read: []string{"~/.ssh/config"}, domains: []string{"example.com"}}
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
	// The carve-out: the file inside the excluded folder is allowed after
	// the folder's deny, and Seatbelt applies the last matching rule.
	deny := strings.Index(wrapped, rule("deny file-read-data", ssh))
	allow := strings.LastIndex(wrapped, "(allow file-read-data\n  (subpath \""+filepath.Join(ssh, "config")+"\")")
	if deny < 0 || allow < deny {
		t.Errorf("the read inside the excluded folder must be allowed after the folder's deny: deny at %d, allow at %d", deny, allow)
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

func TestFenceRequirements(t *testing.T) {
	none := func(string) (string, error) { return "", os.ErrNotExist }
	onlyBwrap := func(name string) (string, error) {
		if name == "bwrap" {
			return "/usr/bin/bwrap", nil
		}
		return "", os.ErrNotExist
	}
	all := func(name string) (string, error) { return "/usr/bin/" + name, nil }
	if err := fenceRequirements("linux", all); err != nil {
		t.Errorf("everything installed: %v", err)
	}
	err := fenceRequirements("linux", onlyBwrap)
	if err == nil || !strings.Contains(err.Error(), "not found in PATH: socat") || !strings.Contains(err.Error(), "bubblewrap") {
		t.Errorf("the error must name what is missing and the packages: %v", err)
	}
	if err := fenceRequirements("darwin", none); err == nil || !strings.Contains(err.Error(), "sandbox-exec") {
		t.Errorf("macOS needs sandbox-exec: %v", err)
	}
	if err := fenceRequirements("windows", all); err == nil {
		t.Error("an unsupported system must be refused")
	}
}

// What fence does not enforce is a warning, or an error for a strict
// container; `strict` is read from the container block.
func TestReportFenceGaps(t *testing.T) {
	gaps := []sandboxpolicy.Rule{{Path: "/work/.ateam/profiles"}}
	var out strings.Builder
	if err := reportFenceGaps(&out, nil, true); err != nil || out.Len() != 0 {
		t.Errorf("no gap, nothing to say: %v %q", err, out.String())
	}
	if err := reportFenceGaps(&out, gaps, false); err != nil || !strings.Contains(out.String(), "NOT ENFORCED") || !strings.Contains(out.String(), "/work/.ateam/profiles") {
		t.Errorf("a warning naming the path: %v %q", err, out.String())
	}
	if err := reportFenceGaps(&out, gaps, true); err == nil || !strings.Contains(err.Error(), "/work/.ateam/profiles") {
		t.Errorf("strict must refuse, naming the path: %v", err)
	}

	file := filepath.Join(t.TempDir(), "strict.hcl")
	if err := os.WriteFile(file, []byte("container \"fence\" {\n  type   = \"fence\"\n  strict = true\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	launchFixture(t)
	cfg, err := runtime.LoadWithFiles(nil, []string{file})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Containers["fence"].Strict {
		t.Error("strict = true must be read from the container block")
	}
}

func TestFenceExposures(t *testing.T) {
	got, err := fenceExposures([]string{"127.0.0.1:3000", "0.0.0.0:9000"})
	if err != nil || len(got) != 2 || got[0] != (fence.ExposedPort{BindAddress: "127.0.0.1", Port: 3000}) || got[1].BindAddress != "0.0.0.0" {
		t.Errorf("exposures = %+v, %v", got, err)
	}
	for _, bad := range []string{"3000", "localhost:3000", "127.0.0.1:x"} {
		if _, err := fenceExposures([]string{bad}); err == nil {
			t.Errorf("--expose %q must be refused", bad)
		}
	}
}
