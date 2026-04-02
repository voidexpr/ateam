package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/root"
)

// TestOpenProjectDB_NoProjectDB_FallsBackToOrg verifies that openProjectDB
// falls back to the org-level DB when the project DB file doesn't exist,
// without creating an empty project state.sqlite.
func TestOpenProjectDB_NoProjectDB_FallsBackToOrg(t *testing.T) {
	dir := t.TempDir()
	projectDir := filepath.Join(dir, ".ateam")
	orgDir := filepath.Join(dir, ".ateamorg")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(orgDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Seed the org-level DB with data.
	orgDBPath := filepath.Join(orgDir, "state.sqlite")
	orgDB, err := calldb.Open(orgDBPath)
	if err != nil {
		t.Fatalf("creating org DB: %v", err)
	}
	_, err = orgDB.InsertCall(&calldb.Call{
		ProjectID: "test-proj",
		Agent:     "claude",
		Container: "none",
		Action:    "report",
		Role:      "security",
		StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("seeding org DB: %v", err)
	}
	orgDB.Close()

	env := &root.ResolvedEnv{
		ProjectDir: projectDir,
		OrgDir:     orgDir,
	}

	db := openProjectDB(env)

	// The project state.sqlite must NOT have been created.
	projDBPath := filepath.Join(projectDir, "state.sqlite")
	if _, err := os.Stat(projDBPath); !os.IsNotExist(err) {
		if db != nil {
			db.Close()
		}
		t.Fatalf("openProjectDB must not create project DB file; stat err = %v", err)
	}

	// Should have fallen back to the org DB (non-nil, with data).
	if db == nil {
		t.Fatal("expected fallback to org DB, got nil")
	}
	defer db.Close()

	var count int
	db.RawDB().QueryRow("SELECT COUNT(*) FROM agent_execs").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 row from org DB fallback, got %d", count)
	}
}

// TestOpenProjectDB_ExistingProjectDB_UsesIt verifies that when the project
// DB exists, openProjectDB opens it (not the org DB).
func TestOpenProjectDB_ExistingProjectDB_UsesIt(t *testing.T) {
	dir := t.TempDir()
	projectDir := filepath.Join(dir, ".ateam")
	orgDir := filepath.Join(dir, ".ateamorg")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(orgDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create project DB with 2 rows.
	projDB, err := calldb.Open(filepath.Join(projectDir, "state.sqlite"))
	if err != nil {
		t.Fatalf("creating project DB: %v", err)
	}
	for i := range 2 {
		_, err := projDB.InsertCall(&calldb.Call{
			ProjectID: "test-proj",
			Agent:     "claude",
			Container: "none",
			Action:    "report",
			Role:      "security",
			StartedAt: time.Now().Add(time.Duration(i) * time.Second),
		})
		if err != nil {
			t.Fatalf("seeding project DB: %v", err)
		}
	}
	projDB.Close()

	// Create org DB with 1 row (different data).
	orgDB, err := calldb.Open(filepath.Join(orgDir, "state.sqlite"))
	if err != nil {
		t.Fatalf("creating org DB: %v", err)
	}
	_, err = orgDB.InsertCall(&calldb.Call{
		ProjectID: "other",
		Agent:     "codex",
		Container: "none",
		Action:    "run",
		Role:      "testing",
		StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("seeding org DB: %v", err)
	}
	orgDB.Close()

	env := &root.ResolvedEnv{
		ProjectDir: projectDir,
		OrgDir:     orgDir,
	}

	db := openProjectDB(env)
	if db == nil {
		t.Fatal("expected project DB, got nil")
	}
	defer db.Close()

	var count int
	db.RawDB().QueryRow("SELECT COUNT(*) FROM agent_execs").Scan(&count)
	if count != 2 {
		t.Errorf("expected 2 rows from project DB, got %d", count)
	}
}

// TestOpenProjectDB_NoProjectDir_UsesOrgDB verifies that when ProjectDir is
// empty, openProjectDB returns the org DB.
func TestOpenProjectDB_NoProjectDir_UsesOrgDB(t *testing.T) {
	dir := t.TempDir()
	orgDir := filepath.Join(dir, ".ateamorg")
	if err := os.MkdirAll(orgDir, 0755); err != nil {
		t.Fatal(err)
	}

	orgDB, err := calldb.Open(filepath.Join(orgDir, "state.sqlite"))
	if err != nil {
		t.Fatalf("creating org DB: %v", err)
	}
	_, err = orgDB.InsertCall(&calldb.Call{
		ProjectID: "proj",
		Agent:     "claude",
		Container: "none",
		Action:    "report",
		Role:      "security",
		StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("seeding org DB: %v", err)
	}
	orgDB.Close()

	env := &root.ResolvedEnv{
		ProjectDir: "",
		OrgDir:     orgDir,
	}

	db := openProjectDB(env)
	if db == nil {
		t.Fatal("expected org DB, got nil")
	}
	defer db.Close()

	var count int
	db.RawDB().QueryRow("SELECT COUNT(*) FROM agent_execs").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 row from org DB, got %d", count)
	}
}
