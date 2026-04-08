package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/root"
)

func setupReviewFixture(t *testing.T) (orgDir, projPath, projDir string) {
	t.Helper()
	base := t.TempDir()
	var err error
	orgDir, err = root.InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}
	projPath = filepath.Join(base, "myproj")
	if err := os.MkdirAll(projPath, 0755); err != nil {
		t.Fatal(err)
	}
	projDir, err = root.InitProject(projPath, orgDir, root.InitProjectOpts{
		Name:         "myproj",
		EnabledRoles: []string{"testing_basic"},
	})
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	return
}

func TestReviewDryRun(t *testing.T) {
	orgDir, projPath, projDir := setupReviewFixture(t)

	// Create a non-empty report so AssembleReviewPrompt succeeds.
	reportPath := filepath.Join(projDir, "roles", "testing_basic", "report.md")
	if err := os.WriteFile(reportPath, []byte("# Findings\n\nsome findings"), 0644); err != nil {
		t.Fatalf("WriteFile report: %v", err)
	}

	savedOrg := orgFlag
	defer func() { orgFlag = savedOrg }()
	orgFlag = filepath.Dir(orgDir) // --org takes the parent of .ateamorg/

	var runErr error
	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			runErr = runReview(ReviewOptions{DryRun: true, Profile: "test"})
		})
	})

	if runErr != nil {
		t.Fatalf("runReview dry-run: %v", runErr)
	}
	if !strings.Contains(out, "Reports found:") {
		t.Errorf("expected 'Reports found:' in output:\n%s", out)
	}
	if !strings.Contains(out, "testing_basic") {
		t.Errorf("expected role name in output:\n%s", out)
	}
}

func TestReviewDryRunEmptyReport(t *testing.T) {
	orgDir, projPath, projDir := setupReviewFixture(t)

	// An empty report.md is discovered by DiscoverReports but has empty content.
	// AssembleReviewPrompt should handle it without panic.
	reportPath := filepath.Join(projDir, "roles", "testing_basic", "report.md")
	if err := os.WriteFile(reportPath, []byte(""), 0644); err != nil {
		t.Fatalf("WriteFile empty report: %v", err)
	}

	savedOrg := orgFlag
	defer func() { orgFlag = savedOrg }()
	orgFlag = filepath.Dir(orgDir) // --org takes the parent of .ateamorg/

	var runErr error
	captureStdout(t, func() {
		withChdir(t, projPath, func() {
			runErr = runReview(ReviewOptions{DryRun: true, Profile: "test"})
		})
	})

	if runErr != nil {
		t.Fatalf("runReview with empty report: %v", runErr)
	}
}
