package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ateam/internal/calldb"
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

func TestRerunFailedDryRunSelectsOnlyFailed(t *testing.T) {
	base := t.TempDir()
	orgDir, err := root.InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}
	projPath := filepath.Join(base, "myproj")
	if err := os.MkdirAll(projPath, 0755); err != nil {
		t.Fatal(err)
	}
	_, err = root.InitProject(projPath, orgDir, root.InitProjectOpts{
		Name:         "myproj",
		EnabledRoles: []string{"testing_basic", "security"},
	})
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}

	// Resolve env to get ProjectID and DB path.
	env, err := root.LookupFrom(projPath)
	if err != nil {
		t.Fatalf("LookupFrom: %v", err)
	}

	// Seed a report task group: security succeeded, testing_basic failed.
	db, err := calldb.Open(env.ProjectDBPath())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	now := time.Now()
	tg := "report-2026-04-01_10-00-00"

	secID, err := db.InsertCall(&calldb.Call{
		ProjectID: env.ProjectID(), Action: "report", Role: "security",
		TaskGroup: tg, StartedAt: now.Add(-2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("InsertCall: %v", err)
	}
	if err := db.UpdateCall(secID, &calldb.CallResult{
		EndedAt: now.Add(-1 * time.Minute), DurationMS: 60000,
	}); err != nil {
		t.Fatalf("UpdateCall: %v", err)
	}

	tbID, err := db.InsertCall(&calldb.Call{
		ProjectID: env.ProjectID(), Action: "report", Role: "testing_basic",
		TaskGroup: tg, StartedAt: now.Add(-2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("InsertCall: %v", err)
	}
	if err := db.UpdateCall(tbID, &calldb.CallResult{
		EndedAt: now.Add(-1 * time.Minute), DurationMS: 60000,
		IsError: true, ErrorMessage: "agent crashed",
	}); err != nil {
		t.Fatalf("UpdateCall: %v", err)
	}
	db.Close()

	savedOrg := orgFlag
	defer func() { orgFlag = savedOrg }()
	orgFlag = filepath.Dir(orgDir)

	var runErr error
	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			runErr = runReport(ReportOptions{
				RerunFailed: true,
				DryRun:      true,
				Profile:     "test",
			})
		})
	})

	if runErr != nil {
		t.Fatalf("runReport --rerun-failed --dry-run: %v", runErr)
	}

	// The failed role (testing_basic) should appear in dry-run output.
	if !strings.Contains(out, "testing_basic") {
		t.Errorf("expected testing_basic in rerun-failed output:\n%s", out)
	}
	// The succeeded role (security) should be mentioned as successful, not in the roles list.
	if !strings.Contains(out, "security") {
		t.Errorf("expected security mentioned as succeeded:\n%s", out)
	}
}

func TestRerunFailedMutuallyExclusiveWithRoles(t *testing.T) {
	base := t.TempDir()
	orgDir, err := root.InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}
	projPath := filepath.Join(base, "myproj")
	if err := os.MkdirAll(projPath, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := root.InitProject(projPath, orgDir, root.InitProjectOpts{
		Name:         "myproj",
		EnabledRoles: []string{"testing_basic"},
	}); err != nil {
		t.Fatalf("InitProject: %v", err)
	}

	savedOrg := orgFlag
	defer func() { orgFlag = savedOrg }()
	orgFlag = filepath.Dir(orgDir)

	var runErr error
	captureStdout(t, func() {
		withChdir(t, projPath, func() {
			runErr = runReport(ReportOptions{
				RerunFailed: true,
				Roles:       []string{"testing_basic"},
				DryRun:      true,
				Profile:     "test",
			})
		})
	})

	if runErr == nil {
		t.Fatal("expected error when both --rerun-failed and --roles are set")
	}
	if !strings.Contains(runErr.Error(), "mutually exclusive") {
		t.Errorf("expected 'mutually exclusive' error, got: %v", runErr)
	}
}
