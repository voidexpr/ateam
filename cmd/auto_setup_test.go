package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/root"
)

func saveAutoSetupGlobals() func() {
	profile, ag := autoSetupProfile, autoSetupAgent
	verbose, dryRun, timeout := autoSetupVerbose, autoSetupDryRun, autoSetupTimeout
	return func() {
		autoSetupProfile = profile
		autoSetupAgent = ag
		autoSetupVerbose = verbose
		autoSetupDryRun = dryRun
		autoSetupTimeout = timeout
	}
}

// TestAutoSetupDryRun walks the --dry-run path: assemble the auto-setup
// prompt, print it inside the auto-setup banner, return. No real agent or
// runner setup is performed.
func TestAutoSetupDryRun(t *testing.T) {
	defer saveAutoSetupGlobals()()

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

	autoSetupDryRun = true

	var runErr error
	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			runErr = runAutoSetup(nil, nil)
		})
	})
	if runErr != nil {
		t.Fatalf("runAutoSetup --dry-run: %v", runErr)
	}
	if !strings.Contains(out, "auto-setup") {
		t.Errorf("expected 'auto-setup' banner in dry-run output:\n%s", out)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatal("expected non-empty dry-run output")
	}
}

// TestAutoSetupStageHappyPath exercises the migrated runAutoSetup against
// the mock agent: starting line, Stage emits Done + stream body.
func TestAutoSetupStageHappyPath(t *testing.T) {
	defer saveAutoSetupGlobals()()

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

	autoSetupProfile = "test" // mock agent

	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			if err := runAutoSetup(nil, nil); err != nil {
				t.Fatalf("runAutoSetup: %v", err)
			}
		})
	})
	for _, want := range []string{
		"Auto-setup running",
		"Done (",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "mock") {
		t.Errorf("expected mock-agent stream body in output:\n%s", out)
	}
}
