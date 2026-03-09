package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ateam-poc/internal/root"
	"github.com/ateam-poc/internal/runner"
	"github.com/spf13/cobra"
)

var (
	logSupervisor bool
	logAgent      string
	logAction     string
)

var logCmd = &cobra.Command{
	Use:   "log",
	Short: "Pretty-print the last run stream log",
	Long: `Read and format the last_run_stream.jsonl of the supervisor or a specific agent.

Example:
  ateam log --supervisor
  ateam log --agent security
  ateam log --agent security --action run`,
	RunE: runLog,
}

func init() {
	logCmd.Flags().BoolVar(&logSupervisor, "supervisor", false, "show supervisor log (defaults to code action)")
	logCmd.Flags().StringVar(&logAgent, "agent", "", "agent name to show log for")
	logCmd.Flags().StringVar(&logAction, "action", "", "action type (e.g. report, run, code, review; auto-detected if omitted)")
}

func runLog(cmd *cobra.Command, args []string) error {
	if !logSupervisor && logAgent == "" {
		return fmt.Errorf("specify --supervisor or --agent AGENT")
	}
	if logSupervisor && logAgent != "" {
		return fmt.Errorf("use either --supervisor or --agent, not both")
	}

	env, err := root.Resolve(orgFlag, projectFlag)
	if err != nil {
		return err
	}

	var logsDir string
	if logSupervisor {
		action := logAction
		if action == "" {
			action = "code"
		}
		logsDir = env.SupervisorLogsDir(action)
	} else {
		action := logAction
		if action == "" {
			action = "run"
		}
		logsDir = env.AgentLogsDir(logAgent, action)
	}

	streamPath := filepath.Join(logsDir, "last_run_stream.jsonl")
	if _, err := os.Stat(streamPath); err != nil {
		return fmt.Errorf("no stream log found at %s", streamPath)
	}

	return runner.FormatStream(streamPath, os.Stdout)
}
