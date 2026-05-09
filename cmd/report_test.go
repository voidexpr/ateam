package cmd

import (
	"os"
	"path/filepath"
	"reflect"
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
			opts:     ReportOptions{DryRun: true, Profile: "test"},
			mustHave: []string{"security"},
			mustOmit: []string{"testing_basic"},
		},
		{
			name:     "explicit --roles overrides enabled",
			opts:     ReportOptions{DryRun: true, Profile: "test", Roles: []string{"testing_basic"}},
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
		Roles:           []string{"security"},
		ExtraPrompt:     "focus on auth",
		Timeout:         42,
		CheaperModel:    true,
		Profile:         "docker",
		Agent:           "claude",
		Verbose:         true,
		Force:           true,
		DockerAutoSetup: true,
		ContainerName:   "myctr",
		Model:           "gpt-5.4",
		Effort:          "high",
		// Fields below should NOT leak into ReviewOptions: they're report-only
		// or have different semantics on the review side.
		Parallel:             4,
		Print:                true,
		DryRun:               true,
		IgnorePreviousReport: true,
		Review:               true,
		RerunFailed:          true,
		MaxBudgetUSD:         "1.50",
		MaxBudgetBatch:       "10",
	}
	got := reviewOptionsFromReport(in)
	want := ReviewOptions{
		Roles:           []string{"security"},
		ExtraPrompt:     "focus on auth",
		Timeout:         42,
		CheaperModel:    true,
		Profile:         "docker",
		Agent:           "claude",
		Verbose:         true,
		Force:           true,
		DockerAutoSetup: true,
		ContainerName:   "myctr",
		Model:           "gpt-5.4",
		Effort:          "high",
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
