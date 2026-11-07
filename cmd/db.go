package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/root"
)

// openStateDB opens state.sqlite under env.StateDir() (the project's .ateam/
// when inside one, else ~/.ateam/state/ for scratch-mode exec/parallel).
// Creates the file and its directory if missing.
func openStateDB(env *root.ResolvedEnv) (*calldb.CallDB, error) {
	if env.StateDir() == "" {
		return nil, fmt.Errorf("no project .ateam/ and no user ateam directory found — run 'ateam init' first")
	}
	dbPath := env.ProjectDBPath()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("cannot create state directory %s: %w", filepath.Dir(dbPath), err)
	}
	db, err := calldb.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("cannot open state database %s: %w", dbPath, err)
	}
	return db, nil
}

// requireStateDB opens an existing state.sqlite from env.StateDir().
// Returns an error if the database does not exist.
func requireStateDB(env *root.ResolvedEnv) (*calldb.CallDB, error) {
	if env.StateDir() == "" {
		return nil, fmt.Errorf("no project .ateam/ and no user ateam directory found")
	}
	dbPath := env.ProjectDBPath()
	db, err := calldb.OpenIfExists(dbPath)
	if err != nil {
		return nil, fmt.Errorf("cannot open state database %s: %w", dbPath, err)
	}
	if db == nil {
		return nil, fmt.Errorf("state database not found at %s — run a command like 'ateam exec' or 'ateam report' first", dbPath)
	}
	return db, nil
}

func checkConcurrentRunsEnv(db *calldb.CallDB, env *root.ResolvedEnv, action string, roles []string) error {
	projectID := env.ProjectID()
	// Scratch mode (no project) has no per-project namespace; skip the guard.
	if projectID == "" {
		return nil
	}
	return checkConcurrentRuns(db, projectID, action, roles)
}

// checkConcurrentRuns returns an error if any of the given roles already have a
// live process for the same project+action. Pass roles=nil to check all roles.
func checkConcurrentRuns(db *calldb.CallDB, projectID, action string, roles []string) error {
	if db == nil {
		return nil
	}
	running, err := db.FindRunning(projectID, action)
	if err != nil || len(running) == 0 {
		return nil
	}

	roleSet := make(map[string]bool, len(roles))
	for _, r := range roles {
		roleSet[r] = true
	}

	var alive []string
	for _, r := range running {
		if len(roles) > 0 && !roleSet[r.Role] {
			continue
		}
		if r.PID > 0 && isProcessAlive(r.PID) {
			alive = append(alive, fmt.Sprintf("  %s (PID %d, started %s)", r.Role, r.PID, r.StartedAt))
		}
	}
	if len(alive) == 0 {
		return nil
	}
	return fmt.Errorf("concurrent %s already running:\n%s\nuse --force to run anyway", action, strings.Join(alive, "\n"))
}

func parseIDArgs(args []string) ([]int64, error) {
	ids := make([]int64, len(args))
	for i, arg := range args {
		id, err := strconv.ParseInt(arg, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid ID %q: %w", arg, err)
		}
		ids[i] = id
	}
	return ids, nil
}

// resolveExecIDs returns the explicit IDs from args, or the most recent run's
// ID when useLast is set and no args were given. Errors when both are
// provided so a stray --last on a typed-ID command line surfaces instead of
// being silently ignored.
func resolveExecIDs(db *calldb.CallDB, args []string, useLast bool) ([]int64, error) {
	if useLast && len(args) > 0 {
		return nil, fmt.Errorf("--last cannot be combined with explicit IDs")
	}
	if useLast {
		id, err := lastRunID(db)
		if err != nil {
			return nil, err
		}
		return []int64{id}, nil
	}
	return parseIDArgs(args)
}

// lastRunID returns the ID of the most recent agent_execs row.
func lastRunID(db *calldb.CallDB) (int64, error) {
	rows, err := db.RecentRuns(calldb.RecentFilter{Limit: 1})
	if err != nil {
		return 0, fmt.Errorf("query failed: %w", err)
	}
	if len(rows) == 0 {
		return 0, fmt.Errorf("no runs found")
	}
	return rows[0].ID, nil
}
