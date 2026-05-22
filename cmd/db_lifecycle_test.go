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
// and the per-role logs directories.
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

	// Per-role logs dir must exist (active streams write under logs/<exec_id>/
	// at runtime, so no need for any other init-time subdir).
	roleDir := filepath.Join(env.ProjectDir, "logs", "roles", "testing_basic")
	if _, err := os.Stat(roleDir); err != nil {
		t.Errorf("logs/roles/testing_basic not created: %v", err)
	}
}

// TestRunWithMockAgent verifies init → run → ps flow using the mock agent.
func TestRunWithMockAgent(t *testing.T) {
	orgParent, projPath, _ := setupTestProject(t)

	saved := saveExecGlobals()
	defer saved.restore()
	orgFlag = orgParent
	execProfile = "test" // uses mock agent
	execQuiet = true
	execNoStream = true

	var runErr error
	captureStdout(t, func() {
		withChdir(t, projPath, func() {
			runErr = runExec(nil, []string{"hello mock"})
		})
	})
	if runErr != nil {
		t.Fatalf("runExec: %v", runErr)
	}

	// ps should show the run
	savedPS := savePSGlobals()
	defer savedPS.restore()
	orgFlag = orgParent

	var psErr error
	psOut := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			psErr = runPs(nil, nil)
		})
	})
	if psErr != nil {
		t.Fatalf("ps: %v", psErr)
	}
	if !strings.Contains(psOut, "exec") {
		t.Errorf("ps output should contain action 'exec':\n%s", psOut)
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
	if !strings.Contains(costOut, "exec") {
		t.Errorf("cost output should contain action 'exec':\n%s", costOut)
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
			psErr = runPs(nil, nil)
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

type execGlobals struct {
	org, profile, agent, role, action, model, effort, workDir, agentArgs, batch, containerName string
	maxBudgetUSD, maxBudgetBatch, extraPrompt                                                  string
	noStream, noSummary, quiet, verbose, dryRun, dockerAutoSetup                               bool
}

func saveExecGlobals() execGlobals {
	return execGlobals{
		org: orgFlag, profile: execProfile, agent: execAgent, role: execRole, action: execAction, model: execModel, effort: execEffort,
		workDir: workDirFlag, agentArgs: execAgentArgs, batch: execBatch, containerName: execContainerName,
		maxBudgetUSD: execMaxBudgetUSD, maxBudgetBatch: execMaxBudgetBatch, extraPrompt: execExtraPrompt,
		noStream: execNoStream, noSummary: execNoSummary, quiet: execQuiet, verbose: execVerbose, dryRun: execDryRun, dockerAutoSetup: execDockerAutoSetup,
	}
}

func (g execGlobals) restore() {
	orgFlag = g.org
	execProfile = g.profile
	execAgent = g.agent
	execRole = g.role
	execAction = g.action
	execModel = g.model
	execEffort = g.effort
	workDirFlag = g.workDir
	execAgentArgs = g.agentArgs
	execBatch = g.batch
	execContainerName = g.containerName
	execMaxBudgetUSD = g.maxBudgetUSD
	execMaxBudgetBatch = g.maxBudgetBatch
	execExtraPrompt = g.extraPrompt
	execNoStream = g.noStream
	execNoSummary = g.noSummary
	execQuiet = g.quiet
	execVerbose = g.verbose
	execDryRun = g.dryRun
	execDockerAutoSetup = g.dockerAutoSetup
}

type psGlobals struct {
	org, role, action, batch string
	limit                    int
}

func savePSGlobals() psGlobals {
	return psGlobals{
		org: orgFlag, role: psRole, action: psAction,
		batch: psBatch, limit: psLimit,
	}
}

func (g psGlobals) restore() {
	orgFlag = g.org
	psRole = g.role
	psAction = g.action
	psBatch = g.batch
	psLimit = g.limit
}
