package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/root"
)

// setupTestProject creates a temp org + project and returns paths.
// The project has state.sqlite (created by InitProject) and mock-compatible config.
func setupTestProject(t *testing.T) (orgParent, projPath string, env *root.ResolvedEnv) {
	t.Helper()
	base := t.TempDir()
	orgDir, err := root.InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}
	projPath = filepath.Join(base, "testproj")
	if err := os.MkdirAll(projPath, 0755); err != nil {
		t.Fatal(err)
	}
	_, err = root.InitProject(projPath, orgDir, root.InitProjectOpts{
		Name:         "testproj",
		EnabledRoles: []string{"testing_basic"},
	})
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	env, err = root.LookupFrom(projPath)
	if err != nil {
		t.Fatalf("LookupFrom: %v", err)
	}
	return base, projPath, env
}

// TestInitCreatesDBAndLogDirs verifies that ateam init creates state.sqlite
// and all expected log directories including logs/run/.
func TestInitCreatesDBAndLogDirs(t *testing.T) {
	_, _, env := setupTestProject(t)

	// DB must exist
	dbPath := env.ProjectDBPath()
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("state.sqlite not created by init: %v", err)
	}

	// DB must have the proper schema (agent_execs table)
	db, err := calldb.Open(dbPath)
	if err != nil {
		t.Fatalf("cannot open DB: %v", err)
	}
	defer db.Close()
	rows, err := db.RecentRuns(calldb.RecentFilter{Limit: 1})
	if err != nil {
		t.Fatalf("query agent_execs failed (bad schema?): %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows in fresh DB, got %d", len(rows))
	}

	// Log dirs must exist
	for _, sub := range []string{"supervisor", "run"} {
		dir := filepath.Join(env.ProjectDir, "logs", sub)
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("logs/%s not created: %v", sub, err)
		}
	}
}

// TestRunWithMockAgent verifies init → run → ps flow using the mock agent.
func TestRunWithMockAgent(t *testing.T) {
	orgParent, projPath, _ := setupTestProject(t)

	saved := saveRunGlobals()
	defer saved.restore()
	orgFlag = orgParent
	runProfile = "test" // uses mock agent
	runQuiet = true
	runNoStream = true

	var runErr error
	captureStdout(t, func() {
		withChdir(t, projPath, func() {
			runErr = runRun(nil, []string{"hello mock"})
		})
	})
	if runErr != nil {
		t.Fatalf("runRun: %v", runErr)
	}

	// ps should show the run
	savedPS := savePSGlobals()
	defer savedPS.restore()
	orgFlag = orgParent

	var psErr error
	psOut := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			psErr = runRuns(nil, nil)
		})
	})
	if psErr != nil {
		t.Fatalf("ps: %v", psErr)
	}
	if !strings.Contains(psOut, "run") {
		t.Errorf("ps output should contain action 'run':\n%s", psOut)
	}
	if !strings.Contains(psOut, "ok") {
		t.Errorf("ps output should show status 'ok':\n%s", psOut)
	}

	// cost should show data
	var costErr error
	costOut := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			costErr = runCost(nil, nil)
		})
	})
	if costErr != nil {
		t.Fatalf("cost: %v", costErr)
	}
	if !strings.Contains(costOut, "run") {
		t.Errorf("cost output should contain action 'run':\n%s", costOut)
	}
}

// TestPSFailsWithoutDB verifies that ps fails when no state.sqlite exists.
func TestPSFailsWithoutDB(t *testing.T) {
	base := t.TempDir()
	orgDir, err := root.InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}
	projPath := filepath.Join(base, "nodb")
	if err := os.MkdirAll(projPath, 0755); err != nil {
		t.Fatal(err)
	}
	_, err = root.InitProject(projPath, orgDir, root.InitProjectOpts{Name: "nodb"})
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}

	// Remove the DB to simulate a missing database
	dbPath := filepath.Join(projPath, ".ateam", "state.sqlite")
	os.Remove(dbPath)
	// Also remove WAL/SHM files
	os.Remove(dbPath + "-wal")
	os.Remove(dbPath + "-shm")

	savedPS := savePSGlobals()
	defer savedPS.restore()
	orgFlag = base

	var psErr error
	captureStdout(t, func() {
		withChdir(t, projPath, func() {
			psErr = runRuns(nil, nil)
		})
	})
	if psErr == nil {
		t.Fatal("expected ps to fail when DB missing")
	}
	if !strings.Contains(psErr.Error(), "not found") {
		t.Errorf("error should mention 'not found': %v", psErr)
	}
}

// TestCostFailsWithoutDB verifies that cost fails when no state.sqlite exists.
func TestCostFailsWithoutDB(t *testing.T) {
	base := t.TempDir()
	orgDir, err := root.InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}
	projPath := filepath.Join(base, "nodb")
	if err := os.MkdirAll(projPath, 0755); err != nil {
		t.Fatal(err)
	}
	_, err = root.InitProject(projPath, orgDir, root.InitProjectOpts{Name: "nodb"})
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}

	// Remove the DB
	os.Remove(filepath.Join(projPath, ".ateam", "state.sqlite"))

	savedPS := savePSGlobals()
	defer savedPS.restore()
	orgFlag = base

	var costErr error
	captureStdout(t, func() {
		withChdir(t, projPath, func() {
			costErr = runCost(nil, nil)
		})
	})
	if costErr == nil {
		t.Fatal("expected cost to fail when DB missing")
	}
}

// TestEnvShowsNotFoundForMissingPaths verifies env flags missing DB and logs.
func TestEnvShowsNotFoundForMissingPaths(t *testing.T) {
	base := t.TempDir()
	orgDir, err := root.InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}
	projPath := filepath.Join(base, "testproj")
	if err := os.MkdirAll(projPath, 0755); err != nil {
		t.Fatal(err)
	}
	_, err = root.InitProject(projPath, orgDir, root.InitProjectOpts{Name: "testproj"})
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}

	// Remove the DB
	os.Remove(filepath.Join(projPath, ".ateam", "state.sqlite"))

	env, err := root.LookupFrom(projPath)
	if err != nil {
		t.Fatalf("LookupFrom: %v", err)
	}

	cwd, _ := resolvedCwd()
	out := captureStdout(t, func() {
		printProjectSection(env, cwd)
	})

	if !strings.Contains(out, "NOT FOUND") {
		t.Errorf("expected NOT FOUND for missing DB:\n%s", out)
	}
}

// --- global state save/restore helpers ---

type runGlobals struct {
	org, profile, agent, role, model, workDir, agentArgs, taskGroup, containerName string
	noStream, noSummary, quiet, verbose, dryRun, dockerAutoSetup                   bool
}

func saveRunGlobals() runGlobals {
	return runGlobals{
		org: orgFlag, profile: runProfile, agent: runAgent, role: runRole, model: runModel,
		workDir: runWorkDir, agentArgs: runAgentArgs, taskGroup: runTaskGroup,
		containerName: runContainerName, noStream: runNoStream, noSummary: runNoSummary,
		quiet: runQuiet, verbose: runVerbose, dryRun: runDryRun, dockerAutoSetup: runDockerAutoSetup,
	}
}

func (g runGlobals) restore() {
	orgFlag = g.org
	runProfile = g.profile
	runAgent = g.agent
	runRole = g.role
	runModel = g.model
	runWorkDir = g.workDir
	runAgentArgs = g.agentArgs
	runTaskGroup = g.taskGroup
	runContainerName = g.containerName
	runNoStream = g.noStream
	runNoSummary = g.noSummary
	runQuiet = g.quiet
	runVerbose = g.verbose
	runDryRun = g.dryRun
	runDockerAutoSetup = g.dockerAutoSetup
}

type psGlobals struct {
	org, role, action, taskGroup string
	limit                        int
}

func savePSGlobals() psGlobals {
	return psGlobals{
		org: orgFlag, role: recentRole, action: recentAction,
		taskGroup: recentTaskGroup, limit: recentLimit,
	}
}

func (g psGlobals) restore() {
	orgFlag = g.org
	recentRole = g.role
	recentAction = g.action
	recentTaskGroup = g.taskGroup
	recentLimit = g.limit
}
