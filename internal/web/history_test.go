package web

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ateam/internal/calldb"
)

func TestParseExecHistoryFilename(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"exec-42.md", 42},
		{"exec-1.md", 1},
		{"exec-9999.md", 9999},
		{"2026-03-14_00-20-28.report.md", 0},
		{"exec.md", 0},
		{"exec-.md", 0},
		{"exec-abc.md", 0},
		{"exec-42", 0},
		{"", 0},
	}
	for _, tc := range cases {
		if got := parseExecHistoryFilename(tc.in); got != tc.want {
			t.Errorf("parseExecHistoryFilename(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestHistoryFromDB_PopulatesExecFilenameAndRuntimePath(t *testing.T) {
	projDir := t.TempDir()
	db, err := calldb.Open(filepath.Join(projDir, "state.sqlite"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	id, err := db.InsertCall(&calldb.Call{
		Action:    "report",
		Role:      "security",
		StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("InsertCall: %v", err)
	}
	// Simulate runner finalizeCall storing the runtime path.
	runtimeRel := filepath.Join("runtime", itoa(id), "report.md")
	if err := db.UpdateOutputFile(id, runtimeRel); err != nil {
		t.Fatalf("UpdateOutputFile: %v", err)
	}

	// A second row with no output_file must NOT appear in history (would 404).
	if _, err := db.InsertCall(&calldb.Call{
		Action:    "report",
		Role:      "security",
		StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	entries := historyFromDB(db, projDir, "report", "security", "report")
	if len(entries) != 1 {
		t.Fatalf("want 1 entry (the one with output_file), got %d", len(entries))
	}
	got := entries[0]
	if got.ExecID != id {
		t.Errorf("ExecID = %d, want %d", got.ExecID, id)
	}
	wantFilename := execHistoryFilename(id)
	if got.Filename != wantFilename {
		t.Errorf("Filename = %q, want %q", got.Filename, wantFilename)
	}
	wantPath := filepath.Join(projDir, runtimeRel)
	if got.Path != wantPath {
		t.Errorf("Path = %q, want %q", got.Path, wantPath)
	}
}

// itoa converts a small positive int64 to its decimal string. Avoids pulling
// strconv into the test file just for one call.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
