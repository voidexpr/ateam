package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/root"
)

func TestReportDryRun(t *testing.T) {
	base := t.TempDir()
	orgDir, err := root.InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}
	projPath := filepath.Join(base, "myproj")
	if err := os.MkdirAll(projPath, 0755); err != nil {
		t.Fatal(err)
	}
	projDir, err := root.InitProject(projPath, orgDir, root.InitProjectOpts{
		Name:         "myproj",
		EnabledRoles: []string{"testing_basic"},
	})
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}

	savedOrg := orgFlag
	defer func() { orgFlag = savedOrg }()
	orgFlag = filepath.Dir(orgDir) // --org takes the parent of .ateamorg/

	var runErr error
	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			runErr = runReport(ReportOptions{
				Roles:   []string{"testing_basic"},
				DryRun:  true,
				Profile: "test",
			})
		})
	})

	if runErr != nil {
		t.Fatalf("runReport dry-run: %v", runErr)
	}
	if !strings.Contains(out, "testing_basic") {
		t.Errorf("expected role name in dry-run output:\n%s", out)
	}

	// EnsureRoles creates the logs dir before the dry-run check.
	logsDir := filepath.Join(projDir, "logs", "roles", "testing_basic")
	if _, err := os.Stat(logsDir); err != nil {
		t.Errorf("expected logs dir %s to exist: %v", logsDir, err)
	}
}
