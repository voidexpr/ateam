package cmd

import (
	"fmt"
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
	// are all invoked. Verify always runs as the final phase.
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

	// Save and restore all package-level flags that runAll reads.
	savedQuiet, savedTimeout, savedParallel := runAllQuiet, runAllTimeout, runAllParallel
	savedCheaper, savedVerbose := runAllCheaperModel, runAllVerbose
	savedRoles, savedProfile := runAllRoles, runAllProfile
	savedRP, savedRA := runAllReportProfile, runAllReportAgent
	savedSP, savedSA := runAllSupervisorProfile, runAllSupervisorAgent
	savedCP, savedCA := runAllCodeProfile, runAllCodeAgent
	savedDocker := runAllDockerAutoSetup
	defer func() {
		runAllQuiet, runAllTimeout, runAllParallel = savedQuiet, savedTimeout, savedParallel
		runAllCheaperModel, runAllVerbose = savedCheaper, savedVerbose
		runAllRoles, runAllProfile = savedRoles, savedProfile
		runAllReportProfile, runAllReportAgent = savedRP, savedRA
		runAllSupervisorProfile, runAllSupervisorAgent = savedSP, savedSA
		runAllCodeProfile, runAllCodeAgent = savedCP, savedCA
		runAllDockerAutoSetup = savedDocker
	}()

	runAllQuiet = false
	runAllRoles = []string{"testing_basic"}
	runAllReportProfile = "test"
	runAllSupervisorProfile = "test"
	runAllCodeProfile = "test"

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

func TestAllDefaultRoles(t *testing.T) {
	// When runAllRoles is empty, runAll should default to []string{"all"}.
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

	savedQuiet, savedRoles := runAllQuiet, runAllRoles
	savedRP, savedSP, savedCP := runAllReportProfile, runAllSupervisorProfile, runAllCodeProfile
	defer func() {
		runAllQuiet, runAllRoles = savedQuiet, savedRoles
		runAllReportProfile, runAllSupervisorProfile, runAllCodeProfile = savedRP, savedSP, savedCP
	}()
	runAllQuiet = false
	runAllRoles = nil // should default to "all"
	runAllReportProfile = "test"
	runAllSupervisorProfile = "test"
	runAllCodeProfile = "test"

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

// TestAllVerifyRunsExactlyOnce guards against the historical bug where
// `ateam run-all` ran verify twice (once via runCode's auto-chain and once in
// Phase 4). The auto-chain is gone now; the test asserts the single Phase 4
// run remains. We count occurrences of the unique line that runVerify
// prints on entry.
func TestAllVerifyRunsExactlyOnce(t *testing.T) {
	const verifyMarker = "Supervisor verifying recent code changes"

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
	t.Cleanup(func() { orgFlag = savedOrg })
	orgFlag = filepath.Dir(orgDir)

	savedRoles := runAllRoles
	savedRP, savedSP, savedCP := runAllReportProfile, runAllSupervisorProfile, runAllCodeProfile
	defer func() {
		runAllRoles = savedRoles
		runAllReportProfile, runAllSupervisorProfile, runAllCodeProfile = savedRP, savedSP, savedCP
	}()
	runAllRoles = []string{"testing_basic"}
	runAllReportProfile = "test"
	runAllSupervisorProfile = "test"
	runAllCodeProfile = "test"

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
}

// TestCodeStopsAfterCodePhase verifies that `ateam code` no longer chains
// verify automatically. The auto-chain was removed because users invoking
// `ateam code` directly want to inspect the changes before verifying; the
// chained pipeline lives in `ateam run-all`.
func TestCodeStopsAfterCodePhase(t *testing.T) {
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
	t.Cleanup(func() { orgFlag = savedOrg })
	orgFlag = filepath.Dir(orgDir)

	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			_ = runCode(CodeOptions{
				CommonExecFlags:   CommonExecFlags{Profile: "test"},
				Review:            "# Test Review\n\nsome tasks",
				SupervisorProfile: "test",
			})
		})
	})
	if strings.Contains(out, "Supervisor verifying recent code changes") {
		t.Errorf("ateam code should not chain verify; got verify entry line in output:\n%s", out)
	}
}

// TestAllPropagatesModelAndBudgetFlags verifies that --model, --effort,
// --max-budget-usd, and --max-budget-usd-batch flow from the `ateam run-all`
// flags into every sub-command's *Options literal. We exercise the flag-
// combination warning ("--cheaper-model and --model both set") that the
// shared helper emits — its appearance once per phase in stderr proves
// the Model+CheaperModel pair reached each *Options. The remaining flags
// are checked via the registered cobra flags on the command itself.
func TestAllPropagatesModelAndBudgetFlags(t *testing.T) {
	for _, name := range []string{"model", "effort", "max-budget-usd", "max-budget-usd-batch"} {
		if runAllCmd.Flags().Lookup(name) == nil {
			t.Errorf("expected --%s registered on `ateam run-all`", name)
		}
	}

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

	saved := struct {
		roles               []string
		rp, sp, cp          string
		cheaper             bool
		model, effort       string
		budget, budgetBatch string
		quiet               bool
	}{
		runAllRoles, runAllReportProfile, runAllSupervisorProfile, runAllCodeProfile,
		runAllCheaperModel, runAllModel, runAllEffort, runAllMaxBudgetUSD, runAllMaxBudgetBatch,
		runAllQuiet,
	}
	defer func() {
		runAllRoles = saved.roles
		runAllReportProfile, runAllSupervisorProfile, runAllCodeProfile = saved.rp, saved.sp, saved.cp
		runAllCheaperModel = saved.cheaper
		runAllModel, runAllEffort = saved.model, saved.effort
		runAllMaxBudgetUSD, runAllMaxBudgetBatch = saved.budget, saved.budgetBatch
		runAllQuiet = saved.quiet
	}()
	runAllRoles = []string{"testing_basic"}
	runAllReportProfile = "test"
	runAllSupervisorProfile = "test"
	runAllCodeProfile = "test"
	runAllCheaperModel = true
	runAllModel = "opus-4"
	runAllEffort = "high"
	runAllMaxBudgetUSD = "10"
	runAllMaxBudgetBatch = "50"
	runAllQuiet = true

	var runErr error
	stderr := captureStderr(t, func() {
		captureStdout(t, func() {
			withChdir(t, projPath, func() {
				runErr = runAll(nil, nil)
			})
		})
	})
	if runErr != nil {
		t.Fatalf("runAll: %v", runErr)
	}

	got := strings.Count(stderr, combinedWarning)
	if got < 4 {
		t.Errorf("expected the --cheaper-model/--model warning at least 4 times "+
			"(once per phase), got %d:\n%s", got, stderr)
	}
}

// TestAllAutoRolesPlanOnlySkipsAllPhases verifies the user-visible contract:
// with --auto-roles --plan-only, the planner runs exactly once, its rationale
// reaches stdout, and none of the report/review/code/verify phases execute.
func TestAllAutoRolesPlanOnlySkipsAllPhases(t *testing.T) {
	const rationaleMarker = "PLANNER_RATIONALE_FIXED_MARKER"

	savedRunAutoRoles := runAutoRoles
	defer func() { runAutoRoles = savedRunAutoRoles }()

	var callCount int
	runAutoRoles = func(env *root.ResolvedEnv, profile, agentName string, verbose, planOnly, dockerAutoSetup bool) ([]string, bool, error) {
		callCount++
		fmt.Println(rationaleMarker)
		return nil, true, nil
	}

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

	savedRoles, savedAuto, savedPlanOnly := runAllRoles, runAllAutoRoles, runAllPlanOnly
	defer func() {
		runAllRoles, runAllAutoRoles, runAllPlanOnly = savedRoles, savedAuto, savedPlanOnly
	}()
	runAllRoles = nil
	runAllAutoRoles = true
	runAllPlanOnly = true

	var runErr error
	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			runErr = runAll(nil, nil)
		})
	})
	if runErr != nil {
		t.Fatalf("runAll: %v", runErr)
	}

	if callCount != 1 {
		t.Errorf("expected planner to run exactly once, got %d", callCount)
	}
	if !strings.Contains(out, rationaleMarker) {
		t.Errorf("expected planner output %q in stdout, got:\n%s", rationaleMarker, out)
	}
	for _, header := range []string{
		"=== Phase 1: Report ===",
		"=== Phase 2: Review ===",
		"=== Phase 3: Code ===",
		"=== Phase 4: Verify ===",
	} {
		if strings.Contains(out, header) {
			t.Errorf("did not expect %q in plan-only output, got:\n%s", header, out)
		}
	}
}

// TestAutoRolesAndRolesMutuallyExclusive verifies runAll rejects the
// combination of --auto-roles with explicit --roles before doing any work.
func TestAutoRolesAndRolesMutuallyExclusive(t *testing.T) {
	savedRoles, savedAuto, savedPlanOnly := runAllRoles, runAllAutoRoles, runAllPlanOnly
	defer func() {
		runAllRoles, runAllAutoRoles, runAllPlanOnly = savedRoles, savedAuto, savedPlanOnly
	}()
	runAllRoles = []string{"testing_basic"}
	runAllAutoRoles = true
	runAllPlanOnly = false

	err := runAll(nil, nil)
	if err == nil {
		t.Fatalf("expected error when --auto-roles and --roles are both set, got nil")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected mutual-exclusion error, got: %v", err)
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
