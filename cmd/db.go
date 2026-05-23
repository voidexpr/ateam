package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/root"
)

// openProjectDB opens the per-project state.sqlite in .ateam/, creating it
// if it doesn't exist. Returns an error if the project has no ProjectDir.
//
// On first open after upgrading to the logs/<exec_id>/ layout this also runs
// MigrateLogsLayout — sentinel-guarded, so subsequent calls are no-ops.
func openProjectDB(env *root.ResolvedEnv) (*calldb.CallDB, error) {
	if env.ProjectDir == "" {
		return nil, fmt.Errorf("no project context — run 'ateam init' first")
	}
	dbPath := env.ProjectDBPath()
	db, err := calldb.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("cannot open project database %s: %w", dbPath, err)
	}
	if err := root.MigrateLogsLayout(env, db); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: log layout migration: %v\n", err)
	}
	return db, nil
}

// requireProjectDB opens an existing per-project state.sqlite.
// Returns an error if the database does not exist.
//
// Like openProjectDB, this also runs MigrateLogsLayout so read-only commands
// (ateam ps, cat, inspect, resume, tail, cost) trigger the migration when
// they touch the DB.
func requireProjectDB(env *root.ResolvedEnv) (*calldb.CallDB, error) {
	if env.ProjectDir == "" {
		return nil, fmt.Errorf("no project context — run 'ateam init' first")
	}
	dbPath := env.ProjectDBPath()
	db, err := calldb.OpenIfExists(dbPath)
	if err != nil {
		return nil, fmt.Errorf("cannot open project database %s: %w", dbPath, err)
	}
	if db == nil {
		return nil, fmt.Errorf("project database not found at %s — run a command like 'ateam exec' or 'ateam report' first", dbPath)
	}
	if err := root.MigrateLogsLayout(env, db); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: log layout migration: %v\n", err)
	}
	return db, nil
}

func checkConcurrentRunsEnv(db *calldb.CallDB, env *root.ResolvedEnv, action string, roles []string) error {
	projectID := env.ProjectID()
	if projectID == "" && env.OrgDir != "" {
		return fmt.Errorf("cannot determine project ID for concurrency guard")
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
