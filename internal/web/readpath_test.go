package web

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ateam/internal/calldb"
)

// TestGetDB_MissingDB_DoesNotCreateFile verifies that getDB does not create
// state.sqlite on disk when the project DB doesn't exist. The web server
// (ateam serve) must not mutate disk for read-only operations.
func TestGetDB_MissingDB_DoesNotCreateFile(t *testing.T) {
	dir := t.TempDir()
	projectDir := filepath.Join(dir, ".ateam")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}

	s := &Server{}
	pe := &ProjectEntry{ProjectDir: projectDir}

	db := s.getDB(pe)

	dbPath := filepath.Join(projectDir, "state.sqlite")
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		if db != nil {
			db.Close()
		}
		t.Fatalf("getDB must not create state.sqlite for read-only access; stat err = %v", err)
	}

	if db != nil {
		db.Close()
		t.Fatal("getDB should return nil when project DB doesn't exist")
	}
}

// TestGetDB_ExistingDB_ReturnsIt verifies that getDB opens and caches an
// existing project DB.
func TestGetDB_ExistingDB_ReturnsIt(t *testing.T) {
	dir := t.TempDir()
	projectDir := filepath.Join(dir, ".ateam")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create the DB with data.
	dbPath := filepath.Join(projectDir, "state.sqlite")
	db, err := calldb.Open(dbPath)
	if err != nil {
		t.Fatalf("creating DB: %v", err)
	}
	_, err = db.InsertCall(&calldb.Call{
		ProjectID: "proj",
		Agent:     "claude",
		Container: "none",
		Action:    "report",
		Role:      "security",
		StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("seeding DB: %v", err)
	}
	db.Close()

	s := &Server{}
	pe := &ProjectEntry{ProjectDir: projectDir}

	got := s.getDB(pe)
	if got == nil {
		t.Fatal("expected non-nil DB for existing state.sqlite")
	}
	defer got.Close()

	var count int
	got.RawDB().QueryRow("SELECT COUNT(*) FROM agent_execs").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 row, got %d", count)
	}

	// Verify caching: second call returns the same instance.
	got2 := s.getDB(pe)
	if got2 != got {
		t.Error("getDB should cache and return the same instance")
	}
}
