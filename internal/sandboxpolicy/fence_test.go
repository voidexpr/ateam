package sandboxpolicy

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestRenderFence(t *testing.T) {
	in := testInput(t)
	in.Exclude = []string{"~/.ssh"}
	in.Read = []string{"/opt/data"}
	in.Write = []string{"~/go/pkg"}
	in.Domains = []string{"proxy.golang.org", "api.anthropic.com"}
	p, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := RenderFence(p, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	fs := cfg.Filesystem
	ssh, pkg := filepath.Join(in.Home, ".ssh"), filepath.Join(in.Home, "go/pkg")
	if !fs.DefaultDenyRead {
		t.Error("reading must be closed outside the grants")
	}
	// Every writable path is readable too: fence's write grant alone is not.
	for _, want := range []string{in.Home, "/opt/data", in.WorkDir, pkg} {
		if !slices.Contains(fs.AllowRead, want) {
			t.Errorf("allowRead misses %s: %v", want, fs.AllowRead)
		}
	}
	if !slices.Equal(fs.AllowWrite, sortedStrings([]string{in.WorkDir, pkg})) {
		t.Errorf("allowWrite = %v", fs.AllowWrite)
	}
	// An excluded path is closed for reads and for writes: ateam.hcl lies
	// inside the writable working folder.
	hcl := filepath.Join(in.ProjectDir, "ateam.hcl")
	for _, want := range []string{ssh, in.AteamHome, hcl} {
		if !slices.Contains(fs.DenyRead, want) || !slices.Contains(fs.DenyWrite, want) {
			t.Errorf("%s must be in denyRead and denyWrite: %v / %v", want, fs.DenyRead, fs.DenyWrite)
		}
	}
	net := cfg.Network
	if !slices.Equal(net.AllowedDomains, []string{"api.anthropic.com", "proxy.golang.org"}) || !net.AllowLocalBinding || !net.AllowLocalOutbound {
		t.Errorf("network = %+v", net)
	}
	if cfg.Command.UseDefaults {
		t.Error("fence's own command list must be off: the policy has no command rule")
	}

	p.Domains = nil
	cfg, _ = RenderFence(p, "darwin")
	if net := cfg.Network; len(net.AllowedDomains) != 0 || net.AllowLocalBinding || net.AllowLocalOutbound {
		t.Errorf("no domain must close the network, localhost included: %+v", net)
	}
	if _, err := RenderFence(p, "windows"); err == nil {
		t.Error("an unsupported system must be refused")
	}
}

// On Linux fence opens localhost one listed port at a time: it stays
// closed, which is said in the notes. The denies are the same; the folder
// above a read-only path is bound onto itself so it cannot be renamed.
func TestRenderFenceLinux(t *testing.T) {
	in := testInput(t)
	if err := os.MkdirAll(filepath.Join(in.WorkDir, ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	in.Exclude = []string{"~/.ssh"}
	in.Domains = []string{"example.com"}
	p, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	mac, err := RenderFence(p, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	linux, err := RenderFence(p, "linux")
	if err != nil {
		t.Fatal(err)
	}
	if net := linux.Network; net.AllowLocalBinding || net.AllowLocalOutbound || !slices.Equal(net.AllowedDomains, []string{"example.com"}) {
		t.Errorf("network = %+v", net)
	}
	if !reflect.DeepEqual(mac.Filesystem.DenyRead, linux.Filesystem.DenyRead) || !reflect.DeepEqual(mac.Filesystem.DenyWrite, linux.Filesystem.DenyWrite) {
		t.Errorf("the denies must not depend on the system:\n%+v\n%+v", mac.Filesystem, linux.Filesystem)
	}
	git := filepath.Join(in.WorkDir, ".git")
	if !slices.Contains(linux.Filesystem.AllowWrite, git) || slices.Contains(mac.Filesystem.AllowWrite, git) {
		t.Errorf(".git is pinned by a mount on Linux only: linux %v, mac %v", linux.Filesystem.AllowWrite, mac.Filesystem.AllowWrite)
	}
	if !slices.ContainsFunc(FenceNotes("linux"), func(n string) bool { return strings.Contains(n, "localhost of its own") }) {
		t.Errorf("the notes must say the host's localhost is closed: %v", FenceNotes("linux"))
	}
}

// On Linux fence closes a path with a mount, so a protected path that does
// not exist yet inside a writable folder can be created: it is listed as
// not enforced, never left unsaid.
func TestFenceGaps(t *testing.T) {
	in := testInput(t)
	if err := os.MkdirAll(filepath.Join(in.WorkDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	in.Exclude = []string{"~/.ssh"} // does not exist, but nothing can be created in the home
	p, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if gaps := FenceGaps(p, "darwin"); len(gaps) != 0 {
		t.Errorf("Seatbelt closes a path that does not exist: %v", paths(gaps))
	}
	gaps := paths(FenceGaps(p, "linux"))
	for _, want := range []string{
		filepath.Join(in.ProjectDir, "secrets.env"),
		filepath.Join(in.ProjectDir, "profiles"),
		filepath.Join(in.WorkDir, ".git", "hooks"),
	} {
		if !slices.Contains(gaps, want) {
			t.Errorf("%s does not exist and can be created: %v", want, gaps)
		}
	}
	for _, never := range []string{filepath.Join(in.ProjectDir, "ateam.hcl"), filepath.Join(in.Home, ".ssh"), in.AteamHome} {
		if slices.Contains(gaps, never) {
			t.Errorf("%s exists, or lies outside every writable folder: %v", never, gaps)
		}
	}
}

// fence cannot open a path inside a folder that stays denied: the rule is
// refused and named, never dropped.
func TestRenderFenceRefusesGrantInsideExclude(t *testing.T) {
	for _, attr := range []string{"read", "write"} {
		in := testInput(t)
		in.Exclude = []string{"~/.ssh"}
		in.Origin = func(a, entry string) string { return "profile mine" }
		if attr == "read" {
			in.Read = []string{"~/.ssh/config"}
		} else {
			in.Write = []string{"~/.ssh/known_hosts"}
		}
		p, err := Build(in)
		if err != nil {
			t.Fatal(err)
		}
		_, err = RenderFence(p, "darwin")
		if err == nil {
			t.Fatalf("%s inside an excluded folder must be refused", attr)
		}
		for _, want := range []string{filepath.Join(in.Home, ".ssh"), "profile mine"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the error must name %q: %v", want, err)
			}
		}
		// The same policy is one nono expresses.
		if _, err := RenderNono(p, NonoInfo{}, "darwin"); err != nil {
			t.Errorf("nono renders it: %v", err)
		}
	}
}

// A read of exactly an excluded path inside a writable folder, and the
// repository's hooks and config, stay readable and are write-denied.
func TestFenceKeepsReadOnlyInsideWritableFolder(t *testing.T) {
	in := testInput(t)
	if err := os.MkdirAll(filepath.Join(in.WorkDir, ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	hcl := filepath.Join(in.ProjectDir, "ateam.hcl")
	in.Read = []string{hcl}
	p, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := RenderFence(p, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	fs := cfg.Filesystem
	for _, want := range []string{hcl, filepath.Join(in.WorkDir, ".git", "hooks"), filepath.Join(in.WorkDir, ".git", "config")} {
		if !slices.Contains(fs.DenyWrite, want) {
			t.Errorf("denyWrite misses %s: %v", want, fs.DenyWrite)
		}
		if slices.Contains(fs.DenyRead, want) {
			t.Errorf("%s must stay readable: %v", want, fs.DenyRead)
		}
	}

	in.Read, in.Write = nil, []string{hcl} // opened fully, on purpose
	p, _ = Build(in)
	cfg, _ = RenderFence(p, "darwin")
	if slices.Contains(cfg.Filesystem.DenyWrite, hcl) || !slices.Contains(cfg.Filesystem.AllowWrite, hcl) {
		t.Errorf("an explicit write is not read-only: %+v", cfg.Filesystem)
	}
}

func TestWriteFenceConfig(t *testing.T) {
	in := testInput(t)
	p, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := RenderFence(p, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "sub", "fence.json")
	refuse := func(string) error { return errors.New("refused") }
	if err := WriteFenceConfig(cfg, dest, refuse); err == nil {
		t.Fatal("a refused configuration must be an error")
	}
	if entries, _ := os.ReadDir(filepath.Dir(dest)); len(entries) != 0 {
		t.Errorf("a refused configuration leaves no file: %v", entries)
	}
	if err := WriteFenceConfig(cfg, dest, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	// The keys are fence's: a list fence does not find is a rule it drops.
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("%v in %s", err, data)
	}
	for section, keys := range map[string][]string{
		"filesystem": {"defaultDenyRead", "allowRead", "denyRead", "allowWrite", "denyWrite"},
		"network":    {"allowedDomains", "allowLocalBinding", "allowLocalOutbound"},
		"command":    {"useDefaults"},
	} {
		var fields map[string]any
		if err := json.Unmarshal(doc[section], &fields); err != nil {
			t.Fatalf("%s: %v in %s", section, err, data)
		}
		for _, key := range keys {
			if _, ok := fields[key]; !ok {
				t.Errorf("%s.%s is missing in %s", section, key, data)
			}
		}
	}
	if got := FencePrefix("/bin/ateam", dest, in.WorkDir); !slices.Equal(got, []string{"/bin/ateam", "fence-exec", "--settings", dest, "--workdir", in.WorkDir, "--"}) {
		t.Errorf("prefix = %v", got)
	}
}

// Localhost has three forms and no backend takes all of them on both
// systems: each renders what it has and refuses the rest by name.
func TestNetworkRulesPerBackend(t *testing.T) {
	build := func(domains []string, deny ...string) *Policy {
		t.Helper()
		in := testInput(t)
		in.Domains, in.Deny = domains, deny
		p, err := Build(in)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	refused := func(what string, err error, want string) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s must be refused, naming %q: %v", what, want, err)
		}
	}

	// localhost:PORT, with a host
	p := build([]string{"example.com", "localhost:5432", "localhost:3000"})
	if !slices.Equal(p.HostPorts, []int{3000, 5432}) || p.HostLocalhost || !slices.Equal(p.Domains, []string{"example.com"}) {
		t.Fatalf("policy = %+v", p)
	}
	linux, err := RenderFence(p, "linux")
	if err != nil {
		t.Fatal(err)
	}
	if net := linux.Network; !net.AllowLocalOutbound || !slices.Equal(net.AllowLocalOutboundPorts, []int{3000, 5432}) || net.AllowLocalBinding {
		t.Errorf("Linux bridges each listed port: %+v", net)
	}
	mac, err := RenderFence(p, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	if net := mac.Network; !net.AllowLocalOutbound || len(net.AllowLocalOutboundPorts) != 0 {
		t.Errorf("macOS with a host has all of localhost already: %+v", net)
	}
	nono, err := RenderNono(p, NonoInfo{}, "darwin")
	if err != nil || !slices.Equal(nono.Network.OpenPort, []int{0}) {
		t.Errorf("nono with a host opens all of localhost: %+v, %v", nono, err)
	}

	// localhost:PORT alone
	p = build([]string{"localhost:5432"})
	if p.NetworkClosed() {
		t.Error("a port of localhost is not a closed network")
	}
	if linux, err = RenderFence(p, "linux"); err != nil || !slices.Equal(linux.Network.AllowLocalOutboundPorts, []int{5432}) {
		t.Errorf("Linux: %+v, %v", linux, err)
	}
	_, err = RenderFence(p, "darwin")
	refused("a single port under fence on macOS", err, "localhost:5432")
	_, err = RenderNono(p, NonoInfo{}, "darwin")
	refused("localhost alone under nono", err, "localhost alone")

	// all of the host's localhost
	p = build([]string{"localhost"})
	if !p.HostLocalhost {
		t.Fatalf("policy = %+v", p)
	}
	if mac, err = RenderFence(p, "darwin"); err != nil || !mac.Network.AllowLocalOutbound || !mac.Network.AllowLocalBinding {
		t.Errorf("macOS opens all of localhost: %+v, %v", mac, err)
	}
	_, err = RenderFence(p, "linux")
	refused("all of localhost under fence on Linux", err, "localhost:PORT")

	// a denied host
	p = build([]string{"*.example.com"}, "internal.example.com")
	if mac, err = RenderFence(p, "darwin"); err != nil || !slices.Equal(mac.Network.DeniedDomains, []string{"internal.example.com"}) {
		t.Errorf("fence denies the host: %+v, %v", mac, err)
	}
	_, err = RenderNono(p, NonoInfo{}, "darwin")
	refused("a denied host under nono", err, "internal.example.com")

	// forms no backend takes
	for _, entry := range []string{"example.com:8443", "localhost:0", "localhost:x", "http://example.com", ""} {
		in := testInput(t)
		in.Domains = []string{entry}
		if _, err := Build(in); err == nil {
			t.Errorf("network entry %q must be refused", entry)
		}
	}
	// 127.0.0.1 and ::1 are localhost; other addresses are refused.
	p = build([]string{"127.0.0.1:5432", "[::1]:3000", "example.com"})
	if !slices.Equal(p.HostPorts, []int{3000, 5432}) || !slices.Equal(p.Domains, []string{"example.com"}) {
		t.Errorf("loopback addresses are localhost: %+v", p)
	}
	if p = build([]string{"127.0.0.1"}); !p.HostLocalhost || len(p.Domains) != 0 {
		t.Errorf("127.0.0.1 is all of localhost, not a host name: %+v", p)
	}
	for _, entry := range []string{"0.0.0.0:3000", "0.0.0.0", "10.0.0.5", "10.0.0.5:443", "::"} {
		in := testInput(t)
		in.Domains = []string{entry}
		if _, err := Build(in); err == nil {
			t.Errorf("network entry %q must be refused", entry)
		}
	}
	in := testInput(t)
	in.Deny = []string{"localhost"}
	if _, err := Build(in); err == nil {
		t.Error("a deny of localhost must be refused")
	}
}
