package cmd

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/config"
	"github.com/ateam/internal/root"
	"github.com/spf13/cobra"
)

var migrateLogsDryRun bool

var migrateLogsCmd = &cobra.Command{
	Use:   "migrate-logs",
	Short: "Migrate logs and exec history from .ateamorg/ to per-project .ateam/",
	Long: `Migrate all registered projects from the legacy org-level layout to
the new per-project layout.

For each project:
  1. Copies log files from .ateamorg/projects/<id>/ to .ateam/logs/
  2. Copies agent_execs rows from .ateamorg/state.sqlite to .ateam/state.sqlite
  3. Rewrites stream_file paths from org-relative to project-relative
  4. Creates .ateam/.gitignore if missing
  5. Creates .ateam/logs/ directory structure if missing

Run from anywhere under the org root. Use --dry-run to preview changes.

Example:
  ateam migrate-logs
  ateam migrate-logs --dry-run`,
	Args: cobra.NoArgs,
	RunE: runMigrateLogs,
}

func init() {
	migrateLogsCmd.Flags().BoolVar(&migrateLogsDryRun, "dry-run", false, "preview changes without applying them")
}

type migrationProject struct {
	projectID string
	relPath   string
	ateamDir  string // absolute path to .ateam/
	stateDir  string // absolute path to .ateamorg/projects/<id>/
}

func runMigrateLogs(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("cannot get working directory: %w", err)
	}

	var orgDir string
	if orgFlag != "" {
		orgDir, err = root.FindOrg(orgFlag)
	} else {
		orgDir, err = root.FindOrg(cwd)
	}
	if err != nil {
		return err
	}

	orgRoot := filepath.Dir(orgDir)
	orgDBPath := filepath.Join(orgDir, "state.sqlite")

	// Check if org DB exists
	if _, err := os.Stat(orgDBPath); os.IsNotExist(err) {
		fmt.Println("No .ateamorg/state.sqlite found, nothing to migrate.")
		return nil
	}

	// Discover all registered projects
	projects, err := discoverMigrationProjects(orgDir, orgRoot)
	if err != nil {
		return err
	}
	if len(projects) == 0 {
		fmt.Println("No registered projects found.")
		return nil
	}

	fmt.Printf("Found %d project(s) to migrate:\n", len(projects))
	for _, p := range projects {
		fmt.Printf("  %s -> %s\n", p.projectID, p.relPath)
	}
	fmt.Println()

	var totalFiles, totalRows int
	for _, p := range projects {
		nf, nr, err := migrateProject(orgDir, orgRoot, orgDBPath, p, migrateLogsDryRun)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error migrating %s: %v\n", p.relPath, err)
			continue
		}
		totalFiles += nf
		totalRows += nr
	}

	verb := "Migrated"
	if migrateLogsDryRun {
		verb = "Would migrate"
	}
	fmt.Printf("\n%s %d file(s), %d DB row(s) across %d project(s).\n", verb, totalFiles, totalRows, len(projects))
	return nil
}

func discoverMigrationProjects(orgDir, orgRoot string) ([]migrationProject, error) {
	projectsDir := filepath.Join(orgDir, "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot read projects directory: %w", err)
	}

	var projects []migrationProject
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		projectID := e.Name()
		relPath := config.ProjectIDToPath(projectID)
		ateamDir := filepath.Join(orgRoot, relPath, root.ProjectDirName)

		if _, err := os.Stat(ateamDir); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: skipping %s — .ateam/ not found at %s\n", projectID, ateamDir)
			continue
		}

		projects = append(projects, migrationProject{
			projectID: projectID,
			relPath:   relPath,
			ateamDir:  ateamDir,
			stateDir:  filepath.Join(projectsDir, projectID),
		})
	}
	return projects, nil
}

func migrateProject(orgDir, orgRoot, orgDBPath string, p migrationProject, dryRun bool) (filesCopied, rowsCopied int, err error) {
	prefix := ""
	if dryRun {
		prefix = "[dry-run] "
	}

	fmt.Printf("%sMigrating %s...\n", prefix, p.relPath)

	// 1. Copy log files
	filesCopied, err = migrateLogFiles(p, dryRun, prefix)
	if err != nil {
		return filesCopied, 0, fmt.Errorf("copying log files: %w", err)
	}

	// 2. Copy runner.log
	runnerLog := filepath.Join(p.stateDir, "runner.log")
	if _, err := os.Stat(runnerLog); err == nil {
		dst := filepath.Join(p.ateamDir, "logs", "runner.log")
		if dryRun {
			fmt.Printf("  %scopy %s -> %s\n", prefix, relPath(orgRoot, runnerLog), relPath(orgRoot, dst))
		} else {
			if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
				return filesCopied, 0, err
			}
			if err := appendFile(dst, runnerLog); err != nil {
				return filesCopied, 0, fmt.Errorf("copying runner.log: %w", err)
			}
		}
		filesCopied++
	}

	// 3. Copy DB rows
	rowsCopied, err = migrateDBRows(orgDBPath, p, dryRun, prefix)
	if err != nil {
		return filesCopied, rowsCopied, fmt.Errorf("copying DB rows: %w", err)
	}

	// 4. Ensure .gitignore
	gitignorePath := filepath.Join(p.ateamDir, ".gitignore")
	if _, err := os.Stat(gitignorePath); os.IsNotExist(err) {
		if dryRun {
			fmt.Printf("  %screate .gitignore\n", prefix)
		} else {
			if err := root.WriteProjectGitignore(p.ateamDir); err != nil {
				return filesCopied, rowsCopied, fmt.Errorf("writing .gitignore: %w", err)
			}
		}
	}

	return filesCopied, rowsCopied, nil
}

// migrateLogFiles copies log files from the old state dir layout to the new .ateam/logs/ layout.
// Old: .ateamorg/projects/<id>/roles/<role>/logs/<files>
// New: .ateam/logs/roles/<role>/<files>
// Old: .ateamorg/projects/<id>/supervisor/logs/<files>
// New: .ateam/logs/supervisor/<files>
func migrateLogFiles(p migrationProject, dryRun bool, prefix string) (int, error) {
	var count int

	// Migrate role logs
	rolesDir := filepath.Join(p.stateDir, "roles")
	roleEntries, err := os.ReadDir(rolesDir)
	if err != nil && !os.IsNotExist(err) {
		return 0, err
	}
	for _, roleEntry := range roleEntries {
		if !roleEntry.IsDir() {
			continue
		}
		roleID := roleEntry.Name()
		oldLogsDir := filepath.Join(rolesDir, roleID, "logs")
		newLogsDir := filepath.Join(p.ateamDir, "logs", "roles", roleID)

		n, err := copyDirFiles(oldLogsDir, newLogsDir, dryRun, prefix)
		if err != nil {
			return count, fmt.Errorf("role %s: %w", roleID, err)
		}
		count += n
	}

	// Migrate supervisor logs
	oldSupLogs := filepath.Join(p.stateDir, "supervisor", "logs")
	newSupLogs := filepath.Join(p.ateamDir, "logs", "supervisor")
	n, err := copyDirFiles(oldSupLogs, newSupLogs, dryRun, prefix)
	if err != nil {
		return count, fmt.Errorf("supervisor: %w", err)
	}
	count += n

	return count, nil
}

// copyDirFiles copies all files (non-recursive) from src to dst.
// Skips files that already exist in dst.
func copyDirFiles(src, dst string, dryRun bool, prefix string) (int, error) {
	entries, err := os.ReadDir(src)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	var count int
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		srcFile := filepath.Join(src, e.Name())
		dstFile := filepath.Join(dst, e.Name())

		if dryRun {
			fmt.Printf("  %scopy %s\n", prefix, e.Name())
		} else {
			if err := os.MkdirAll(dst, 0755); err != nil {
				return count, err
			}
			if err := copyFile(srcFile, dstFile); err != nil {
				if os.IsExist(err) {
					continue // already copied
				}
				return count, fmt.Errorf("copy %s: %w", e.Name(), err)
			}
		}
		count++
	}
	return count, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

// appendFile appends the contents of src to dst, creating dst if needed.
func appendFile(dst, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

// migrateDBRows copies agent_execs rows for a project from the org DB to the project DB.
// It rewrites project_id to "" and stream_file from org-relative to project-relative.
func migrateDBRows(orgDBPath string, p migrationProject, dryRun bool, prefix string) (int, error) {
	// Check if already migrated before reading from the org DB.
	if !dryRun {
		projDBPath := filepath.Join(p.ateamDir, "state.sqlite")
		if _, err := os.Stat(projDBPath); err == nil {
			cdb, err := calldb.Open(projDBPath)
			if err != nil {
				return 0, fmt.Errorf("open project DB: %w", err)
			}
			var existingCount int
			_ = cdb.RawDB().QueryRow("SELECT COUNT(*) FROM agent_execs").Scan(&existingCount)
			_ = cdb.Close()
			if existingCount > 0 {
				fmt.Printf("  project DB already has %d row(s), skipping DB migration\n", existingCount)
				return 0, nil
			}
		}
	}

	orgDB, err := sql.Open("sqlite", orgDBPath+"?_pragma=busy_timeout(5000)&mode=ro")
	if err != nil {
		return 0, fmt.Errorf("open org DB: %w", err)
	}
	defer orgDB.Close()

	// Check if agent_execs table exists in org DB
	var tableExists bool
	_ = orgDB.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='agent_execs'").Scan(&tableExists)
	if !tableExists {
		return 0, nil
	}

	// Read all rows for this project
	rows, err := orgDB.Query(`
		SELECT project_id, profile, agent, container, action, role,
			task_group, model, prompt_hash, started_at, stream_file,
			ended_at, duration_ms, exit_code, is_error, error_message,
			cost_usd, input_tokens, output_tokens, cache_read_tokens, turns,
			COALESCE(pid, 0), COALESCE(container_id, '')
		FROM agent_execs
		WHERE project_id = ?
		ORDER BY started_at`, p.projectID)
	if err != nil {
		return 0, fmt.Errorf("query org DB: %w", err)
	}
	defer rows.Close()

	type execRow struct {
		projectID, profile, agent, container, action, role  string
		taskGroup, model, promptHash, startedAt, streamFile string
		endedAt                                             sql.NullString
		durationMS, exitCode                                sql.NullInt64
		isError                                             int
		errorMessage                                        string
		costUSD                                             sql.NullFloat64
		inputTokens, outputTokens, cacheReadTokens, turns   sql.NullInt64
		pid                                                 int
		containerID                                         string
	}

	// The stream_file prefix in the org DB for this project
	oldStreamPrefix := "projects/" + p.projectID + "/"

	var pending []execRow
	for rows.Next() {
		var r execRow
		if err := rows.Scan(
			&r.projectID, &r.profile, &r.agent, &r.container, &r.action, &r.role,
			&r.taskGroup, &r.model, &r.promptHash, &r.startedAt, &r.streamFile,
			&r.endedAt, &r.durationMS, &r.exitCode, &r.isError, &r.errorMessage,
			&r.costUSD, &r.inputTokens, &r.outputTokens, &r.cacheReadTokens, &r.turns,
			&r.pid, &r.containerID,
		); err != nil {
			return 0, fmt.Errorf("scan row: %w", err)
		}

		// Rewrite stream_file: "projects/<id>/roles/sec/logs/..." -> "logs/roles/sec/..."
		if strings.HasPrefix(r.streamFile, oldStreamPrefix) {
			oldSuffix := r.streamFile[len(oldStreamPrefix):]
			r.streamFile = rewriteStreamPath(oldSuffix)
		}

		pending = append(pending, r)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	if len(pending) == 0 {
		return 0, nil
	}

	if dryRun {
		fmt.Printf("  %s%d DB row(s) to copy\n", prefix, len(pending))
		return len(pending), nil
	}

	// Open (or create) the project DB for writing.
	projDBPath := filepath.Join(p.ateamDir, "state.sqlite")
	cdb, err := calldb.Open(projDBPath)
	if err != nil {
		return 0, fmt.Errorf("open project DB: %w", err)
	}
	defer cdb.Close()

	tx, err := cdb.RawDB().Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(`
		INSERT INTO agent_execs (
			project_id, profile, agent, container, action, role,
			task_group, model, prompt_hash, started_at, stream_file,
			ended_at, duration_ms, exit_code, is_error, error_message,
			cost_usd, input_tokens, output_tokens, cache_read_tokens, turns,
			pid, container_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	for _, r := range pending {
		if _, err := stmt.Exec(
			"", r.profile, r.agent, r.container, r.action, r.role,
			r.taskGroup, r.model, r.promptHash, r.startedAt, r.streamFile,
			r.endedAt, r.durationMS, r.exitCode, r.isError, r.errorMessage,
			r.costUSD, r.inputTokens, r.outputTokens, r.cacheReadTokens, r.turns,
			r.pid, r.containerID,
		); err != nil {
			return 0, fmt.Errorf("insert row: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}

	fmt.Printf("  copied %d DB row(s)\n", len(pending))
	return len(pending), nil
}

// rewriteStreamPath converts the path suffix after "projects/<id>/" to the new layout.
// Old: "roles/<role>/logs/<file>"      -> "logs/roles/<role>/<file>"
// Old: "supervisor/logs/<file>"        -> "logs/supervisor/<file>"
// Other paths are placed under "logs/" as-is.
func rewriteStreamPath(oldSuffix string) string {
	parts := strings.SplitN(oldSuffix, "/", 3)

	// "roles/<role>/logs/<file>" -> 3+ parts where parts[0]="roles"
	if len(parts) >= 3 && parts[0] == "roles" {
		// parts[1] = roleID, parts[2] = "logs/<file>"
		rest := parts[2]
		rest = strings.TrimPrefix(rest, "logs/")
		return "logs/roles/" + parts[1] + "/" + rest
	}

	// "supervisor/logs/<file>" -> parts[0]="supervisor"
	if len(parts) >= 2 && parts[0] == "supervisor" {
		rest := strings.Join(parts[1:], "/")
		rest = strings.TrimPrefix(rest, "logs/")
		return "logs/supervisor/" + rest
	}

	return "logs/" + oldSuffix
}
