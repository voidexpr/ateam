package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ateam/internal/sandboxprobe"
)

func probeEnvFor(t *testing.T) sandboxprobe.Env {
	t.Helper()
	env, err := sandboxprobe.CurrentEnv(homeDirFlag(), builtinSecrets())
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func checkNamed(t *testing.T, checks []sandboxprobe.Check, group, name string) sandboxprobe.Check {
	t.Helper()
	at := slices.IndexFunc(checks, func(c sandboxprobe.Check) bool { return c.Group == group && c.Name == name })
	if at < 0 {
		t.Fatalf("no check %s %s", group, name)
	}
	return checks[at]
}

func TestBuiltinSecretsComeFromTheBinary(t *testing.T) {
	launchFixture(t)
	home, _ := os.UserHomeDir()
	secrets := builtinSecrets()
	for _, want := range []string{".ssh", ".aws", filepath.Join(".config", "gh")} {
		if !slices.Contains(secrets, filepath.Join(home, want)) {
			t.Errorf("the embedded %s filter misses ~/%s: %v", secretsFilter, want, secrets)
		}
	}
}

// --profile without --start-sandbox compares: it must build the policy of
// a profile whose container enforces nothing, which a launch refuses.
func TestProbePolicyNeedsNoSandbox(t *testing.T) {
	launchFixture(t)
	f := &launchFlags{profiles: []string{"dev/go"}, exclude: []string{"~/private"}}
	if _, err := planLaunch(f, modeRun, []string{"true"}); err == nil {
		t.Fatal("a launch of dev/go without a sandbox backend must be refused: the test's premise")
	}
	policy, err := probePolicy(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(policy.Domains) == 0 || len(policy.Write) == 0 {
		t.Errorf("the policy of dev/go has no rule: %+v", policy)
	}
}

func TestProbeChecksOfAProfile(t *testing.T) {
	work := launchFixture(t)
	o := &probeOptions{launch: launchFlags{
		profiles: []string{"dev/go"}, write: []string{"~/scratch", "~/.claude"}, exclude: []string{"~/private"},
		domains: []string{"example.net"}, listen: []string{"3000"},
	}}
	policy, err := probePolicy(&o.launch)
	if err != nil {
		t.Fatal(err)
	}
	checks, err := probeChecks(probeEnvFor(t), policy, o, []string{"~/mine", "rel/path"}, true)
	if err != nil {
		t.Fatal(err)
	}
	expects := func(group, name string, want ...sandboxprobe.Permission) {
		t.Helper()
		if c := checkNamed(t, checks, group, name); !slices.Equal(c.Expect, want) {
			t.Errorf("%s %s expects %v (%s), want %v", group, name, c.Expect, c.ExpectWhy, want)
		}
	}
	expects("profile", "~/scratch", sandboxprobe.Write, sandboxprobe.Create)
	expects("secrets", "~/private", sandboxprobe.Blocked, sandboxprobe.Absent)
	// dev/go does not exclude ~/.ssh: the built-in judgment stays, a
	// readable key is still a risk.
	expects("secrets", "~/.ssh")
	if c := checkNamed(t, checks, "secrets", "~/.ssh"); !slices.Contains(c.Safe, sandboxprobe.Blocked) || slices.Contains(c.Safe, sandboxprobe.Read) {
		t.Errorf("~/.ssh safe set under a profile that does not name it: %v", c.Safe)
	}
	expects("profile", "https://example.net (proxy)", sandboxprobe.Connect)
	expects("profile", "https://example.org (direct)", sandboxprobe.Blocked)
	expects("profile", "127.0.0.1:3000 (listen)", sandboxprobe.Listen)
	expects("network", "1.1.1.1:443 (direct)", sandboxprobe.Blocked)
	// A built-in check under a rule of the profile takes the rule: the
	// agent's own folder is writable on purpose, the startup files are not.
	expects("agent", "~/.claude/settings.json", sandboxprobe.Read, sandboxprobe.Absent, sandboxprobe.Blocked, sandboxprobe.Write, sandboxprobe.Create)
	// That the home stays readable is no rule of the profile: what no rule
	// names keeps its built-in judgment.
	expects("shell", "~/.zshrc")
	expects("paths", "~/mine")
	if c := checkNamed(t, checks, "paths", "rel/path"); c.Target != filepath.Join(work, "rel", "path") {
		t.Errorf("a relative path is taken from the working folder: %s", c.Target)
	}
	if c := checkNamed(t, checks, "where", "./"); c.Target != work || !slices.Contains(c.Expect, sandboxprobe.Write) {
		t.Errorf("the working folder: %+v", c)
	}

	// Active checks are listed and off.
	selected, _, err := sandboxprobe.Select(checks, o.sel)
	if err != nil {
		t.Fatal(err)
	}
	isBuild := func(c sandboxprobe.Check) bool { return c.ID == "docker-build" }
	if !slices.ContainsFunc(checks, isBuild) || slices.ContainsFunc(selected, isBuild) {
		t.Error("docker-build must be listed and not selected without --with")
	}
}

func TestProbeWithoutProfileJudgesByBuiltinSets(t *testing.T) {
	launchFixture(t)
	t.Setenv("SHELL", "/bin/zsh")
	checks, err := probeChecks(probeEnvFor(t), nil, &probeOptions{}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range checks {
		if len(c.Expect) > 0 || c.Group == "profile" {
			t.Errorf("without a profile nothing is expected: %+v", c)
		}
	}
	if c := checkNamed(t, checks, "secrets", "~/.ssh"); !slices.Equal(c.Safe, []sandboxprobe.Permission{sandboxprobe.Blocked, sandboxprobe.Absent}) {
		t.Errorf("~/.ssh safe set: %v", c.Safe)
	}
}

func TestProbeStartSandboxRefusals(t *testing.T) {
	launchFixture(t)
	if _, err := probeLaunchPlan(&probeOptions{}); err == nil || !strings.Contains(err.Error(), "--profile sandbox/") {
		t.Errorf("--start-sandbox without a profile: %v", err)
	}
	if _, err := probeLaunchPlan(&probeOptions{launch: launchFlags{profiles: []string{"dev/go"}}}); err == nil {
		t.Error("--start-sandbox with a profile that names no sandbox backend must be refused")
	}
	t.Setenv("ATEAM_IN_SANDBOX", "1")
	_, err := probeLaunchPlan(&probeOptions{launch: launchFlags{profiles: []string{"sandbox/nono", "dev/go"}}})
	if err == nil || !strings.Contains(err.Error(), "already inside") {
		t.Errorf("--start-sandbox inside a sandbox: %v", err)
	}
}

func TestProbePayloadRoundTrip(t *testing.T) {
	launchFixture(t)
	checks, err := probeChecks(probeEnvFor(t), nil, &probeOptions{}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	payload := probePayload{Started: []string{"fence sandbox, profile x"}, Checks: checks, Skipped: 3, Quiet: true}
	payload.Checks[0].Before = &sandboxprobe.Facts{Exists: true, IsDir: true}
	encoded, err := payload.encode()
	if err != nil {
		t.Fatal(err)
	}
	// One argument is limited to 128 KiB on Linux.
	if len(encoded) > 64*1024 {
		t.Errorf("the encoded check list is %d bytes for %d checks", len(encoded), len(checks))
	}
	got, err := decodeProbePayload(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Checks) != len(checks) || got.Skipped != 3 || !got.Quiet || got.Started[0] != payload.Started[0] {
		t.Errorf("decoded payload differs: %d checks, %+v", len(got.Checks), got.Started)
	}
	if first := got.Checks[0]; first.ID != checks[0].ID || first.Before == nil || !first.Before.IsDir {
		t.Errorf("first check = %+v", first)
	}
	if _, err := decodeProbePayload("not a payload"); err == nil {
		t.Error("a broken --run-checks value must be an error")
	}
}

func TestProbeDryRunChecksNothing(t *testing.T) {
	work := launchFixture(t)
	o := &probeOptions{launch: launchFlags{exclude: []string{"~/private"}}}
	policy, err := probePolicy(&o.launch)
	if err != nil {
		t.Fatal(err)
	}
	checks, err := probeChecks(probeEnvFor(t), policy, o, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	selected, skipped, err := sandboxprobe.Select(checks, sandboxprobe.Selection{Only: []string{"secrets", "where"}})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := probeDryRun(&out, nil, checks, probePayload{Checks: selected, Skipped: skipped}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"would be: nothing started: checking this environment",
		"secrets:~/private  expects blocked|absent: the profile excludes it",
		"where:./  expects",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("--dry-run misses %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "network:") {
		t.Errorf("--dry-run lists only what would run:\n%s", got)
	}
	entries, _ := os.ReadDir(work)
	if len(entries) != 0 {
		t.Errorf("--dry-run left entries in the working folder: %v", entries)
	}
}
