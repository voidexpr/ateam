package root

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/config"
	"github.com/ateam/internal/promptdata"
	"github.com/ateam/internal/runtime"
)

func TestEnsureHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".ateam")
	if err := EnsureHome(home); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{home, filepath.Join(home, runtime.StateSubdir), filepath.Join(home, "prompts"), filepath.Join(home, ConfigFile)} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s to exist: %v", p, err)
		}
	}
	if runtime.ProjectName(home) != "" {
		t.Error("the starter home ateam.hcl must not name a project")
	}
	if err := os.WriteFile(filepath.Join(home, ConfigFile), []byte("// mine\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureHome(home); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(home, ConfigFile)); string(data) != "// mine\n" {
		t.Error("EnsureHome must keep an existing ateam.hcl")
	}
	if err := EnsureHome(""); err == nil {
		t.Error("empty home must error")
	}
}

func TestInitProject(t *testing.T) {
	home := makeHome(t)
	projectPath := filepath.Join(resolvedTempDir(t), "myproject")
	if err := os.MkdirAll(projectPath, 0755); err != nil {
		t.Fatal(err)
	}
	enabled := promptdata.AllRoleIDs[:2]

	projDir, err := InitProject(projectPath, home, InitProjectOpts{Name: "test-project", EnabledRoles: enabled})
	if err != nil {
		t.Fatalf("InitProject failed: %v", err)
	}
	if projDir != filepath.Join(projectPath, DirName) {
		t.Errorf("projDir = %q", projDir)
	}
	for _, p := range []string{"state.sqlite", ".gitignore", ConfigFile, "logs"} {
		if _, err := os.Stat(filepath.Join(projDir, p)); err != nil {
			t.Errorf("expected %s: %v", p, err)
		}
	}

	t.Chdir(projectPath)
	env, err := Resolve("", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	cfg := env.Config
	if cfg.Project.Name != "test-project" || env.ProjectName != "test-project" {
		t.Errorf("project name = %q", cfg.Project.Name)
	}
	defaults := runtime.DefaultSettings()
	if cfg.Report.MaxParallel != defaults.Report.MaxParallel || cfg.Report.ReportTimeoutMinutes != defaults.Report.ReportTimeoutMinutes {
		t.Errorf("timeouts must come from the built-in defaults: %+v", cfg.Report)
	}
	enabledSet := map[string]bool{}
	for _, id := range enabled {
		enabledSet[id] = true
	}
	for _, id := range promptdata.AllRoleIDs {
		want := config.RoleDisabled
		if enabledSet[id] {
			want = config.RoleEnabled
		}
		if cfg.Roles[id] != want {
			t.Errorf("role %s = %q, want %q", id, cfg.Roles[id], want)
		}
	}

	entry, ok := LookupRegistry(home, "test-project")
	if !ok || entry.Path != projectPath || entry.KeychainKey == "" {
		t.Errorf("registry entry: %+v (ok=%v)", entry, ok)
	}
	if env.KeychainKey != entry.KeychainKey {
		t.Errorf("env.KeychainKey = %q, want %q", env.KeychainKey, entry.KeychainKey)
	}
}

func TestInitProjectDefaultsNameAndRoles(t *testing.T) {
	home := makeHome(t)
	projectPath := filepath.Join(resolvedTempDir(t), "svc-api")
	if err := os.MkdirAll(projectPath, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := InitProject(projectPath, home, InitProjectOpts{}); err != nil {
		t.Fatal(err)
	}
	if runtime.ProjectName(filepath.Join(projectPath, DirName)) != "svc-api" {
		t.Error("name must default to the directory name")
	}
	data, _ := os.ReadFile(filepath.Join(projectPath, DirName, ConfigFile))
	if !strings.Contains(string(data), `role "code.bugs"`) || !strings.Contains(string(data), `status = "on"`) {
		t.Errorf("roles must be listed with their default status:\n%s", data)
	}
}

func TestInitProjectErrors(t *testing.T) {
	home := makeHome(t)
	base := resolvedTempDir(t)

	bad := filepath.Join(base, "Bad Name")
	if err := os.MkdirAll(bad, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := InitProject(bad, home, InitProjectOpts{}); err == nil {
		t.Error("invalid default name must error")
	}

	first := filepath.Join(base, "first")
	if _, err := InitProject(first, home, InitProjectOpts{Name: "dup"}); err != nil {
		t.Fatal(err)
	}
	if _, err := InitProject(first, home, InitProjectOpts{Name: "dup"}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("re-init of an existing project: %v", err)
	}
	second := filepath.Join(base, "second")
	if _, err := InitProject(second, home, InitProjectOpts{Name: "dup"}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("duplicate name in another dir: %v", err)
	}

	// A registry entry whose directory is gone no longer blocks the name.
	if err := os.RemoveAll(first); err != nil {
		t.Fatal(err)
	}
	if _, err := InitProject(second, home, InitProjectOpts{Name: "dup"}); err != nil {
		t.Errorf("stale registry entry must not block: %v", err)
	}
}

func TestEnsureProjectGitignore(t *testing.T) {
	t.Run("creates file when missing", func(t *testing.T) {
		dir := t.TempDir()
		if err := EnsureProjectGitignore(dir); err != nil {
			t.Fatalf("EnsureProjectGitignore: %v", err)
		}
		data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		for _, want := range projectGitignoreEntries {
			if !strings.Contains(string(data), want) {
				t.Errorf("missing %q in fresh .gitignore:\n%s", want, data)
			}
		}
	})

	t.Run("appends missing entries, preserves user content", func(t *testing.T) {
		dir := t.TempDir()
		const userContent = "# my notes\nlocal_scratch/\n"
		stale := userContent + "state.sqlite\nstate.sqlite-wal\nlogs/\ncache/\nsecrets.env\n"
		path := filepath.Join(dir, ".gitignore")
		if err := os.WriteFile(path, []byte(stale), 0644); err != nil {
			t.Fatal(err)
		}
		if err := EnsureProjectGitignore(dir); err != nil {
			t.Fatalf("EnsureProjectGitignore: %v", err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		got := string(data)
		for _, want := range []string{"runtime/", "state.sqlite-shm", "# my notes", "local_scratch/"} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %q:\n%s", want, got)
			}
		}
	})

	t.Run("no-op when all entries present", func(t *testing.T) {
		dir := t.TempDir()
		if err := WriteProjectGitignore(dir); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, ".gitignore")
		before, _ := os.ReadFile(path)
		if err := EnsureProjectGitignore(dir); err != nil {
			t.Fatal(err)
		}
		after, _ := os.ReadFile(path)
		if string(before) != string(after) {
			t.Errorf("file changed when all entries already present:\nbefore: %q\nafter: %q", before, after)
		}
	})
}
