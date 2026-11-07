package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/root"
)

func TestRunInitCreatesProjectAndRegistersIt(t *testing.T) {
	base := t.TempDir()
	outside := t.TempDir()
	home := testHome(t, base)

	target := filepath.Join(base, "myproj")
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}

	withChdir(t, outside, func() {
		resetInitGlobals()
		captureStdout(t, func() {
			if err := runInit(nil, []string{target}); err != nil {
				t.Fatalf("runInit: %v", err)
			}
		})
	})

	env, err := root.LookupFrom(target)
	if err != nil {
		t.Fatalf("LookupFrom: %v", err)
	}
	if evalSymlinks(env.HomeDir) != evalSymlinks(home) {
		t.Fatalf("HomeDir = %q, want %q", env.HomeDir, home)
	}
	if evalSymlinks(env.ProjectDir) != evalSymlinks(filepath.Join(target, ".ateam")) {
		t.Fatalf("ProjectDir = %q, want %q", env.ProjectDir, filepath.Join(target, ".ateam"))
	}
	if env.Config.Project.Name != "myproj" {
		t.Fatalf("project name = %q, want %q", env.Config.Project.Name, "myproj")
	}
	if entry, ok := root.LookupRegistry(home, "myproj"); !ok || evalSymlinks(entry.Path) != evalSymlinks(target) {
		t.Fatalf("registry entry = %+v (ok=%v), want path %q", entry, ok, target)
	}
	data, err := os.ReadFile(filepath.Join(target, ".ateam", root.ConfigFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `name = "myproj"`) || !strings.Contains(string(data), `role "code.bugs"`) {
		t.Errorf("unexpected ateam.hcl:\n%s", data)
	}
}

func TestRunInitNameFlagAndValidation(t *testing.T) {
	base := t.TempDir()
	home := testHome(t, base)

	target := filepath.Join(base, "Some Dir")
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}
	withChdir(t, target, func() {
		resetInitGlobals()
		if err := runInit(nil, nil); err == nil {
			t.Fatal("expected an error for a directory name that is not a valid project name")
		}
		resetInitGlobals()
		initName = "svc"
		captureStdout(t, func() {
			if err := runInit(nil, nil); err != nil {
				t.Fatalf("runInit --name: %v", err)
			}
		})
	})
	if _, ok := root.LookupRegistry(home, "svc"); !ok {
		t.Error("project svc should be registered")
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
	initName = ""
	initRoles = nil
	initAutoSetup = false
	initAgentConfig = ""
}
