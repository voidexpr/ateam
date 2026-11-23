package sandboxpolicy

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
	if _, err := RenderFence(p, "linux"); err == nil {
		t.Error("Linux must be refused: localhost cannot be opened as a whole there")
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
