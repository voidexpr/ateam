package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/root"
)

// The four commands below all expose --cheaper-model and --model. They route
// both flags through applyModelOverrides (table.go), which the unit test
// TestApplyModelOverrides already covers in detail. These per-command tests
// guard the *wiring*: they confirm each command's runXxx function actually
// invokes the helper when both flags are set, so a future refactor that drops
// the call cannot regress without a test failure.
//
// The assertion is on the stderr warning the helper emits when both flags are
// present — observable through the mock profile without having to inspect
// post-resolution agent state (MockAgent.SetModel is a no-op).

const combinedWarning = "--cheaper-model"

func setupMiniProject(t *testing.T, enabled []string) (orgDir, projPath, projDir string) {
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
	initTestGitRepo(t, projPath)
	projDir, err = root.InitProject(projPath, orgDir, root.InitProjectOpts{
		Name:         "myproj",
		EnabledRoles: enabled,
	})
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	return
}

func TestReportWarnsWhenCheaperAndModelBothSet(t *testing.T) {
	orgDir, projPath, _ := setupMiniProject(t, []string{"testing_basic"})

	savedOrg := orgFlag
	defer func() { orgFlag = savedOrg }()
	orgFlag = filepath.Dir(orgDir)

	var runErr error
	stderr := captureStderr(t, func() {
		captureStdout(t, func() {
			withChdir(t, projPath, func() {
				runErr = runReport(ReportOptions{
					CommonExecFlags: CommonExecFlags{
						Profile:      "test",
						CheaperModel: true,
						Model:        "opus-4",
					},
					Roles:  []string{"testing_basic"},
					DryRun: true,
				})
			})
		})
	})
	if runErr != nil {
		t.Fatalf("runReport: %v", runErr)
	}
	if !strings.Contains(stderr, combinedWarning) || !strings.Contains(stderr, "Warning:") {
		t.Errorf("expected --cheaper-model/--model warning on stderr, got: %q", stderr)
	}
}

func TestReviewWarnsWhenCheaperAndModelBothSet(t *testing.T) {
	orgDir, projPath, projDir := setupMiniProject(t, []string{"testing_basic"})
	// runReview needs at least one non-empty report on disk.
	reportPath := filepath.Join(projDir, "roles", "testing_basic", "report.md")
	if err := os.MkdirAll(filepath.Dir(reportPath), 0755); err != nil {
		t.Fatalf("MkdirAll role dir: %v", err)
	}
	if err := os.WriteFile(reportPath, []byte("# Findings\n\nsome findings"), 0644); err != nil {
		t.Fatalf("WriteFile report: %v", err)
	}

	savedOrg := orgFlag
	defer func() { orgFlag = savedOrg }()
	orgFlag = filepath.Dir(orgDir)

	var runErr error
	stderr := captureStderr(t, func() {
		captureStdout(t, func() {
			withChdir(t, projPath, func() {
				runErr = runReview(ReviewOptions{
					CommonExecFlags: CommonExecFlags{
						Profile:      "test",
						CheaperModel: true,
						Model:        "opus-4",
					},
				})
			})
		})
	})
	if runErr != nil {
		t.Fatalf("runReview: %v", runErr)
	}
	if !strings.Contains(stderr, combinedWarning) || !strings.Contains(stderr, "Warning:") {
		t.Errorf("expected --cheaper-model/--model warning on stderr, got: %q", stderr)
	}
}

func TestCodeWarnsWhenCheaperAndModelBothSet(t *testing.T) {
	orgDir, projPath, _ := setupMiniProject(t, []string{"testing_basic"})

	savedOrg := orgFlag
	defer func() { orgFlag = savedOrg }()
	orgFlag = filepath.Dir(orgDir)

	var runErr error
	stderr := captureStderr(t, func() {
		captureStdout(t, func() {
			withChdir(t, projPath, func() {
				runErr = runCode(CodeOptions{
					CommonExecFlags: CommonExecFlags{
						Profile:      "test",
						CheaperModel: true,
						Model:        "opus-4",
					},
					Review:            "# Test Review\n\nsome tasks",
					SupervisorProfile: "test",
				})
			})
		})
	})
	if runErr != nil {
		t.Fatalf("runCode: %v", runErr)
	}
	if !strings.Contains(stderr, combinedWarning) || !strings.Contains(stderr, "Warning:") {
		t.Errorf("expected --cheaper-model/--model warning on stderr, got: %q", stderr)
	}
}

func TestVerifyWarnsWhenCheaperAndModelBothSet(t *testing.T) {
	orgDir, projPath, _ := setupMiniProject(t, []string{"testing_basic"})

	savedOrg := orgFlag
	defer func() { orgFlag = savedOrg }()
	orgFlag = filepath.Dir(orgDir)

	var runErr error
	stderr := captureStderr(t, func() {
		captureStdout(t, func() {
			withChdir(t, projPath, func() {
				runErr = runVerify(VerifyOptions{
					CommonExecFlags: CommonExecFlags{
						Profile:      "test",
						CheaperModel: true,
						Model:        "opus-4",
					},
				})
			})
		})
	})
	if runErr != nil {
		t.Fatalf("runVerify: %v", runErr)
	}
	if !strings.Contains(stderr, combinedWarning) || !strings.Contains(stderr, "Warning:") {
		t.Errorf("expected --cheaper-model/--model warning on stderr, got: %q", stderr)
	}
}
