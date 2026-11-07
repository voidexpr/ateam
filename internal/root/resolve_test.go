package root

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ateam/internal/runtime"
)

// resolvedTempDir returns a t.TempDir() with symlinks resolved, so
// comparisons with discovered paths (which call filepath.EvalSymlinks)
// work on macOS where /tmp -> /private/tmp.
func resolvedTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", dir, err)
	}
	return resolved
}

// makeHome creates an isolated user ateam directory and points
// ATEAM_HOME at it so discovery never touches the real ~/.ateam.
func makeHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(resolvedTempDir(t), ".ateam")
	if err := EnsureHome(home); err != nil {
		t.Fatal(err)
	}
	t.Setenv(runtime.HomeEnv, home)
	return home
}

// makeLayer creates <root>/.ateam with the given ateam.hcl content (a
// config-only layer when it names no project).
func makeLayer(t *testing.T, root, hcl string) string {
	t.Helper()
	dir := filepath.Join(root, DirName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ConfigFile), []byte(hcl), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// makeProject creates <projectRoot>/.ateam naming the project.
func makeProject(t *testing.T, projectRoot, name string) string {
	t.Helper()
	return makeLayer(t, projectRoot, "project {\n  name = \""+name+"\"\n}\n")
}

func TestResolveFindsProjectWalkingUp(t *testing.T) {
	home := makeHome(t)
	base := resolvedTempDir(t)
	projectRoot := filepath.Join(base, "svc")
	projectDir := makeProject(t, projectRoot, "svc")
	sub := filepath.Join(projectRoot, "deep", "sub")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)

	env, err := Resolve("", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if env.ProjectDir != projectDir || env.ProjectName != "svc" || env.SourceDir != projectRoot {
		t.Errorf("project: dir=%q name=%q source=%q", env.ProjectDir, env.ProjectName, env.SourceDir)
	}
	if env.HomeDir != home {
		t.Errorf("HomeDir = %q, want %q", env.HomeDir, home)
	}
	if len(env.Layers) != 2 || env.Layers[0] != home || env.Layers[1] != projectDir {
		t.Errorf("Layers = %v, want [home, project]", env.Layers)
	}
	if env.StateDir() != projectDir || env.ProjectID() != "svc" {
		t.Errorf("StateDir=%q ProjectID=%q", env.StateDir(), env.ProjectID())
	}
	if env.Config == nil || env.Config.Project.Name != "svc" || env.Runtime == nil {
		t.Errorf("Config/Runtime not loaded: %+v", env.Config)
	}
}

func TestResolveAncestorLayersAndNearestProject(t *testing.T) {
	home := makeHome(t)
	base := resolvedTempDir(t)
	// base/.ateam: config-only layer (no project block)
	outer := makeLayer(t, base, `agent "claude" { model = "sonnet" }`)
	// base/mono/.ateam: a project
	monoDir := makeProject(t, filepath.Join(base, "mono"), "mono")
	// base/mono/svc/.ateam: a nearer project
	svcDir := makeProject(t, filepath.Join(base, "mono", "svc"), "svc")
	t.Chdir(filepath.Join(base, "mono", "svc"))

	env, err := Resolve("", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if env.ProjectDir != svcDir || env.ProjectName != "svc" {
		t.Errorf("nearest project must win: %q (%q)", env.ProjectDir, env.ProjectName)
	}
	want := []string{home, outer, monoDir, svcDir}
	if strings.Join(env.Layers, ",") != strings.Join(want, ",") {
		t.Errorf("Layers = %v, want %v", env.Layers, want)
	}
	if env.Runtime.Agents["claude"].Model != "sonnet" {
		t.Errorf("outer layer config not applied: %+v", env.Runtime.Agents["claude"].Model)
	}
	if got := env.ConfigLayers(); len(got) != 3 || got[2] != monoDir {
		t.Errorf("ConfigLayers = %v", got)
	}
}

func TestResolveScratchModeWithoutProject(t *testing.T) {
	home := makeHome(t)
	t.Chdir(resolvedTempDir(t))

	env, err := Resolve("", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if env.ProjectDir != "" || env.HasProject() {
		t.Errorf("expected scratch mode, got project %q", env.ProjectDir)
	}
	if env.StateDir() != filepath.Join(home, runtime.StateSubdir) {
		t.Errorf("scratch StateDir = %q", env.StateDir())
	}
	if len(env.Layers) != 1 || env.Layers[0] != home {
		t.Errorf("Layers = %v", env.Layers)
	}
	if _, err := Resolve("", resolvedTempDir(t)); err == nil {
		t.Error("--project pointing at a non-project must error")
	}
}

func TestHomeIsNeverAProject(t *testing.T) {
	home := makeHome(t)
	if err := os.WriteFile(filepath.Join(home, ConfigFile), []byte(`project { name = "home" }`), 0644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Dir(home))
	env, err := Resolve("", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if env.ProjectDir != "" {
		t.Errorf("home dir must not be a project, got %q", env.ProjectDir)
	}
}

func TestResolveHomeOverrideAndProjectOverride(t *testing.T) {
	makeHome(t)
	otherHome := filepath.Join(resolvedTempDir(t), "h")
	base := resolvedTempDir(t)
	projectDir := makeProject(t, filepath.Join(base, "p"), "p")
	t.Chdir(resolvedTempDir(t))

	for _, override := range []string{filepath.Join(base, "p"), projectDir, filepath.Join(base, "p", "x", "y")} {
		if err := os.MkdirAll(override, 0755); err != nil {
			t.Fatal(err)
		}
		env, err := Resolve(otherHome, override)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", override, err)
		}
		if env.ProjectDir != projectDir || env.HomeDir != otherHome {
			t.Errorf("override %q: project=%q home=%q", override, env.ProjectDir, env.HomeDir)
		}
	}
}

func TestResolveRejectsLegacyLayout(t *testing.T) {
	makeHome(t)
	base := resolvedTempDir(t)
	if err := os.MkdirAll(filepath.Join(base, ".ateamorg"), 0755); err != nil {
		t.Fatal(err)
	}
	makeProject(t, filepath.Join(base, "p"), "p")
	t.Chdir(filepath.Join(base, "p"))
	if _, err := Resolve("", ""); err == nil || !strings.Contains(err.Error(), "legacy layout") {
		t.Errorf("expected legacy layout error for .ateamorg, got %v", err)
	}

	base2 := resolvedTempDir(t)
	dir := makeProject(t, base2, "p2")
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[project]\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(base2)
	if _, err := Resolve("", ""); err == nil || !strings.Contains(err.Error(), "config.toml") {
		t.Errorf("expected legacy layout error for config.toml, got %v", err)
	}
}

func TestResolveMalformedConfigReturnsError(t *testing.T) {
	makeHome(t)
	base := resolvedTempDir(t)
	makeLayer(t, base, "project { name = ")
	t.Chdir(base)
	if _, err := Resolve("", ""); err == nil {
		t.Fatal("expected an error for an unparsable ateam.hcl")
	}
}

func TestLookupFromSeedsWorkDirFromStart(t *testing.T) {
	makeHome(t)
	base := resolvedTempDir(t)
	makeProject(t, base, "p")
	t.Chdir(resolvedTempDir(t))

	env, err := LookupFrom(base)
	if err != nil {
		t.Fatalf("LookupFrom: %v", err)
	}
	if env.WorkDir != base || env.ProjectName != "p" {
		t.Errorf("WorkDir = %q name=%q, want start path %q", env.WorkDir, env.ProjectName, base)
	}
}

func TestResolveStreamPath(t *testing.T) {
	if got := ResolveStreamPath("/state", "logs/3/agent.jsonl"); got != "/state/logs/3/agent.jsonl" {
		t.Errorf("relative: %q", got)
	}
	if got := ResolveStreamPath("/state", "/abs/agent.jsonl"); got != "/abs/agent.jsonl" {
		t.Errorf("absolute passes through: %q", got)
	}
	if got := ResolveStreamPath("", "logs/3/agent.jsonl"); got != "logs/3/agent.jsonl" {
		t.Errorf("no state dir: %q", got)
	}
	if !IsLegacyStreamFile("x_stream.jsonl") || IsLegacyStreamFile("logs/3/agent.jsonl") {
		t.Error("IsLegacyStreamFile")
	}
}

func TestResolveWorkDirAndGitRepoDir(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git CLI required")
	}

	t.Run("WorkDir defaults to cwd; GitRepoDir is empty outside a repo", func(t *testing.T) {
		env := &ResolvedEnv{}
		if err := env.resolveWorkDir(""); err != nil {
			t.Fatalf("resolveWorkDir: %v", err)
		}
		cwd, _ := os.Getwd()
		if env.WorkDir != realPath(cwd) {
			t.Errorf("WorkDir = %q, want realPath(cwd) %q", env.WorkDir, realPath(cwd))
		}
		tmp := resolvedTempDir(t)
		t.Chdir(tmp)
		env2 := &ResolvedEnv{}
		if err := env2.resolveWorkDir(""); err != nil {
			t.Fatalf("resolveWorkDir: %v", err)
		}
		if env2.GitRepoDir != "" {
			t.Errorf("GitRepoDir = %q, want \"\" (non-repo tmp dir)", env2.GitRepoDir)
		}
	})

	t.Run("OverrideWorkDir sets both WorkDir and GitRepoDir", func(t *testing.T) {
		tmp := gitRepoDir(t)
		env := &ResolvedEnv{}
		if err := env.OverrideWorkDir(tmp); err != nil {
			t.Fatalf("OverrideWorkDir: %v", err)
		}
		if env.WorkDir != tmp || env.GitRepoDir != tmp {
			t.Errorf("WorkDir=%q GitRepoDir=%q, want %q", env.WorkDir, env.GitRepoDir, tmp)
		}
		sub := filepath.Join(tmp, "deep", "sub")
		if err := os.MkdirAll(sub, 0755); err != nil {
			t.Fatal(err)
		}
		if err := env.OverrideWorkDir(sub); err != nil {
			t.Fatalf("OverrideWorkDir(sub): %v", err)
		}
		if env.WorkDir != sub || env.GitRepoDir != tmp {
			t.Errorf("WorkDir=%q GitRepoDir=%q, want sub %q and repo %q", env.WorkDir, env.GitRepoDir, sub, tmp)
		}
	})
}

func gitRepoDir(t *testing.T) string {
	t.Helper()
	tmp := resolvedTempDir(t)
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		c := exec.Command("git", args...)
		c.Dir = tmp
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return tmp
}

// TestNewProjectInfoParamsCachesMeta verifies that multi-role commands which
// call NewProjectInfoParams N times only fork git log/status once.
func TestNewProjectInfoParamsCachesMeta(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git CLI required")
	}
	tmp := gitRepoDir(t)
	env := &ResolvedEnv{}
	if err := env.OverrideWorkDir(tmp); err != nil {
		t.Fatalf("OverrideWorkDir: %v", err)
	}
	if env.projectMeta != nil {
		t.Fatal("projectMeta should start nil")
	}
	p1 := env.NewProjectInfoParams("role one", "report")
	if env.projectMeta == nil {
		t.Fatal("projectMeta should be cached after first call")
	}
	cached := env.projectMeta
	p2 := env.NewProjectInfoParams("role two", "report")
	if env.projectMeta != cached {
		t.Errorf("second call re-ran GetProjectMeta (cached %p, now %p)", cached, env.projectMeta)
	}
	if p1.Meta == nil || p2.Meta == nil || p1.Meta != p2.Meta {
		t.Errorf("Meta should be populated once and shared (p1=%v, p2=%v)", p1.Meta, p2.Meta)
	}

	other := resolvedTempDir(t)
	if err := env.OverrideWorkDir(other); err != nil {
		t.Fatalf("OverrideWorkDir(other): %v", err)
	}
	if env.projectMeta != nil || env.quickOrientation != nil {
		t.Error("OverrideWorkDir to a new path should clear the caches")
	}
}

func TestNewProjectInfoParamsQuickOrientation(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git CLI required")
	}
	tmp := gitRepoDir(t)

	t.Run("always populated for a git repo", func(t *testing.T) {
		env := &ResolvedEnv{}
		if err := env.OverrideWorkDir(tmp); err != nil {
			t.Fatalf("OverrideWorkDir: %v", err)
		}
		p1 := env.NewProjectInfoParams("role one", "report")
		if !strings.Contains(p1.QuickOrientation, "## Quick orientation") {
			t.Errorf("QuickOrientation missing expected header:\n%s", p1.QuickOrientation)
		}
		cached := env.quickOrientation
		p2 := env.NewProjectInfoParams("role two", "report")
		if env.quickOrientation != cached || p1.QuickOrientation != p2.QuickOrientation {
			t.Error("second call re-collected quickOrientation")
		}
	})

	t.Run("non-git tree does not invent commit hash or clean status", func(t *testing.T) {
		env := &ResolvedEnv{}
		if err := env.OverrideWorkDir(resolvedTempDir(t)); err != nil {
			t.Fatalf("OverrideWorkDir: %v", err)
		}
		p := env.NewProjectInfoParams("role", "report")
		if !strings.Contains(p.QuickOrientation, "## Quick orientation") {
			t.Fatalf("QuickOrientation missing header for non-git tree:\n%s", p.QuickOrientation)
		}
		if regexp.MustCompile(`\b[0-9a-f]{40}\b`).MatchString(p.QuickOrientation) {
			t.Errorf("non-git QuickOrientation contains a 40-hex hash:\n%s", p.QuickOrientation)
		}
		if strings.Contains(p.QuickOrientation, "Working tree: clean") {
			t.Errorf("non-git QuickOrientation falsely reports a clean working tree:\n%s", p.QuickOrientation)
		}
	})
}

func TestResolveWorkDirCanonicalisesSymlinks(t *testing.T) {
	tmp := resolvedTempDir(t)
	realDir := filepath.Join(tmp, "real")
	if err := os.MkdirAll(realDir, 0755); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(tmp, "link")
	if err := os.Symlink(realDir, symlink); err != nil {
		t.Fatal(err)
	}
	env := &ResolvedEnv{}
	if err := env.OverrideWorkDir(symlink); err != nil {
		t.Fatalf("OverrideWorkDir: %v", err)
	}
	if env.WorkDir != realDir {
		t.Errorf("WorkDir = %q, want realPath %q (symlink should resolve)", env.WorkDir, realDir)
	}
}

func TestRegistryRoundTrip(t *testing.T) {
	home := makeHome(t)
	entries, err := LoadRegistry(home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("empty registry: %v %v", entries, err)
	}
	if err := Register(home, RegistryEntry{Name: "b", Path: "/x/b", KeychainKey: "b-1"}); err != nil {
		t.Fatal(err)
	}
	if err := Register(home, RegistryEntry{Name: "a", Path: "/x/a"}); err != nil {
		t.Fatal(err)
	}
	if err := Register(home, RegistryEntry{Name: "b", Path: "/y/b", KeychainKey: "b-1"}); err != nil {
		t.Fatal(err)
	}
	entries, err = LoadRegistry(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Name != "a" || entries[1].Path != "/y/b" || entries[1].KeychainKey != "b-1" {
		t.Errorf("registry: %+v", entries)
	}
	if e, ok := LookupRegistry(home, "b"); !ok || e.Path != "/y/b" {
		t.Errorf("LookupRegistry: %+v %v", e, ok)
	}
	if err := Unregister(home, "a"); err != nil {
		t.Fatal(err)
	}
	if _, ok := LookupRegistry(home, "a"); ok {
		t.Error("a should be unregistered")
	}
}

func TestWalkProjectsSkipsMissingAndRenamed(t *testing.T) {
	home := makeHome(t)
	base := resolvedTempDir(t)
	for _, name := range []string{"one", "two"} {
		root := filepath.Join(base, name)
		if _, err := InitProject(root, home, InitProjectOpts{Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	if err := Register(home, RegistryEntry{Name: "gone", Path: filepath.Join(base, "gone")}); err != nil {
		t.Fatal(err)
	}
	var seen []string
	err := WalkProjects(home, func(p ProjectInfo) error {
		seen = append(seen, p.Name)
		if p.Config == nil || p.Config.Project.Name != p.Name || len(p.Layers) != 2 {
			t.Errorf("project %s: config %+v layers %v", p.Name, p.Config, p.Layers)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(seen, ",") != "one,two" {
		t.Errorf("walked %v, want one,two", seen)
	}
}
