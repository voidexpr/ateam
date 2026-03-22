package cmd

import (
	"fmt"
	"os"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/ateam/internal/runtime"
	"github.com/spf13/cobra"
)

var (
	catVerbose bool
	catNoColor bool
)

var catCmd = &cobra.Command{
	Use:   "cat ID [ID...]",
	Short: "Pretty-print stream logs by call ID",
	Long: `Read and format stream logs for one or more completed runs.

Example:
  ateam cat 42
  ateam cat 42 43 44 --verbose
  ateam cat 42 --no-color`,
	Args: cobra.MinimumNArgs(1),
	RunE: runCat,
}

func init() {
	catCmd.Flags().BoolVar(&catVerbose, "verbose", false, "show full tool inputs and text content")
	catCmd.Flags().BoolVar(&catNoColor, "no-color", false, "disable color output")
}

func runCat(cmd *cobra.Command, args []string) error {
	ids, err := parseIDArgs(args)
	if err != nil {
		return err
	}

	env, err := root.Lookup()
	if err != nil {
		return fmt.Errorf("cannot find project: %w", err)
	}

	db := openProjectDB(env)
	if db == nil {
		return fmt.Errorf("cannot open call database")
	}
	defer db.Close()

	rows, err := db.CallsByIDs(ids)
	if err != nil {
		return fmt.Errorf("query failed: %w", err)
	}
	if len(rows) == 0 {
		return fmt.Errorf("no calls found for given IDs")
	}

	// Load runtime config for agent pricing lookup.
	rtCfg, _ := runtime.Load(env.ProjectDir, env.OrgDir)
	agentPricing := func(agentName string) (agent.PricingTable, string) {
		if rtCfg == nil {
			return nil, ""
		}
		ac, ok := rtCfg.Agents[agentName]
		if !ok {
			return nil, ""
		}
		return buildPricingFromConfig(ac.Pricing)
	}

	color := !catNoColor && isTerminal()

	for i, row := range rows {
		if i > 0 {
			fmt.Println()
		}

		header := fmt.Sprintf("═══ [ID:%d] %s/%s %s ═══", row.ID, row.Role, row.Action, row.StartedAt)
		fmt.Println(header)

		if row.StreamFile == "" {
			fmt.Println("  (no stream file recorded)")
			continue
		}

		streamPath := root.ResolveStreamPath(env.ProjectDir, env.OrgDir, row.StreamFile)

		pricing, defaultModel := agentPricing(row.Agent)
		f := &runner.StreamFormatter{
			Verbose:      catVerbose,
			Color:        color,
			Model:        row.Model,
			DefaultModel: defaultModel,
			Pricing:      pricing,
		}
		if err := f.FormatFile(streamPath, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "  error reading %s: %v\n", row.StreamFile, err)
		}
	}

	return nil
}
