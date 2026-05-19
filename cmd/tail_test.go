package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/root"
)

func saveTailGlobals() func() {
	reports, coding, last, verbose, nc := tailReports, tailCoding, tailLast, tailVerbose, tailNoColor
	return func() {
		tailReports = reports
		tailCoding = coding
		tailLast = last
		tailVerbose = verbose
		tailNoColor = nc
	}
}

// TestTailNoRunningExits exercises the graceful empty-state path: with no
// rows in the project DB, `ateam tail --coding` should return a "no coding
// session" error rather than hanging on the 30-second discovery wait.
func TestTailNoRunningExits(t *testing.T) {
	defer saveTailGlobals()()

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

	savedOrg := orgFlag
	defer func() { orgFlag = savedOrg }()
	orgFlag = filepath.Dir(orgDir)

	tailCoding = true
	tailReports = false
	tailLast = false

	var runErr error
	withChdir(t, projPath, func() {
		runErr = runTail(nil, nil)
	})

	if runErr == nil {
		t.Fatal("expected error for empty coding session, got nil")
	}
	if !strings.Contains(runErr.Error(), "no coding session") {
		t.Errorf("expected 'no coding session' message, got: %v", runErr)
	}
}
