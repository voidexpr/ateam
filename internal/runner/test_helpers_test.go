package runner

import (
	"path/filepath"
	"testing"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/calldb"
)

// newTestRunner returns a AgentExecutor backed by a temp project DB so Run() can
// satisfy its "CallDB required" precondition. baseDir is the test's TempDir.
func newTestRunner(t *testing.T, baseDir string, ag agent.Agent) *AgentExecutor {
	t.Helper()
	dbPath := filepath.Join(baseDir, "state.sqlite")
	db, err := calldb.Open(dbPath)
	if err != nil {
		t.Fatalf("open test calldb: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &AgentExecutor{
		Agent:      ag,
		ProjectDir: baseDir,
		CallDB:     db,
	}
}
