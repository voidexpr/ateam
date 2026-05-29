package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/ateam/internal/runtime"
	"github.com/spf13/cobra"
)

var (
	tailReports      bool
	tailCoding       bool
	tailLast         bool
	tailVerbose      bool
	tailNoColor      bool
	tailFinalMessage bool
)

var tailCmd = &cobra.Command{
	Use:   "tail [ID...]",
	Short: "Live-stream agent output",
	Long: `Stream live output from running agents.

Modes:
  ateam tail                 Tail all running processes (default)
  ateam tail ID [ID...]      Tail specific calls by ID
  ateam tail --last          Tail the most recent run
  ateam tail --reports       Tail all current report runs
  ateam tail --coding        Tail current coding session (supervisor + sub-runs)

Options:
  --verbose          Show full tool inputs and text content
  --no-color         Disable color output
  --final-message    Suppress streaming output; wait for each run to finish
                     and emit one JSONL line per run on stdout with the call
                     metadata and the final assistant message. Pipelines
                     nicely with jq. Exits non-zero if any run errored.`,
	RunE: runTail,
}

func init() {
	tailCmd.Flags().BoolVar(&tailReports, "reports", false, "tail all current report runs")
	tailCmd.Flags().BoolVar(&tailCoding, "coding", false, "tail current coding session")
	tailCmd.Flags().BoolVar(&tailLast, "last", false, "tail the most recent run")
	tailCmd.Flags().BoolVar(&tailVerbose, "verbose", false, "show full tool inputs and text content")
	tailCmd.Flags().BoolVar(&tailNoColor, "no-color", false, "disable color output")
	tailCmd.Flags().BoolVar(&tailFinalMessage, "final-message", false, "wait for runs to finish and emit one JSONL line per run with metadata and final assistant text")
	tailCmd.MarkFlagsMutuallyExclusive("last", "reports", "coding")
}

func runTail(cmd *cobra.Command, args []string) error {
	env, err := lookupEnv()
	if err != nil {
		return fmt.Errorf("cannot find project: %w", err)
	}

	db, err := requireStateDB(env)
	if err != nil {
		return err
	}
	defer db.Close()

	color := !tailNoColor && isTerminal()
	writer := io.Writer(os.Stderr)
	if tailFinalMessage {
		// JSONL must go to stdout so it composes with shell pipelines.
		writer = os.Stdout
		color = false
	}
	tailer := runner.NewTailer(writer, db, color, tailVerbose)
	tailer.ProjectDir = env.ProjectDir
	tailer.OrgDir = env.OrgDir
	tailer.FinalMessageOnly = tailFinalMessage

	// Load runtime config to provide pricing for cost estimation.
	if rtCfg, err := runtime.Load(env.ProjectDir, env.OrgDir); err == nil {
		tailer.Pricing, tailer.DefaultModel = mergedPricingFromConfig(rtCfg)
	}

	hasProject := env.ProjectDir != ""

	switch {
	case len(args) > 0 || tailLast:
		ids, err := resolveExecIDs(db, args, tailLast)
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
				tailer.AddSource(r.ID, r.Role, r.Action, root.ResolveStreamPath(env.ProjectDir, env.OrgDir, r.StreamFile), r.Model)
			}
		}

	case tailCoding:
		if !hasProject {
			return fmt.Errorf("--coding requires a project context (run from within a project)")
		}
		batch, err := db.LatestBatch(env.ProjectID(), "code-")
		if err != nil {
			return fmt.Errorf("cannot find coding session: %w", err)
		}
		if batch == "" {
			return fmt.Errorf("no coding session found for this project")
		}
		tailer.Batch = batch

	case tailReports:
		if !hasProject {
			return fmt.Errorf("--reports requires a project context (run from within a project)")
		}
		tailer.Action = runner.ActionReport
		tailer.ProjectID = env.ProjectID()
		tailer.DiscoverAll = true

	default:
		if !hasProject {
			return fmt.Errorf("no project context found (run from within a project)")
		}
		tailer.ProjectID = env.ProjectID()
		tailer.DiscoverAll = true
	}

	ctx, stop := cmdContext()
	defer stop()

	if err := tailer.Run(ctx); err != nil {
		return err
	}
	if tailFinalMessage && tailer.AnyError {
		return fmt.Errorf("one or more runs ended in error")
	}
	return nil
}
