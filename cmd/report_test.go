package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
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
	initTestGitRepo(t, projPath)
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
				CommonExecFlags: CommonExecFlags{Profile: "test"},
				Roles:           []string{"testing_basic"},
				DryRun:          true,
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
	initTestGitRepo(t, projPath)
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

	// Seed a report batch: security succeeded, testing_basic failed.
	db, err := calldb.Open(env.ProjectDBPath())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	now := time.Now()
	batch := "report-2026-04-01_10-00-00"

	secID, err := db.InsertCall(&calldb.Call{
		ProjectID: env.ProjectID(), Action: "report", Role: "security",
		Batch: batch, StartedAt: now.Add(-2 * time.Minute),
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
		Batch: batch, StartedAt: now.Add(-2 * time.Minute),
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
				CommonExecFlags: CommonExecFlags{Profile: "test"},
				RerunFailed:     true,
				DryRun:          true,
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

// TestReportRoleSelectionModes covers the two ways report picks roles:
//   - empty --roles            → enabled-only (always; --all does NOT apply to report)
//   - explicit --roles A,B     → those exact roles regardless of enabled state
func TestReportRoleSelectionModes(t *testing.T) {
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
		Name: "myproj",
		// security ON; testing_basic OFF (per InitProject's "everything not in
		// EnabledRoles → off" semantics for known roles).
		EnabledRoles: []string{"security"},
	}); err != nil {
		t.Fatalf("InitProject: %v", err)
	}

	savedOrg := orgFlag
	defer func() { orgFlag = savedOrg }()
	orgFlag = filepath.Dir(orgDir)

	cases := []struct {
		name     string
		opts     ReportOptions
		mustHave []string
		mustOmit []string
	}{
		{
			name:     "default → enabled-only",
			opts:     ReportOptions{CommonExecFlags: CommonExecFlags{Profile: "test"}, DryRun: true},
			mustHave: []string{"security"},
			mustOmit: []string{"testing_basic"},
		},
		{
			name:     "explicit --roles overrides enabled",
			opts:     ReportOptions{CommonExecFlags: CommonExecFlags{Profile: "test"}, DryRun: true, Roles: []string{"testing_basic"}},
			mustHave: []string{"testing_basic"},
			mustOmit: []string{"security"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var runErr error
			out := captureStdout(t, func() {
				withChdir(t, projPath, func() {
					runErr = runReport(tc.opts)
				})
			})
			if runErr != nil {
				t.Fatalf("runReport: %v", runErr)
			}
			// Dry-run output prints "Roles: a, b, c" — find that line.
			rolesLine := ""
			for _, line := range strings.Split(out, "\n") {
				if strings.HasPrefix(line, "Roles: ") {
					rolesLine = line
					break
				}
			}
			if rolesLine == "" {
				t.Fatalf("no 'Roles:' header in output:\n%s", out)
			}
			for _, want := range tc.mustHave {
				if !strings.Contains(rolesLine, want) {
					t.Errorf("expected %q in %q", want, rolesLine)
				}
			}
			for _, omit := range tc.mustOmit {
				// Match the role as a token, not a substring (testing_basic
				// vs testing_full both contain "testing").
				tokens := strings.Split(strings.TrimPrefix(rolesLine, "Roles: "), ", ")
				for _, tok := range tokens {
					if tok == omit {
						t.Errorf("%q should not appear in %q", omit, rolesLine)
					}
				}
			}
		})
	}
}

// TestReviewOptionsFromReportPropagation guards against the regression where
// `report --review --roles X` (plus runner/model overrides) silently dropped
// the role scope and overrides on the auto-triggered review step.
func TestReviewOptionsFromReportPropagation(t *testing.T) {
	in := ReportOptions{
		CommonExecFlags: CommonExecFlags{
			Timeout:         42,
			CheaperModel:    true,
			Profile:         "docker",
			Agent:           "claude",
			Verbose:         true,
			DockerAutoSetup: true,
			ContainerName:   "myctr",
			Model:           "gpt-5.4",
			Effort:          "high",
			// MaxBudgetUSD should NOT leak — verified below via want.
			MaxBudgetUSD: "1.50",
		},
		Roles: []string{"security"},
		Force: true,
		// Fields below should NOT leak into ReviewOptions: they're report-only
		// or have different semantics on the review side.
		Parallel:             4,
		Print:                true,
		DryRun:               true,
		IgnorePreviousReport: true,
		Review:               true,
		RerunFailed:          true,
		MaxBudgetBatch:       "10",
	}
	got := reviewOptionsFromReport(in)
	want := ReviewOptions{
		CommonExecFlags: CommonExecFlags{
			Timeout:         42,
			CheaperModel:    true,
			Profile:         "docker",
			Agent:           "claude",
			Verbose:         true,
			DockerAutoSetup: true,
			ContainerName:   "myctr",
			Model:           "gpt-5.4",
			Effort:          "high",
		},
		Roles: []string{"security"},
		Force: true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("propagation mismatch\n got: %+v\nwant: %+v", got, want)
	}

	// Empty ReportOptions must not clobber review defaults — a zero value in
	// goes to a zero value out so review's own defaulting still kicks in.
	if zero := reviewOptionsFromReport(ReportOptions{}); !reflect.DeepEqual(zero, ReviewOptions{}) {
		t.Errorf("empty ReportOptions should produce empty ReviewOptions, got %+v", zero)
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

	var runErr error
	captureStdout(t, func() {
		withChdir(t, projPath, func() {
			runErr = runReport(ReportOptions{
				CommonExecFlags: CommonExecFlags{Profile: "test"},
				RerunFailed:     true,
				Roles:           []string{"testing_basic"},
				DryRun:          true,
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

// TestPrintReportBodiesFiltersFailedRoles guards the --print fix in 591474f:
// when a role failed in the current run, printArtifact's on-disk fallback must
// NOT surface the previous-run report under the failed role's header.
//
// The bug: iterating rbs unconditionally let printArtifact read the stale file
// for a failed role (since outputByRole[failedRole] was an empty string, which
// triggered the path fallback). The fix skips roles missing from the success
// map.
func TestPrintReportBodiesFiltersFailedRoles(t *testing.T) {
	_, projPath, env := setupTestProject(t)

	// Pre-populate role B's on-disk report with a sentinel from a "prior run".
	// This is the staleness the fix must guard against — if the filter is
	// reverted, printArtifact reads this file and surfaces it.
	const sentinel = "PRIOR_RUN_REPORT_SENTINEL_DO_NOT_PRINT"
	sentinelPath := env.RoleReportPath("role_b")
	if err := os.MkdirAll(filepath.Dir(sentinelPath), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(sentinelPath, []byte(sentinel), 0644); err != nil {
		t.Fatalf("WriteFile sentinel: %v", err)
	}

	// Simulate a finished run: role_a succeeded, role_b failed.
	results := []runner.RunSummary{
		{RoleID: "role_a", Output: "ROLE_A_FRESH_OUTPUT"},
		{RoleID: "role_b", IsError: true, Err: errors.New("agent crashed")},
	}

	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			printReportBodies([]string{"role_a", "role_b"}, results, env)
		})
	})

	if strings.Contains(out, sentinel) {
		t.Errorf("sentinel from prior run leaked under failed role's header:\n%s", out)
	}
	if !strings.Contains(out, "role_a") || !strings.Contains(out, "ROLE_A_FRESH_OUTPUT") {
		t.Errorf("expected role_a body in output:\n%s", out)
	}
	// The failed role should not get a header at all — the current-run filter
	// short-circuits before the banner prints.
	if strings.Contains(out, "══════ role_b ══════") {
		t.Errorf("failed role should be skipped, but its header appeared:\n%s", out)
	}
}

// TestPrintReportBodiesFiltersSkippedRoles is the sibling of the failed case:
// roles that PreDispatch refused to dispatch arrive as skipped summaries
// (Err==nil, IsError==true) and must also be filtered out so the on-disk
// fallback doesn't surface a stale report for them.
func TestPrintReportBodiesFiltersSkippedRoles(t *testing.T) {
	_, projPath, env := setupTestProject(t)

	const sentinel = "STALE_FROM_PRIOR_BATCH"
	sentinelPath := env.RoleReportPath("role_skipped")
	if err := os.MkdirAll(filepath.Dir(sentinelPath), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(sentinelPath, []byte(sentinel), 0644); err != nil {
		t.Fatalf("WriteFile sentinel: %v", err)
	}

	// Skipped summaries mirror what runner/pool.go:skippedSummary produces:
	// IsError==true, Err==nil, ErrorSource=="skipped".
	results := []runner.RunSummary{
		{RoleID: "role_ok", Output: "FRESH"},
		{RoleID: "role_skipped", IsError: true, ErrorSource: agent.ErrorSourceSkipped},
	}

	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			printReportBodies([]string{"role_ok", "role_skipped"}, results, env)
		})
	})

	if strings.Contains(out, sentinel) {
		t.Errorf("sentinel from prior batch leaked for skipped role:\n%s", out)
	}
}

// TestShouldAutoReview locks in the gate from 591474f: --review must require
// not only failed==0 but also skipped==0, otherwise a PreDispatch budget skip
// would auto-trigger review over a partial report set.
func TestShouldAutoReview(t *testing.T) {
	cases := []struct {
		name      string
		reviewOpt bool
		failed    int
		skipped   int
		succeeded int
		want      bool
	}{
		{name: "review off",
			reviewOpt: false, failed: 0, skipped: 0, succeeded: 3, want: false},
		{name: "all succeeded → run review",
			reviewOpt: true, failed: 0, skipped: 0, succeeded: 3, want: true},
		{name: "any failed blocks review",
			reviewOpt: true, failed: 1, skipped: 0, succeeded: 2, want: false},
		{name: "any skipped blocks review (regression case for 591474f)",
			reviewOpt: true, failed: 0, skipped: 1, succeeded: 2, want: false},
		{name: "skipped + failed both block",
			reviewOpt: true, failed: 1, skipped: 1, succeeded: 1, want: false},
		{name: "succeeded == 0 blocks review even with no failure",
			reviewOpt: true, failed: 0, skipped: 0, succeeded: 0, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldAutoReview(tc.reviewOpt, tc.failed, tc.skipped, tc.succeeded)
			if got != tc.want {
				t.Errorf("shouldAutoReview(%v, failed=%d, skipped=%d, succeeded=%d) = %v, want %v",
					tc.reviewOpt, tc.failed, tc.skipped, tc.succeeded, got, tc.want)
			}
		})
	}
}
