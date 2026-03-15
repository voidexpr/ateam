package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/ateam-poc/internal/root"
	"github.com/ateam-poc/internal/runner"
	"github.com/spf13/cobra"
)

var (
	tailReports bool
	tailCoding  bool
	tailVerbose bool
	tailNoColor bool
)

var tailCmd = &cobra.Command{
	Use:   "tail [ID...]",
	Short: "Live-stream agent output",
	Long: `Stream live output from running agents.

Modes:
  ateam tail ID [ID...]     Tail specific calls by ID
  ateam tail --reports       Tail all current report runs
  ateam tail --coding        Tail current coding session (supervisor + sub-runs)

Options:
  --verbose        Show full tool inputs and text content
  --no-color       Disable color output`,
	RunE: runTail,
}

func init() {
	tailCmd.Flags().BoolVar(&tailReports, "reports", false, "tail all current report runs")
	tailCmd.Flags().BoolVar(&tailCoding, "coding", false, "tail current coding session")
	tailCmd.Flags().BoolVar(&tailVerbose, "verbose", false, "show full tool inputs and text content")
	tailCmd.Flags().BoolVar(&tailNoColor, "no-color", false, "disable color output")
}

func runTail(cmd *cobra.Command, args []string) error {
	if len(args) == 0 && !tailReports && !tailCoding {
		return fmt.Errorf("specify call IDs, --reports, or --coding")
	}

	env, err := root.Lookup()
	if err != nil {
		return fmt.Errorf("cannot find .ateamorg/: %w", err)
	}

	db := openCallDB(env.OrgDir)
	if db == nil {
		return fmt.Errorf("cannot open call database")
	}
	defer db.Close()

	color := !tailNoColor && isTerminal()
	tailer := runner.NewTailer(os.Stderr, db, color, tailVerbose)

	projectID := env.ProjectID()

	switch {
	case len(args) > 0:
		ids := make([]int64, len(args))
		for i, arg := range args {
			id, err := strconv.ParseInt(arg, 10, 64)
			if err != nil {
				return fmt.Errorf("invalid ID %q: %w", arg, err)
			}
			ids[i] = id
		}
		rows, err := db.CallsByIDs(ids)
		if err != nil {
			return fmt.Errorf("query failed: %w", err)
		}
		if len(rows) == 0 {
			return fmt.Errorf("no calls found for given IDs")
		}
		for _, r := range rows {
			if r.StreamFile != "" {
				tailer.AddSource(r.ID, r.Role, r.Action, r.StreamFile)
			}
		}

	case tailReports:
		if projectID == "" {
			return fmt.Errorf("--reports requires a project context (run from within a project)")
		}
		tailer.Action = runner.ActionReport
		tailer.ProjectID = projectID

	case tailCoding:
		if projectID == "" {
			return fmt.Errorf("--coding requires a project context (run from within a project)")
		}
		tg, err := db.LatestTaskGroup(projectID, "code-")
		if err != nil {
			return fmt.Errorf("cannot find coding session: %w", err)
		}
		if tg == "" {
			return fmt.Errorf("no coding session found for this project")
		}
		tailer.TaskGroup = tg
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	return tailer.Run(ctx)
}
