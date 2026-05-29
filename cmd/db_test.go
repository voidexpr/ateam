package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/root"
)

func TestOpenProjectDBCreatesProjectDB(t *testing.T) {
	dir := t.TempDir()
	projectDir := filepath.Join(dir, "project", ".ateam")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}

	env := &root.ResolvedEnv{
		ProjectDir: projectDir,
	}

	projDBPath := filepath.Join(projectDir, "state.sqlite")
	if _, err := os.Stat(projDBPath); !os.IsNotExist(err) {
		t.Fatalf("project DB should not exist yet, got err=%v", err)
	}

	db, err := openStateDB(env)
	if err != nil {
		t.Fatalf("openStateDB: %v", err)
	}
	db.Close()

	if _, err := os.Stat(projDBPath); err != nil {
		t.Fatalf("project DB should have been created: %v", err)
	}
}

func TestOpenProjectDBErrorsWithoutProjectDir(t *testing.T) {
	env := &root.ResolvedEnv{
		ProjectDir: "",
		OrgDir:     "/tmp/some-org",
	}

	_, err := openStateDB(env)
	if err == nil {
		t.Fatal("expected error when ProjectDir is empty")
	}
}

func TestOpenProjectDBOpensExistingDB(t *testing.T) {
	dir := t.TempDir()
	projectDir := filepath.Join(dir, "project", ".ateam")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}

	projDBPath := filepath.Join(projectDir, "state.sqlite")
	projDB, err := calldb.Open(projDBPath)
	if err != nil {
		t.Fatalf("Open project DB: %v", err)
	}
	projDB.Close()

	env := &root.ResolvedEnv{
		ProjectDir: projectDir,
	}

	db, err := openStateDB(env)
	if err != nil {
		t.Fatalf("openStateDB: %v", err)
	}
	db.Close()
}

func TestRequireProjectDBFailsWhenMissing(t *testing.T) {
	dir := t.TempDir()
	projectDir := filepath.Join(dir, "project", ".ateam")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}

	env := &root.ResolvedEnv{
		ProjectDir: projectDir,
	}

	_, err := requireStateDB(env)
	if err == nil {
		t.Fatal("expected error when DB does not exist")
	}
}

func TestRequireProjectDBSucceedsWhenExists(t *testing.T) {
	dir := t.TempDir()
	projectDir := filepath.Join(dir, "project", ".ateam")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}

	projDBPath := filepath.Join(projectDir, "state.sqlite")
	projDB, err := calldb.Open(projDBPath)
	if err != nil {
		t.Fatalf("Open project DB: %v", err)
	}
	projDB.Close()

	env := &root.ResolvedEnv{
		ProjectDir: projectDir,
	}

	db, err := requireStateDB(env)
	if err != nil {
		t.Fatalf("requireStateDB: %v", err)
	}
	db.Close()
}

func TestCheckConcurrentRunsEnv(t *testing.T) {
	// (a) scratch mode: org resolved, no project dir → no error (guard skipped).
	// This is the `ateam exec` from arbitrary cwd case: there's no per-project
	// namespace to enforce against.
	t.Run("ScratchModeOrgOnly", func(t *testing.T) {
		env := &root.ResolvedEnv{
			OrgDir:    "/some/org/.ateamorg",
			SourceDir: "", // causes ProjectID() == ""
		}
		err := checkConcurrentRunsEnv(nil, env, "code", nil)
		if err != nil {
			t.Fatalf("expected no error in scratch mode (no ProjectDir), got: %v", err)
		}
	})

	// (b) project resolved but ProjectID() returns "" → real error (path mapping broken).
	t.Run("ProjectModeEmptyProjectID", func(t *testing.T) {
		env := &root.ResolvedEnv{
			OrgDir:     "/some/org/.ateamorg",
			ProjectDir: "/some/org/myproject/.ateam",
			SourceDir:  "", // ProjectID() returns "" because SourceDir is empty
		}
		err := checkConcurrentRunsEnv(nil, env, "code", nil)
		if err == nil {
			t.Fatal("expected error when ProjectDir is set but ProjectID is empty")
		}
	})

	// (c) org-less mode with empty ProjectID → no error
	t.Run("NoOrgEmptyProjectID", func(t *testing.T) {
		env := &root.ResolvedEnv{
			OrgDir:    "",
			SourceDir: "",
		}
		err := checkConcurrentRunsEnv(nil, env, "code", nil)
		if err != nil {
			t.Fatalf("expected no error when OrgDir is empty, got: %v", err)
		}
	})

	// (d) org mode with valid ProjectID → delegates to checkConcurrentRuns (nil db returns nil)
	t.Run("OrgModeValidProjectID", func(t *testing.T) {
		orgDir := "/some/org/.ateamorg"
		env := &root.ResolvedEnv{
			OrgDir:    orgDir,
			SourceDir: "/some/org/myproject",
		}
		err := checkConcurrentRunsEnv(nil, env, "code", nil)
		if err != nil {
			t.Fatalf("expected no error when ProjectID is valid, got: %v", err)
		}
	})
}
