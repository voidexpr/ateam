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
	// Resolved, as a real home is: the temp folder is reached through a link.
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
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
		t.Errorf("an unknown filter name must be refused, got %v", err)
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

// A grant written through a link reopens a rule on the real path: the
// working folder is often reached through one (~/projects -> elsewhere).
func TestGrantThroughLinkReopensExclude(t *testing.T) {
	in := testInput(t)
	link := filepath.Join(in.Home, "link")
	if err := os.Symlink(in.WorkDir, link); err != nil {
		t.Fatal(err)
	}
	in.Read = []string{filepath.Join(link, ".ateam", "ateam.hcl")}
	p, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(filepath.Join(in.WorkDir, ".ateam", "ateam.hcl"))
	for _, ex := range paths(p.Exclude) {
		if resolved, _ := filepath.EvalSymlinks(ex); resolved == real {
			t.Errorf("%s is still excluded after a read through a link", ex)
		}
	}
	if !slices.Contains(paths(p.Read), real) {
		t.Errorf("read = %v, want the real path %s", paths(p.Read), real)
	}
}

// A read that reopens an excluded path inside a writable folder stays
// read-only, and so do the repository's hooks and config: Seatbelt rules,
// with the folders above pinned so the path cannot be moved away.
func TestNonoKeepsReadOnlyInsideWritableFolder(t *testing.T) {
	in := testInput(t)
	if err := os.MkdirAll(filepath.Join(in.WorkDir, ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	hcl := filepath.Join(in.ProjectDir, "ateam.hcl")
	in.Read = []string{hcl, in.AteamHome}
	p, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	prof, err := RenderNono(p, NonoInfo{}, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`(deny file-write* (subpath "` + hcl + `"))`,
		`(deny file-write* (literal "` + in.ProjectDir + `"))`, // .ateam cannot be renamed away
		`(deny file-write* (subpath "` + filepath.Join(in.WorkDir, ".git", "hooks") + `"))`,
		`(deny file-write* (subpath "` + filepath.Join(in.WorkDir, ".git", "config") + `"))`,
		`(deny file-write* (literal "` + filepath.Join(in.WorkDir, ".git") + `"))`,
	} {
		if !slices.Contains(prof.UnsafeSeatbelt, want) {
			t.Errorf("missing rule %s in %v", want, prof.UnsafeSeatbelt)
		}
	}
	for _, rule := range prof.UnsafeSeatbelt {
		if strings.Contains(rule, `"`+in.AteamHome+`"`) || strings.Contains(rule, `(literal "`+in.WorkDir+`")`) {
			t.Errorf("no rule on a path outside a writable folder, or on the folder itself: %s", rule)
		}
	}

	in.Read, in.Write = nil, []string{hcl} // opened fully, on purpose
	p, _ = Build(in)
	prof, _ = RenderNono(p, NonoInfo{}, "darwin")
	for _, rule := range prof.UnsafeSeatbelt {
		if strings.Contains(rule, "ateam.hcl") {
			t.Errorf("an explicit write is not read-only: %s", rule)
		}
	}
	if got := seatbeltString(`a"b\c`); got != `"a\"b\\c"` {
		t.Errorf("seatbeltString = %s", got)
	}
}

func TestAteamFilesOpen(t *testing.T) {
	in := testInput(t)
	in.OpenAteam = true
	p, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, ex := range paths(p.Exclude) {
		if Within(ex, in.AteamHome) || Within(ex, in.ProjectDir) {
			t.Errorf("ateam's files must be open: %s is excluded", ex)
		}
	}
	if w := paths(p.Write); !slices.Contains(w, in.AteamHome) || !slices.Contains(w, in.ProjectDir) {
		t.Errorf("ateam's directories must be writable: %v", w)
	}
}

// A grant that names the project's .ateam opens ateam's own rules below
// it; a grant of the whole project does not.
func TestGrantOfAteamDirOpensItsEntries(t *testing.T) {
	in := testInput(t)
	in.Write = []string{in.ProjectDir}
	p, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	ex := paths(p.Exclude)
	if slices.Contains(ex, filepath.Join(in.ProjectDir, "ateam.hcl")) {
		t.Errorf("write of .ateam opens ateam.hcl: %v", ex)
	}
	if !slices.Contains(ex, in.AteamHome) {
		t.Error("the user's ateam directory stays closed")
	}

	in.Write = []string{in.WorkDir + "/sub", in.Home + "/elsewhere"}
	p, _ = Build(in)
	if !slices.Contains(paths(p.Exclude), filepath.Join(in.ProjectDir, "ateam.hcl")) {
		t.Error("a grant outside .ateam opens nothing of ateam's")
	}
}

// A grant inside the user's ateam directory (an unattended run's output)
// closes the directory entry by entry around it: no path is opened inside
// a folder that stays excluded, which fence could not express.
func TestAteamHomeIsSplitAroundAGrant(t *testing.T) {
	in := testInput(t)
	state := filepath.Join(in.AteamHome, "state")
	for _, dir := range []string{"runtime", "agents/claude", "agents/codex", "logs"} {
		if err := os.MkdirAll(filepath.Join(state, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	p, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(paths(p.Exclude), in.AteamHome) {
		t.Fatalf("without a grant inside, the directory is closed as a whole: %v", paths(p.Exclude))
	}

	in.Write = []string{filepath.Join(state, "runtime"), filepath.Join(state, "agents", "claude")}
	p, err = Build(in)
	if err != nil {
		t.Fatal(err)
	}
	ex := paths(p.Exclude)
	for _, want := range []string{
		filepath.Join(in.AteamHome, "ateam.hcl"),   // closed before it exists
		filepath.Join(in.AteamHome, "secrets.env"), // closed before it exists
		filepath.Join(state, "state.sqlite"),
		filepath.Join(state, "logs"),
		filepath.Join(state, "agents", "codex"), // another agent's config
	} {
		if !slices.Contains(ex, want) {
			t.Errorf("%s must stay excluded: %v", want, ex)
		}
	}
	for _, grant := range paths(p.Write) {
		for _, path := range ex {
			if Within(grant, path) {
				t.Errorf("the grant %s lies inside the excluded %s", grant, path)
			}
		}
	}
	if _, err := RenderFence(p, "darwin"); err != nil {
		t.Errorf("fence renders it: %v", err)
	}
}
