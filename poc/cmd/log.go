package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/ateam-poc/internal/root"
	"github.com/ateam-poc/internal/runner"
	"github.com/spf13/cobra"
)

var (
	logSupervisor bool
	logRole       string
	logAction     string
)

var logCmd = &cobra.Command{
	Use:   "log",
	Short: "Pretty-print the last run stream log",
	Long: `Read and format the latest stream log of the supervisor or a specific role.

Example:
  ateam log --supervisor
  ateam log --role security
  ateam log --role security --action run`,
	RunE: runLog,
}

func init() {
	logCmd.Flags().BoolVar(&logSupervisor, "supervisor", false, "show supervisor log (defaults to code action)")
	logCmd.Flags().StringVar(&logRole, "role", "", "role name to show log for")
	logCmd.Flags().StringVar(&logAction, "action", "", "action type (e.g. report, run, code, review; auto-detected if omitted)")
}

func runLog(cmd *cobra.Command, args []string) error {
	if !logSupervisor && logRole == "" {
		return fmt.Errorf("specify --supervisor or --role ROLE")
	}
	if logSupervisor && logRole != "" {
		return fmt.Errorf("use either --supervisor or --role, not both")
	}

	env, err := root.Resolve(orgFlag, projectFlag)
	if err != nil {
		return err
	}

	var logsDir string
	if logSupervisor {
		logsDir = env.SupervisorLogsDir()
	} else {
		logsDir = env.RoleLogsDir(logRole)
	}

	streamPath, err := findLatestStreamFile(logsDir, logAction)
	if err != nil {
		return err
	}

	return runner.FormatStream(streamPath, os.Stdout)
}

// findLatestStreamFile globs for *_stream.jsonl files in logsDir,
// optionally filtered by action, and returns the most recent by name.
func findLatestStreamFile(logsDir, action string) (string, error) {
	var pattern string
	if action != "" {
		pattern = filepath.Join(logsDir, "*_"+action+"_stream.jsonl")
	} else {
		pattern = filepath.Join(logsDir, "*_stream.jsonl")
	}

	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", fmt.Errorf("cannot search for stream files: %w", err)
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no stream log found in %s", logsDir)
	}

	sort.Strings(matches)
	return matches[len(matches)-1], nil
}
