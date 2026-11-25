package runtime

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func loadLayer(t *testing.T, hcl string) (*Config, error) {
	t.Helper()
	dir := t.TempDir()
	writeLayer(t, dir, hcl)
	return Load([]string{dir})
}

func TestProfileUseAddsListsAndNearestScalarWins(t *testing.T) {
	cfg, err := loadLayer(t, `
profile "go" {
  write = ["~/go/pkg"]
  model = "sonnet"
}
profile "node" {
  write = ["~/.npm", "~/go/pkg"]
  model = "haiku"
}
profile "proj" {
  use     = ["go", "node"]
  write   = ["~/dotfiles"]
  exclude = ["~/other"]
  command = ["make", "test"]
}
profile "pinned" {
  use   = ["proj"]
  model = "opus"
}
`)
	if err != nil {
		t.Fatal(err)
	}
	proj := cfg.Profiles["proj"]
	if want := []string{"~/go/pkg", "~/.npm", "~/dotfiles"}; !slices.Equal(proj.Write, want) {
		t.Errorf("write = %v, want %v", proj.Write, want)
	}
	if proj.Model != "haiku" {
		t.Errorf("model = %q, want the later use (haiku)", proj.Model)
	}
	if got := proj.EntryOrigin("write", "~/go/pkg"); got != "go" {
		t.Errorf("origin of ~/go/pkg = %q, want go", got)
	}
	if got := proj.Origin("model"); got != "node" {
		t.Errorf("origin of model = %q, want node", got)
	}
	pinned := cfg.Profiles["pinned"]
	if pinned.Model != "opus" || pinned.Origin("model") != "pinned" {
		t.Errorf("own scalar must win: model=%q from %q", pinned.Model, pinned.Origin("model"))
	}
	if !slices.Equal(pinned.Command, []string{"make", "test"}) || pinned.Origin("command") != "proj" {
		t.Errorf("command = %v from %q", pinned.Command, pinned.Origin("command"))
	}
	if !pinned.HasPolicy() {
		t.Error("pinned inherits a policy")
	}
}

func TestProfileUseErrors(t *testing.T) {
	for name, tc := range map[string]struct{ hcl, want string }{
		"cycle":   {`profile "a" { use = ["b"] }` + "\n" + `profile "b" { use = ["a"] }`, "a -> b -> a"},
		"self":    {`profile "a" { use = ["a"] }`, "a -> a"},
		"unknown": {`profile "a" { use = ["nope"] }`, `unknown profile "nope"`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadLayer(t, tc.hcl)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("want an error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestFragmentProfileTakesDefaultAgentAndContainer(t *testing.T) {
	cfg, err := loadLayer(t, `profile "go" { write = ["~/go/pkg"] }`)
	if err != nil {
		t.Fatal(err)
	}
	prof, ac, cc, err := cfg.ResolveProfile("go")
	if err != nil {
		t.Fatal(err)
	}
	def := cfg.Profiles[DefaultProfile]
	if ac.Name != def.Agent || cc.Name != def.Container {
		t.Errorf("got agent %q container %q, want the default profile's %q %q", ac.Name, cc.Name, def.Agent, def.Container)
	}
	if prof.Origin("agent") != DefaultProfile {
		t.Errorf("agent origin = %q", prof.Origin("agent"))
	}
	if cfg.Profiles["go"].Agent != "" {
		t.Error("the stored profile must stay a fragment")
	}
}

func TestComposeProfilesFromFlags(t *testing.T) {
	cfg, err := loadLayer(t, `
profile "go" { write = ["~/go/pkg"] }
profile "docker2" {
  agent     = "claude"
  container = "docker"
}
`)
	if err != nil {
		t.Fatal(err)
	}
	p, err := cfg.ComposeProfiles([]string{"docker2", "go"}, ProfileConfig{Write: []string{"/tmp/x"}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Container != "docker" || !slices.Equal(p.Write, []string{"~/go/pkg", "/tmp/x"}) {
		t.Errorf("got container %q write %v", p.Container, p.Write)
	}
	if p.EntryOrigin("write", "/tmp/x") != "command line" {
		t.Errorf("origin = %q", p.EntryOrigin("write", "/tmp/x"))
	}
	if _, err := cfg.ComposeProfiles([]string{"nope"}, ProfileConfig{}); err == nil {
		t.Error("an unknown profile must be an error")
	}
}

func TestBuiltinProfilesUnchangedByComposition(t *testing.T) {
	cfg, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, p := range cfg.Profiles {
		if strings.Contains(name, "/") {
			continue // the composable sandbox profiles, checked below
		}
		if p.Agent == "" || p.Container == "" {
			t.Errorf("built-in profile %q lost its agent or container", name)
		}
		if p.HasPolicy() || len(p.Use) > 0 {
			t.Errorf("built-in profile %q gained a policy", name)
		}
	}
}

func TestBuiltinSandboxProfilesCompose(t *testing.T) {
	cfg, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := cfg.ComposeProfiles([]string{"sandbox/nono", "agent/claude", "dev/all"}, ProfileConfig{})
	if err != nil {
		t.Fatal(err)
	}
	prof, _, cc, err := cfg.ResolveComposed(p)
	if err != nil {
		t.Fatal(err)
	}
	if cc.Type != ContainerTypeNono {
		t.Errorf("container type = %q", cc.Type)
	}
	// The path lists are filters: the profiles name them.
	for attr, want := range map[string]string{"read": "agent/secrets", "write": "dev/go-write", "domains": "api.anthropic.com"} {
		list := map[string][]string{"read": prof.Read, "write": prof.Write, "domains": prof.Domains}[attr]
		if !slices.Contains(list, want) {
			t.Errorf("%s lacks %s", attr, want)
		}
	}
	if got := prof.EntryOrigin("read", "agent/secrets"); got != "agent/secrets" {
		t.Errorf("origin of the agent/secrets filter = %q", got)
	}
	for _, name := range []string{"agent/secrets", "system/removable", "system/sockets", "agent/claude-write", "dev/go-write", "dev/node-write"} {
		if cfg.Filters[name] == "" {
			t.Errorf("embedded filter %q is missing", name)
		}
	}
}

// A filter file in a layer replaces an embedded one of the same name, and
// a filter block in the layer's ateam.hcl replaces the file.
func TestLayerFilterFilesOverride(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "filters", "dev"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{
		"dev/go-write.filter": "version 1\n/mygo\n",
		"dev/mine.filter":     "version 1\n/mine\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, "filters", name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeLayer(t, dir, "filter \"dev/mine\" {\n  rules = \"version 1\\n/block\\n\"\n}\n")
	cfg, err := Load([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cfg.Filters["dev/go-write"], "/mygo") {
		t.Errorf("the layer's file must replace the embedded filter: %q", cfg.Filters["dev/go-write"])
	}
	if !strings.Contains(cfg.Filters["dev/mine"], "/block") {
		t.Errorf("the filter block must replace the layer's file: %q", cfg.Filters["dev/mine"])
	}
}

func TestFilterBlocks(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "secrets.filter"), []byte("version 1\nroot ~\nexclude /.ssh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeLayer(t, dir, `
filter "my/secrets" {
  file = "secrets.filter"
}
filter "my/go-write" {
  rules = <<-EOF
    version 1
    /go/pkg
  EOF
}
profile "p" {
  read  = ["my/secrets"]
  write = ["my/go-write", "~/x"]
}
`)
	cfg, err := Load([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cfg.Filters["my/secrets"], "exclude /.ssh") || !strings.Contains(cfg.Filters["my/go-write"], "/go/pkg") {
		t.Errorf("filters = %v", cfg.Filters)
	}

	for name, tc := range map[string]struct{ hcl, want string }{
		"both":     {"filter \"f\" {\n  file = \"a\"\n  rules = \"version 1\"\n}", "one of file or rules"},
		"neither":  {`filter "f" {}`, "one of file or rules"},
		"missing":  {`filter "f" { file = "nope.filter" }`, "nope.filter"},
		"wildcard": {`filter "f" { rules = "version 1\nexclude /**/*.pem\n" }`, "not one path"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadLayer(t, tc.hcl)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("want a load error containing %q, got %v", tc.want, err)
			}
		})
	}
}
