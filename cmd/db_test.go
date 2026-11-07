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
	env := &root.ResolvedEnv{ProjectDir: "", HomeDir: ""}

	_, err := openStateDB(env)
	if err == nil {
		t.Fatal("expected error when neither a project nor a home dir is resolved")
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
	// Scratch mode (no project, hence no name) skips the guard.
	t.Run("ScratchMode", func(t *testing.T) {
		env := &root.ResolvedEnv{HomeDir: "/some/home/.ateam"}
		if err := checkConcurrentRunsEnv(nil, env, "code", nil); err != nil {
			t.Fatalf("expected no error in scratch mode, got: %v", err)
		}
	})

	// A named project delegates to checkConcurrentRuns (nil db returns nil).
	t.Run("ProjectWithName", func(t *testing.T) {
		env := &root.ResolvedEnv{HomeDir: "/some/home/.ateam", ProjectDir: "/some/myproject/.ateam", ProjectName: "myproject"}
		if err := checkConcurrentRunsEnv(nil, env, "code", nil); err != nil {
			t.Fatalf("expected no error with a named project, got: %v", err)
		}
	})
}
