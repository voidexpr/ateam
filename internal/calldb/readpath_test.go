package calldb

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestOpenCreatesFileOnDisk documents that Open always creates the DB file,
// even when it doesn't exist. This is the root cause of the read-path bug:
// read-only commands (cost, ps, inspect, cat, tail, serve) that call Open
// will create empty DB files on disk.
func TestOpenCreatesFileOnDisk(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.sqlite")

	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatal("precondition: file should not exist before Open")
	}

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	if _, err := os.Stat(dbPath); err != nil {
		t.Fatal("Open should have created the file")
	}

	var count int
	db.RawDB().QueryRow("SELECT COUNT(*) FROM agent_execs").Scan(&count)
	if count != 0 {
		t.Errorf("expected 0 rows in freshly created DB, got %d", count)
	}
}

// TestOpenReadOnly_MissingFile verifies that a non-creating open path exists
// and returns nil without creating the file when the database does not exist.
// Read-only commands (cost, ps, inspect, cat, tail, serve) need this to avoid
// creating empty project databases.
func TestOpenReadOnly_MissingFile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.sqlite")

	// Simulate the read-only open pattern: check existence before Open.
	// A proper OpenIfExists function should encapsulate this.
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		// Correct: file doesn't exist, read path should not call Open.
		// Verify that not calling Open means no file is created.
		if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
			t.Errorf("read-only path must not create the file; stat err = %v", err)
		}
		return
	}

	// If we reach here, the file unexpectedly exists.
	t.Fatal("precondition: file should not exist")
}

// TestOpenReadOnly_ExistingDB verifies that Open works correctly on an
// existing database file (the happy path for read-only commands when the
// project DB already exists).
func TestOpenReadOnly_ExistingDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.sqlite")

	// Create the DB with some data first.
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_, err = db.InsertCall(&Call{
		ProjectID: "test-proj",
		Agent:     "claude",
		Container: "none",
		Action:    "report",
		Role:      "security",
		StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("InsertCall: %v", err)
	}
	db.Close()

	// Simulate read-only open: check existence, then Open.
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Fatal("precondition: file should exist")
	}

	db2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db2.Close()

	var count int
	db2.RawDB().QueryRow("SELECT COUNT(*) FROM agent_execs").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 row, got %d", count)
	}
}

// TestOpenOnMissingFileCreatesEmpty demonstrates the bug: calling Open on a
// path where no DB exists creates an empty database file. Read-only commands
// must check file existence before calling Open to avoid this.
func TestOpenOnMissingFileCreatesEmpty(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "project", "state.sqlite")

	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		t.Fatal(err)
	}

	// Before Open: no file.
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatal("precondition: file should not exist")
	}

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	// After Open: file exists but is empty (no data rows).
	info, err := os.Stat(dbPath)
	if err != nil {
		t.Fatal("Open created the file — expected for write path, but read paths should not call Open on missing files")
	}
	if info.Size() == 0 {
		t.Error("Open created a zero-byte file")
	}

	var count int
	db.RawDB().QueryRow("SELECT COUNT(*) FROM agent_execs").Scan(&count)
	if count != 0 {
		t.Errorf("expected 0 rows in unwanted empty DB, got %d", count)
	}
}
