package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/root"
)

// TestRunProjectsListsRegisteredProjects exercises the read-only `ateam
// projects` listing. After initializing a single project under an org we
// expect its name to appear in the output table.
func TestRunProjectsListsRegisteredProjects(t *testing.T) {
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

	var runErr error
	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			runErr = runProjects(nil, nil)
		})
	})
	if runErr != nil {
		t.Fatalf("runProjects: %v", runErr)
	}
	for _, want := range []string{"NAME", "PATH", "myproj"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in projects output:\n%s", want, out)
		}
	}
}
