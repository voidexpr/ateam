package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/root"
)

func TestOpenProjectDBFallsBackToOrgDB(t *testing.T) {
	dir := t.TempDir()
	projectDir := filepath.Join(dir, "project", ".ateam")
	orgDir := filepath.Join(dir, ".ateamorg")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(orgDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create the org-level DB with a known row so we can verify fallback.
	orgDBPath := filepath.Join(orgDir, "state.sqlite")
	orgDB, err := calldb.Open(orgDBPath)
	if err != nil {
		t.Fatalf("Open org DB: %v", err)
	}
	orgDB.Close()

	env := &root.ResolvedEnv{
		ProjectDir: projectDir,
		OrgDir:     orgDir,
	}

	// Project DB does not exist — should fall back to org DB.
	projDBPath := filepath.Join(projectDir, "state.sqlite")
	if _, err := os.Stat(projDBPath); !os.IsNotExist(err) {
		t.Fatalf("project DB should not exist yet, got err=%v", err)
	}

	db := openProjectDB(env)
	if db == nil {
		t.Fatal("expected non-nil db from org fallback")
	}
	db.Close()

	// Verify project DB was NOT created on disk.
	if _, err := os.Stat(projDBPath); !os.IsNotExist(err) {
		t.Fatalf("project DB should not have been created, got err=%v", err)
	}
}

func TestOpenProjectDBUsesProjectDBWhenExists(t *testing.T) {
	dir := t.TempDir()
	projectDir := filepath.Join(dir, "project", ".ateam")
	orgDir := filepath.Join(dir, ".ateamorg")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(orgDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create the project-level DB.
	projDBPath := filepath.Join(projectDir, "state.sqlite")
	projDB, err := calldb.Open(projDBPath)
	if err != nil {
		t.Fatalf("Open project DB: %v", err)
	}
	projDB.Close()

	env := &root.ResolvedEnv{
		ProjectDir: projectDir,
		OrgDir:     orgDir,
	}

	db := openProjectDB(env)
	if db == nil {
		t.Fatal("expected non-nil db from project DB")
	}
	db.Close()
}
