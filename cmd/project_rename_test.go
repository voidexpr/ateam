package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runtime"
)

type savedProjectRenameGlobals struct {
	newName  string
	dryRun   bool
	homeFlag string
}

func saveProjectRenameGlobals() savedProjectRenameGlobals {
	return savedProjectRenameGlobals{newName: renameNewName, dryRun: renameDryRun, homeFlag: homeFlag}
}

func (s savedProjectRenameGlobals) restore() {
	renameNewName = s.newName
	renameDryRun = s.dryRun
	homeFlag = s.homeFlag
}

func initRenameFixture(t *testing.T) (home, projPath string) {
	t.Helper()
	base := t.TempDir()
	home = testHome(t, base)
	projPath = filepath.Join(base, "myproj")
	if err := os.MkdirAll(projPath, 0755); err != nil {
		t.Fatal(err)
	}
	initTestGitRepo(t, projPath)
	if _, err := root.InitProject(projPath, home, root.InitProjectOpts{
		Name:         "myproj",
		EnabledRoles: []string{"testing_basic"},
	}); err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	return home, projPath
}

// TestProjectRenameReregistersAfterMove: a moved project is found at its new
// path and the registry entry is updated to it.
func TestProjectRenameReregistersAfterMove(t *testing.T) {
	home, projPath := initRenameFixture(t)
	moved := filepath.Join(filepath.Dir(projPath), "moved")
	if err := os.Rename(projPath, moved); err != nil {
		t.Fatal(err)
	}

	saved := saveProjectRenameGlobals()
	defer saved.restore()
	renameNewName = ""
	renameDryRun = false
	homeFlag = home

	var runErr error
	out := captureStdout(t, func() {
		withChdir(t, moved, func() {
			runErr = runProjectRename(nil, nil)
		})
	})
	if runErr != nil {
		t.Fatalf("runProjectRename: %v", runErr)
	}
	if !strings.Contains(out, "Update registry entry") {
		t.Errorf("expected the registry update in the output:\n%s", out)
	}
	entry, ok := root.LookupRegistry(home, "myproj")
	if !ok || evalSymlinks(entry.Path) != evalSymlinks(moved) {
		t.Errorf("registry entry = %+v (ok=%v), want path %q", entry, ok, moved)
	}
}

// TestProjectRenameRenamesProject: --name rewrites the project block, the
// registry, and leaves nothing under the old name. Dry-run changes nothing.
func TestProjectRenameRenamesProject(t *testing.T) {
	home, projPath := initRenameFixture(t)
	configPath := filepath.Join(projPath, ".ateam", root.ConfigFile)

	saved := saveProjectRenameGlobals()
	defer saved.restore()
	homeFlag = home
	renameNewName = "backend"

	renameDryRun = true
	captureStdout(t, func() {
		withChdir(t, projPath, func() {
			if err := runProjectRename(nil, nil); err != nil {
				t.Fatalf("dry-run: %v", err)
			}
		})
	})
	if runtime.ProjectName(filepath.Dir(configPath)) != "myproj" {
		t.Fatal("dry-run must not rename the project")
	}

	renameDryRun = false
	captureStdout(t, func() {
		withChdir(t, projPath, func() {
			if err := runProjectRename(nil, nil); err != nil {
				t.Fatalf("rename: %v", err)
			}
		})
	})
	if got := runtime.ProjectName(filepath.Dir(configPath)); got != "backend" {
		t.Errorf("project name after rename = %q", got)
	}
	if _, ok := root.LookupRegistry(home, "myproj"); ok {
		t.Error("old name must be unregistered")
	}
	if entry, ok := root.LookupRegistry(home, "backend"); !ok || evalSymlinks(entry.Path) != evalSymlinks(projPath) {
		t.Errorf("new registry entry = %+v (ok=%v)", entry, ok)
	}

	renameNewName = "Bad Name"
	withChdir(t, projPath, func() {
		if err := runProjectRename(nil, nil); err == nil {
			t.Error("invalid name must error")
		}
	})
}
