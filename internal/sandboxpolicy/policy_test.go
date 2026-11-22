package sandboxpolicy

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func testInput(t *testing.T) Input {
	t.Helper()
	home := t.TempDir()
	work := filepath.Join(home, "proj")
	if err := os.MkdirAll(filepath.Join(work, ".ateam", "runtime"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"ateam.hcl", "notes.md"} {
		if err := os.WriteFile(filepath.Join(work, ".ateam", f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return Input{
		Name: "proj", Home: home, WorkDir: work,
		AteamHome: filepath.Join(home, ".ateam"), ProjectDir: filepath.Join(work, ".ateam"),
	}
}

func paths(rules []Rule) []string {
	var out []string
	for _, r := range rules {
		out = append(out, r.Path)
	}
	return out
}

func TestBuildExpandsAndAddsAteamRules(t *testing.T) {
	in := testInput(t)
	in.Write = []string{"~/go/pkg"}
	in.Exclude = []string{"~/.ssh", "/Volumes"}
	p, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if got := paths(p.Write); !slices.Equal(got, []string{filepath.Join(in.Home, "go/pkg")}) {
		t.Errorf("write = %v", got)
	}
	ex := paths(p.Exclude)
	for _, want := range []string{
		filepath.Join(in.Home, ".ssh"), "/Volumes", in.AteamHome,
		filepath.Join(in.ProjectDir, "ateam.hcl"),
		filepath.Join(in.ProjectDir, "state.sqlite"), // closed before it exists
		filepath.Join(in.ProjectDir, "notes.md"),     // an entry ateam does not know
	} {
		if !slices.Contains(ex, want) {
			t.Errorf("exclude lacks %s: %v", want, ex)
		}
	}
	for _, open := range ateamOpen {
		if slices.Contains(ex, filepath.Join(in.ProjectDir, open)) {
			t.Errorf(".ateam/%s must stay reachable", open)
		}
	}
}

func TestBuildRefusesWhatItCannotResolve(t *testing.T) {
	in := testInput(t)
	in.Read = []string{"go-read"}
	if _, err := Build(in); err == nil || !strings.Contains(err.Error(), "go-read") {
		t.Errorf("a filter name must be refused for now, got %v", err)
	}
	in = testInput(t)
	in.WorkDir = in.Home
	if _, err := Build(in); err == nil {
		t.Error("running in the home must be refused")
	}
}

func TestExactGrantReopensExclude(t *testing.T) {
	in := testInput(t)
	in.Exclude = []string{"~/.ssh", "~/secret"}
	in.Read = []string{"~/.ssh/config", "~/secret"}
	in.Write = []string{in.ProjectDir + "/state.sqlite"}
	p, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	ex := paths(p.Exclude)
	if slices.Contains(ex, filepath.Join(in.Home, "secret")) {
		t.Error("a read of exactly the excluded path reopens it")
	}
	if !slices.Contains(ex, filepath.Join(in.Home, ".ssh")) {
		t.Error("a grant inside an excluded folder keeps the folder excluded")
	}
	if slices.Contains(ex, filepath.Join(in.ProjectDir, "state.sqlite")) {
		t.Error("an explicit grant reopens one of ateam's own rules")
	}
}

func TestRenderNono(t *testing.T) {
	in := testInput(t)
	in.Exclude = []string{"~/.ssh"}
	in.Read = []string{"~/.ssh/config", "~/.zshrc"}
	in.Write = []string{"~/go/pkg"}
	in.Domains = []string{"proxy.golang.org", "api.anthropic.com"}
	p, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	info := NonoInfo{Blocked: []string{filepath.Join(in.Home, ".zshrc"), filepath.Join(in.Home, ".aws")}}
	prof, err := RenderNono(p, info, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	fs := prof.Filesystem
	if !slices.Contains(fs.Read, in.Home) {
		t.Errorf("the home is one read grant: %v", fs.Read)
	}
	if !slices.Contains(fs.Deny, filepath.Join(in.Home, ".ssh")) || !slices.Contains(fs.Deny, in.AteamHome) {
		t.Errorf("deny = %v", fs.Deny)
	}
	// What nono blocks and the policy does not exclude is lifted (.aws,
	// .zshrc); a grant inside an excluded folder is reopened (.ssh/config).
	want := []string{filepath.Join(in.Home, ".aws"), filepath.Join(in.Home, ".ssh/config"), filepath.Join(in.Home, ".zshrc")}
	if !slices.Equal(fs.BypassProtection, want) {
		t.Errorf("bypass = %v, want %v", fs.BypassProtection, want)
	}
	p.Exclude = append(p.Exclude, Rule{Path: filepath.Join(in.Home, ".aws")})
	if again, _ := RenderNono(p, info, "darwin"); slices.Contains(again.Filesystem.BypassProtection, filepath.Join(in.Home, ".aws")) {
		t.Error("an excluded path must stay blocked")
	}
	if prof.Network.Block || !slices.Equal(prof.Network.AllowDomain, []string{"api.anthropic.com", "proxy.golang.org"}) {
		t.Errorf("network = %+v", prof.Network)
	}
	if !prof.AllowParentOfProtected || prof.Workdir.Access != "readwrite" {
		t.Errorf("profile = %+v", prof)
	}

	p.Domains = nil
	prof, _ = RenderNono(p, info, "darwin")
	if !prof.Network.Block || len(prof.Network.OpenPort) != 0 {
		t.Errorf("no domain must close the network, got %+v", prof.Network)
	}
	if _, err := RenderNono(p, info, "linux"); err == nil {
		t.Error("Linux must be refused until the listing walk is ported")
	}
}

// The rendered profile must be one the installed nono accepts.
func TestNonoAcceptsRenderedProfile(t *testing.T) {
	nono, err := exec.LookPath("nono")
	if err != nil {
		t.Skip("nono not installed")
	}
	in := testInput(t)
	in.Exclude = []string{"~/.ssh"}
	in.Read = []string{"~/.ssh/config"}
	in.Write = []string{"~/go/pkg"}
	in.Domains = []string{"api.anthropic.com", "*.github.com"}
	p, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	info, err := QueryNono(ctx, nono)
	if err != nil {
		t.Skipf("nono cannot be queried here: %v", err)
	}
	if len(info.Blocked) == 0 {
		t.Error("nono reports no required deny")
	}
	for _, domains := range [][]string{in.Domains, nil} {
		p.Domains = domains
		prof, err := RenderNono(p, info, "darwin")
		if err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(t.TempDir(), "profile.json")
		if err := WriteNonoProfile(ctx, nono, prof, dest); err != nil {
			t.Errorf("domains %v: %v", domains, err)
		}
	}
}

func TestExcludeCoversLinkTarget(t *testing.T) {
	in := testInput(t)
	real := filepath.Join(in.Home, "synced", ".ssh")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(in.Home, ".ssh")); err != nil {
		t.Fatal(err)
	}
	in.Exclude = []string{"~/.ssh"}
	p, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(real)
	if ex := paths(p.Exclude); !slices.Contains(ex, filepath.Join(in.Home, ".ssh")) || !slices.Contains(ex, resolved) {
		t.Errorf("exclude must hold the link and its target: %v", ex)
	}
}

func TestGitGuard(t *testing.T) {
	dir := t.TempDir()
	if _, ok := SnapshotGit(dir); ok {
		t.Error("no .git folder: nothing to guard")
	}
	hooks := filepath.Join(dir, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(rel, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(text), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(".git/config", "[core]\n")
	write(".git/hooks/pre-commit.sample", "#!/bin/sh\n")
	g, ok := SnapshotGit(dir)
	if !ok {
		t.Fatal("a .git folder must be guarded")
	}
	if changed := g.Changed(); len(changed) != 0 {
		t.Errorf("nothing changed yet: %v", changed)
	}
	backup := t.TempDir()
	if err := g.Backup(backup); err != nil {
		t.Fatal(err)
	}
	write(".git/config", "[core]\n\tpager = evil\n")
	write(".git/hooks/post-checkout", "#!/bin/sh\nevil\n")
	if err := os.Remove(filepath.Join(hooks, "pre-commit.sample")); err != nil {
		t.Fatal(err)
	}
	want := []string{".git/config (modified)", ".git/hooks/post-checkout (added)", ".git/hooks/pre-commit.sample (removed)"}
	if got := g.Changed(); !slices.Equal(got, want) {
		t.Errorf("changed = %v, want %v", got, want)
	}
	if data, err := os.ReadFile(filepath.Join(backup, ".git", "config")); err != nil || string(data) != "[core]\n" {
		t.Errorf("backup of .git/config = %q, %v", data, err)
	}
}
