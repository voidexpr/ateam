package cmd

import (
	"fmt"
	"os"
	"strings"

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
	Use:   "cat ID|FILE [...]",
	Short: "Pretty-print stream logs by call ID or file path",
	Long: `Read and format stream logs for one or more completed runs.

Arguments can be numeric call IDs or file paths to JSONL stream files.

Example:
  ateam cat 42
  ateam cat 42 43 44 --verbose
  ateam cat 42 --no-color
  ateam cat .ateam/logs/roles/security/stream.jsonl`,
	Args: cobra.MinimumNArgs(1),
	RunE: runCat,
}

func init() {
	catCmd.Flags().BoolVar(&catVerbose, "verbose", false, "show full tool inputs and text content")
	catCmd.Flags().BoolVar(&catNoColor, "no-color", false, "disable color output")
}

func runCat(cmd *cobra.Command, args []string) error {
	if looksLikeFiles(args) {
		return runCatFiles(args)
	}
	return runCatIDs(args)
}

func looksLikeFiles(args []string) bool {
	for _, a := range args {
		if strings.ContainsAny(a, "/.") {
			return true
		}
	}
	return false
}

func runCatFiles(paths []string) error {
	color := !catNoColor && isTerminal()

	for i, path := range paths {
		if i > 0 {
			fmt.Println()
		}
		if len(paths) > 1 {
			fmt.Printf("═══ %s ═══\n", path)
		}
		f := &runner.StreamFormatter{
			Verbose: catVerbose,
			Color:   color,
		}
		if err := f.FormatFile(path, os.Stdout); err != nil {
			return fmt.Errorf("error reading %s: %w", path, err)
		}
	}
	return nil
}

func runCatIDs(args []string) error {
	ids, err := parseIDArgs(args)
	if err != nil {
		return err
	}

	env, err := root.Resolve(orgFlag, projectFlag)
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
