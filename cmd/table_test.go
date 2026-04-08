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

	db, err := openProjectDB(env)
	if err != nil {
		t.Fatalf("openProjectDB: %v", err)
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

	_, err := openProjectDB(env)
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

	db, err := openProjectDB(env)
	if err != nil {
		t.Fatalf("openProjectDB: %v", err)
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

	_, err := requireProjectDB(env)
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

	db, err := requireProjectDB(env)
	if err != nil {
		t.Fatalf("requireProjectDB: %v", err)
	}
	db.Close()
}
