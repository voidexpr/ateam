package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ateam/internal/config"
	"github.com/ateam/internal/root"
)

func TestRunInitFindsOrgFromTargetPath(t *testing.T) {
	base := t.TempDir()
	outside := t.TempDir()

	orgDir, err := root.InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}

	target := filepath.Join(base, "myproj")
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}

	withChdir(t, outside, func() {
		resetInitGlobals()
		if err := runInit(nil, []string{target}); err != nil {
			t.Fatalf("runInit: %v", err)
		}
	})

	env, err := root.LookupFrom(target)
	if err != nil {
		t.Fatalf("LookupFrom: %v", err)
	}
	if evalSymlinks(env.OrgDir) != evalSymlinks(orgDir) {
		t.Fatalf("OrgDir = %q, want %q", env.OrgDir, orgDir)
	}
	if evalSymlinks(env.ProjectDir) != evalSymlinks(filepath.Join(target, ".ateam")) {
		t.Fatalf("ProjectDir = %q, want %q", env.ProjectDir, filepath.Join(target, ".ateam"))
	}

	cfg, err := config.Load(env.ProjectDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if cfg.Project.Name != "myproj" {
		t.Fatalf("project.name = %q, want %q", cfg.Project.Name, "myproj")
	}
}

func TestRunInitPrefersProjectLocalOrg(t *testing.T) {
	base := t.TempDir()
	outside := t.TempDir()

	parentOrgDir, err := root.InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg(base): %v", err)
	}

	target := filepath.Join(base, "myproj")
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}

	localOrgDir, err := root.InstallOrg(target)
	if err != nil {
		t.Fatalf("InstallOrg(target): %v", err)
	}

	withChdir(t, outside, func() {
		resetInitGlobals()
		if err := runInit(nil, []string{target}); err != nil {
			t.Fatalf("runInit: %v", err)
		}
	})

	env, err := root.LookupFrom(target)
	if err != nil {
		t.Fatalf("LookupFrom: %v", err)
	}
	if evalSymlinks(env.OrgDir) != evalSymlinks(localOrgDir) {
		t.Fatalf("OrgDir = %q, want %q", env.OrgDir, localOrgDir)
	}

	cfg, err := config.Load(env.ProjectDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if cfg.Project.Name != "myproj" {
		t.Fatalf("project.name = %q, want %q", cfg.Project.Name, "myproj")
	}

	if _, err := os.Stat(filepath.Join(parentOrgDir, "projects", "myproj")); !os.IsNotExist(err) {
		t.Fatalf("expected parent org to remain unused, got stat err=%v", err)
	}
}

func withChdir(t *testing.T, dir string, fn func()) {
	t.Helper()

	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(%q): %v", dir, err)
	}
	defer func() {
		if err := os.Chdir(oldwd); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	}()

	fn()
}

func resetInitGlobals() {
	initGitRemote = ""
	initName = ""
	initRoles = nil
	initOrgCreate = ""
	initOrgHome = false
	initAutoSetup = false
	initOrgCreatePrompt = false
}
