package cmd

import (
	"fmt"
	"os"
	"strconv"

	"github.com/ateam-poc/internal/root"
	"github.com/ateam-poc/internal/runner"
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
	ids := make([]int64, len(args))
	for i, arg := range args {
		id, err := strconv.ParseInt(arg, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid ID %q: %w", arg, err)
		}
		ids[i] = id
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

	rows, err := db.CallsByIDs(ids)
	if err != nil {
		return fmt.Errorf("query failed: %w", err)
	}
	if len(rows) == 0 {
		return fmt.Errorf("no calls found for given IDs")
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

		f := &runner.StreamFormatter{
			Verbose: catVerbose,
			Color:   color,
		}
		if err := f.FormatFile(row.StreamFile, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "  error reading %s: %v\n", row.StreamFile, err)
		}
	}

	return nil
}
