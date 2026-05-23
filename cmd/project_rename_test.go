package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/config"
	"github.com/ateam/internal/root"
)

// savedProjectRenameGlobals snapshots the flags runProjectRename reads so
// tests can mutate them without leaking state across cases.
type savedProjectRenameGlobals struct {
	oldPath string
	newPath string
	dryRun  bool
	orgFlag string
}

func saveProjectRenameGlobals() savedProjectRenameGlobals {
	return savedProjectRenameGlobals{
		oldPath: renameOldPath,
		newPath: renameNewPath,
		dryRun:  renameDryRun,
		orgFlag: orgFlag,
	}
}

func (s savedProjectRenameGlobals) restore() {
	renameOldPath = s.oldPath
	renameNewPath = s.newPath
	renameDryRun = s.dryRun
	orgFlag = s.orgFlag
}

// TestProjectRenameDryRunPrintsPlan exercises the explicit rename path in
// dry-run mode: the command should print the intended move with a [dry-run]
// prefix and must not touch the filesystem.
func TestProjectRenameDryRunPrintsPlan(t *testing.T) {
	base := t.TempDir()
	orgDir, err := root.InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}

	oldRel := "services/api"
	newRel := "backends/api"
	oldID := config.PathToProjectID(oldRel)
	newID := config.PathToProjectID(newRel)

	oldStateDir := filepath.Join(orgDir, "projects", oldID)
	if err := os.MkdirAll(oldStateDir, 0755); err != nil {
		t.Fatal(err)
	}

	saved := saveProjectRenameGlobals()
	defer saved.restore()
	renameOldPath = oldRel
	renameNewPath = newRel
	renameDryRun = true
	orgFlag = base

	var runErr error
	out := captureStdout(t, func() {
		runErr = runProjectRename(nil, nil)
	})
	if runErr != nil {
		t.Fatalf("runProjectRename: %v", runErr)
	}

	for _, want := range []string{"[dry-run]", oldRel, newRel, oldID, newID} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in dry-run output:\n%s", want, out)
		}
	}

	// Dry-run must not move anything.
	if _, err := os.Stat(oldStateDir); err != nil {
		t.Errorf("dry-run deleted old state dir: %v", err)
	}
	newStateDir := filepath.Join(orgDir, "projects", newID)
	if _, err := os.Stat(newStateDir); !os.IsNotExist(err) {
		t.Errorf("dry-run created new state dir: stat err = %v", err)
	}
}

// TestProjectRenameRenamesStateDir verifies that an explicit --old/--new
// rename actually moves the legacy state directory under
// .ateamorg/projects/. This is the on-disk mutation the dry-run skips.
func TestProjectRenameRenamesStateDir(t *testing.T) {
	base := t.TempDir()
	orgDir, err := root.InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}

	oldRel := "services/api"
	newRel := "backends/api"
	oldID := config.PathToProjectID(oldRel)
	newID := config.PathToProjectID(newRel)

	oldStateDir := filepath.Join(orgDir, "projects", oldID)
	if err := os.MkdirAll(filepath.Join(oldStateDir, "supervisor", "logs"), 0755); err != nil {
		t.Fatal(err)
	}
	// Drop a sentinel file so we can confirm contents — not just the empty dir
	// — moved together with the rename.
	sentinel := filepath.Join(oldStateDir, "supervisor", "logs", "marker")
	if err := os.WriteFile(sentinel, []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}

	saved := saveProjectRenameGlobals()
	defer saved.restore()
	renameOldPath = oldRel
	renameNewPath = newRel
	renameDryRun = false
	orgFlag = base

	var runErr error
	captureStdout(t, func() {
		runErr = runProjectRename(nil, nil)
	})
	if runErr != nil {
		t.Fatalf("runProjectRename: %v", runErr)
	}

	if _, err := os.Stat(oldStateDir); !os.IsNotExist(err) {
		t.Errorf("old state dir still present after rename: stat err = %v", err)
	}
	newStateDir := filepath.Join(orgDir, "projects", newID)
	if info, err := os.Stat(newStateDir); err != nil || !info.IsDir() {
		t.Fatalf("expected new state dir %s: stat err = %v", newStateDir, err)
	}
	movedSentinel := filepath.Join(newStateDir, "supervisor", "logs", "marker")
	if _, err := os.Stat(movedSentinel); err != nil {
		t.Errorf("sentinel file did not move with rename: %v", err)
	}
}

// TestProjectRenameNoFlagsReregistersCurrentProject covers the no-flags path:
// after a project's state dir has been wiped (e.g. because the project was
// moved on disk), `ateam project-rename` should recreate it under the org's
// projects/<id>/ directory.
func TestProjectRenameNoFlagsReregistersCurrentProject(t *testing.T) {
	base := t.TempDir()
	orgDir, err := root.InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}
	projPath := filepath.Join(base, "myproj")
	if err := os.MkdirAll(projPath, 0755); err != nil {
		t.Fatal(err)
	}
	initTestGitRepo(t, projPath)
	if _, err := root.InitProject(projPath, orgDir, root.InitProjectOpts{
		Name:         "myproj",
		EnabledRoles: []string{"testing_basic"},
	}); err != nil {
		t.Fatalf("InitProject: %v", err)
	}

	// Simulate a project whose state dir is missing (i.e. it was moved and
	// the org link needs re-establishing). InitProject created one already.
	projectID := config.PathToProjectID("myproj")
	stateDir := filepath.Join(orgDir, "projects", projectID)
	if err := os.RemoveAll(stateDir); err != nil {
		t.Fatal(err)
	}

	saved := saveProjectRenameGlobals()
	defer saved.restore()
	renameOldPath = ""
	renameNewPath = ""
	renameDryRun = false
	orgFlag = base

	var runErr error
	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			runErr = runProjectRename(nil, nil)
		})
	})
	if runErr != nil {
		t.Fatalf("runProjectRename: %v", runErr)
	}

	if info, err := os.Stat(stateDir); err != nil || !info.IsDir() {
		t.Fatalf("expected state dir %s after re-registration: stat err = %v", stateDir, err)
	}
	if !strings.Contains(out, "Registered project") {
		t.Errorf("expected confirmation message in output:\n%s", out)
	}
}
