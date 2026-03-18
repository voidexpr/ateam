package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/ateam/internal/runtime"
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
  ateam tail                 Tail all running processes (default)
  ateam tail ID [ID...]      Tail specific calls by ID
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

	// Load runtime config to provide pricing for cost estimation.
	if rtCfg, err := runtime.Load(env.ProjectDir, env.OrgDir); err == nil {
		tailer.Pricing, tailer.DefaultModel = mergedPricingFromConfig(rtCfg)
	}

	projectID := env.ProjectID()

	switch {
	case len(args) > 0:
		ids, err := parseIDArgs(args)
		if err != nil {
			return err
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
				tailer.AddSource(r.ID, r.Role, r.Action, r.StreamFile, r.Model)
			}
		}

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

	case tailReports:
		if projectID == "" {
			return fmt.Errorf("--reports requires a project context (run from within a project)")
		}
		tailer.Action = runner.ActionReport
		tailer.ProjectID = projectID

	default:
		// No args, no flags: tail everything running for this project
		if projectID == "" {
			return fmt.Errorf("no project context found (run from within a project)")
		}
		tailer.ProjectID = projectID
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	return tailer.Run(ctx)
}
