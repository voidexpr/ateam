package runtime

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func loadNetworkHCL(t *testing.T, hcl string) (*Config, error) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	file := filepath.Join(t.TempDir(), "net.hcl")
	if err := os.WriteFile(file, []byte(hcl), 0o600); err != nil {
		t.Fatal(err)
	}
	return LoadWithFiles(nil, []string{file})
}

func TestNetworkBlocksCompose(t *testing.T) {
	cfg, err := loadNetworkHCL(t, `
network "base" {
  allow = ["localhost:5432"]
}
network "llm" {
  allow = ["api.anthropic.com"]
}
network "webapp" {
  use   = ["base", "llm"]
  allow = ["registry.npmjs.org", "api.anthropic.com"]
  deny  = ["telemetry.example.com"]
}
profile "web" {
  network = ["webapp"]
}
profile "more" {
  use     = ["web"]
  network = ["llm"]
  domains = ["example.com"]
}
`)
	if err != nil {
		t.Fatal(err)
	}
	prof := cfg.Profiles["more"]
	if !slices.Equal(prof.Network, []string{"webapp", "llm"}) || prof.EntryOrigin("network", "webapp") != "web" {
		t.Errorf("network lists add up across use, with their origin: %v (%q)", prof.Network, prof.EntryOrigin("network", "webapp"))
	}
	if !prof.HasPolicy() {
		t.Error("a network is a rule only a sandbox backend enforces")
	}
	allow, deny, err := cfg.NetworkRules(prof.Network)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"localhost:5432", "api.anthropic.com", "registry.npmjs.org"}; !slices.Equal(allow, want) {
		t.Errorf("allow = %v, want %v", allow, want)
	}
	if !slices.Equal(deny, []string{"telemetry.example.com"}) {
		t.Errorf("deny = %v", deny)
	}
}

func TestNetworkBlocksAreCheckedAtLoad(t *testing.T) {
	for name, tc := range map[string]struct{ hcl, want string }{
		"unknown in a profile": {`profile "p" { network = ["nope"] }`, `unknown network "nope"`},
		"unknown in use":       {`network "a" { use = ["nope"] }`, `uses unknown network "nope"`},
		"cycle":                {"network \"a\" { use = [\"b\"] }\nnetwork \"b\" { use = [\"a\"] }", "uses itself"},
		"open by default":      {`network "a" { default = "allow" }`, `only "deny"`},
	} {
		if _, err := loadNetworkHCL(t, tc.hcl); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want an error holding %q, got %v", name, tc.want, err)
		}
	}
}
