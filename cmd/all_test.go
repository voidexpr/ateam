package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/root"
)

func TestAllRunsAllFourPhases(t *testing.T) {
	// runAll does not expose a DryRun option. We use profile "test" which
	// resolves to a mock agent, and verify all four phase headers appear
	// in the output — confirming that report, review, code, and verify
	// are invoked. Verify is the default; --no-verify skips it.
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

	// Save and restore all package-level flags that runAll reads.
	savedQuiet, savedTimeout, savedParallel := allQuiet, allTimeout, allParallel
	savedCheaper, savedVerbose := allCheaperModel, allVerbose
	savedRoles, savedProfile := allRoles, allProfile
	savedRP, savedRA := allReportProfile, allReportAgent
	savedSP, savedSA := allSupervisorProfile, allSupervisorAgent
	savedCP, savedCA := allCodeProfile, allCodeAgent
	savedDocker := allDockerAutoSetup
	savedEP := allExtraPrompt
	defer func() {
		allQuiet, allTimeout, allParallel = savedQuiet, savedTimeout, savedParallel
		allCheaperModel, allVerbose = savedCheaper, savedVerbose
		allRoles, allProfile = savedRoles, savedProfile
		allReportProfile, allReportAgent = savedRP, savedRA
		allSupervisorProfile, allSupervisorAgent = savedSP, savedSA
		allCodeProfile, allCodeAgent = savedCP, savedCA
		allDockerAutoSetup = savedDocker
		allExtraPrompt = savedEP
	}()

	allQuiet = false
	allRoles = []string{"testing_basic"}
	allReportProfile = "test"
	allSupervisorProfile = "test"
	allCodeProfile = "test"

	var runErr error
	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			runErr = runAll(nil, nil)
		})
	})

	if runErr != nil {
		t.Fatalf("runAll with mock agent: %v", runErr)
	}

	for _, header := range []string{
		"=== Phase 1: Report ===",
		"=== Phase 2: Review ===",
		"=== Phase 3: Code ===",
		"=== Phase 4: Verify ===",
	} {
		if !strings.Contains(out, header) {
			t.Errorf("expected %q in output:\n%s", header, out)
		}
	}
}

func TestAllNoVerifyStopsAfterCode(t *testing.T) {
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

	savedRoles, savedNoVerify := allRoles, allNoVerify
	savedRP, savedSP, savedCP := allReportProfile, allSupervisorProfile, allCodeProfile
	defer func() {
		allRoles, allNoVerify = savedRoles, savedNoVerify
		allReportProfile, allSupervisorProfile, allCodeProfile = savedRP, savedSP, savedCP
	}()

	allRoles = []string{"testing_basic"}
	allReportProfile = "test"
	allSupervisorProfile = "test"
	allCodeProfile = "test"
	allNoVerify = true

	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			_ = runAll(nil, nil)
		})
	})

	if strings.Contains(out, "Phase 4: Verify") {
		t.Errorf("--no-verify should suppress Phase 4 header, got:\n%s", out)
	}
}

func TestAllDefaultRoles(t *testing.T) {
	// When allRoles is empty, runAll should default to []string{"all"}.
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

	savedQuiet, savedRoles := allQuiet, allRoles
	savedRP, savedSP, savedCP := allReportProfile, allSupervisorProfile, allCodeProfile
	defer func() {
		allQuiet, allRoles = savedQuiet, savedRoles
		allReportProfile, allSupervisorProfile, allCodeProfile = savedRP, savedSP, savedCP
	}()
	allQuiet = false
	allRoles = nil // should default to "all"
	allReportProfile = "test"
	allSupervisorProfile = "test"
	allCodeProfile = "test"

	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			_ = runAll(nil, nil)
		})
	})

	// The report phase should be invoked (Phase 1 header appears).
	if !strings.Contains(out, "=== Phase 1: Report ===") {
		t.Errorf("expected Phase 1 header in output:\n%s", out)
	}
}

// TestAllVerifyRunCount guards against the historical bug where `ateam all`
// ran verify twice (once via runCode's auto-chain and once in Phase 4) and
// where --no-verify failed to suppress the auto-chain. We count occurrences
// of the unique line that runVerify prints on entry.
func TestAllVerifyRunCount(t *testing.T) {
	const verifyMarker = "Supervisor verifying recent code changes"

	setupProject := func(t *testing.T) string {
		t.Helper()
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
		t.Cleanup(func() { orgFlag = savedOrg })
		orgFlag = filepath.Dir(orgDir)
		return projPath
	}

	savedRoles, savedNoVerify := allRoles, allNoVerify
	savedRP, savedSP, savedCP := allReportProfile, allSupervisorProfile, allCodeProfile
	defer func() {
		allRoles, allNoVerify = savedRoles, savedNoVerify
		allReportProfile, allSupervisorProfile, allCodeProfile = savedRP, savedSP, savedCP
	}()
	allRoles = []string{"testing_basic"}
	allReportProfile = "test"
	allSupervisorProfile = "test"
	allCodeProfile = "test"

	t.Run("default runs verify exactly once", func(t *testing.T) {
		projPath := setupProject(t)
		allNoVerify = false
		out := captureStdout(t, func() {
			withChdir(t, projPath, func() {
				if err := runAll(nil, nil); err != nil {
					t.Fatalf("runAll: %v", err)
				}
			})
		})
		if got := strings.Count(out, verifyMarker); got != 1 {
			t.Errorf("expected verify to run exactly once, got %d:\n%s", got, out)
		}
	})

	t.Run("--no-verify runs verify zero times", func(t *testing.T) {
		projPath := setupProject(t)
		allNoVerify = true
		out := captureStdout(t, func() {
			withChdir(t, projPath, func() {
				if err := runAll(nil, nil); err != nil {
					t.Fatalf("runAll: %v", err)
				}
			})
		})
		if got := strings.Count(out, verifyMarker); got != 0 {
			t.Errorf("expected verify to run zero times with --no-verify, got %d:\n%s", got, out)
		}
	})
}

func TestCoalesce(t *testing.T) {
	tests := []struct {
		name string
		vals []string
		want string
	}{
		{"first non-empty", []string{"a", "b"}, "a"},
		{"second non-empty", []string{"", "b"}, "b"},
		{"all empty", []string{"", "", ""}, ""},
		{"no values", nil, ""},
		{"single value", []string{"x"}, "x"},
		{"single empty", []string{""}, ""},
		{"skip empties", []string{"", "", "c"}, "c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := coalesce(tt.vals...)
			if got != tt.want {
				t.Errorf("coalesce(%v) = %q, want %q", tt.vals, got, tt.want)
			}
		})
	}
}
