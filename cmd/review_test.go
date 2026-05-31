package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ateam/internal/root"
)

func TestParseMaxAge(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"", 0, false},
		{"2h", 2 * time.Hour, false},
		{"30m", 30 * time.Minute, false},
		{"90s", 90 * time.Second, false},
		{"1d", 24 * time.Hour, false},
		{"7d", 7 * 24 * time.Hour, false},
		// Mixed units with d are rejected to keep semantics obvious.
		{"1d2h", 0, true},
		// Plain garbage.
		{"abc", 0, true},
		{"-1h", 0, true},
		// Plain "d" with no number is invalid.
		{"d", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := parseMaxAge(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseMaxAge(%q) err = %v, wantErr %v", tc.in, err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("parseMaxAge(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

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
	initTestGitRepo(t, projPath)
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
	if err := os.MkdirAll(filepath.Dir(reportPath), 0755); err != nil {
		t.Fatalf("MkdirAll role dir: %v", err)
	}
	if err := os.WriteFile(reportPath, []byte("# Findings\n\nsome findings"), 0644); err != nil {
		t.Fatalf("WriteFile report: %v", err)
	}

	savedOrg := orgFlag
	defer func() { orgFlag = savedOrg }()
	orgFlag = filepath.Dir(orgDir) // --org takes the parent of .ateamorg/

	var runErr error
	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			runErr = runReview(ReviewOptions{CommonExecFlags: CommonExecFlags{Profile: "test"}, DryRun: true})
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
	if err := os.MkdirAll(filepath.Dir(reportPath), 0755); err != nil {
		t.Fatalf("MkdirAll role dir: %v", err)
	}
	if err := os.WriteFile(reportPath, []byte(""), 0644); err != nil {
		t.Fatalf("WriteFile empty report: %v", err)
	}

	savedOrg := orgFlag
	defer func() { orgFlag = savedOrg }()
	orgFlag = filepath.Dir(orgDir) // --org takes the parent of .ateamorg/

	var runErr error
	captureStdout(t, func() {
		withChdir(t, projPath, func() {
			runErr = runReview(ReviewOptions{CommonExecFlags: CommonExecFlags{Profile: "test"}, DryRun: true})
		})
	})

	if runErr != nil {
		t.Fatalf("runReview with empty report: %v", runErr)
	}
}

// TestReviewStageHappyPath exercises the migrated runReview end-to-end
// against the mock agent: starting line, Stage Post chain emits Done +
// "Review:" pointer + the --print body.
func TestReviewStageHappyPath(t *testing.T) {
	orgDir, projPath, projDir := setupReviewFixture(t)

	// Seed a flat-layout report so DiscoverReports finds something.
	reportPath := filepath.Join(projDir, "shared", "report", "testing_basic.md")
	if err := os.MkdirAll(filepath.Dir(reportPath), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(reportPath, []byte("# Findings\n\nclean."), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	savedOrg := orgFlag
	defer func() { orgFlag = savedOrg }()
	orgFlag = filepath.Dir(orgDir)

	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			if err := runReview(ReviewOptions{CommonExecFlags: CommonExecFlags{Profile: "test"}, Print: true}); err != nil {
				t.Fatalf("runReview: %v", err)
			}
		})
	})

	for _, want := range []string{
		"Supervisor reviewing reports",
		"Done (",
		"Review:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}
