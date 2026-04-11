package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/root"
)

func TestAllRunsAllThreePhases(t *testing.T) {
	// runAll does not expose a DryRun option. We use profile "test" which
	// resolves to a mock agent, and verify all three phase headers appear
	// in the output — confirming that report, review, and code are invoked.
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
	} {
		if !strings.Contains(out, header) {
			t.Errorf("expected %q in output:\n%s", header, out)
		}
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
