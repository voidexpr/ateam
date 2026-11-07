package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ateam/defaults"
	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/ateam/internal/runtime"
	"github.com/spf13/cobra"
)

var (
	catVerbose  bool
	catNoColor  bool
	catLast     bool
	catDefaults bool
)

var catCmd = &cobra.Command{
	Use:   "cat [ID|FILE...]",
	Short: "Pretty-print stream logs by call ID or file path",
	Long: `Read and format stream logs for one or more completed runs.

Arguments can be numeric call IDs or file paths to JSONL stream files.
Pass --last instead of an ID to format the most recent run.

With --defaults, print the built-in defaults instead: the embedded
ateam.hcl (no argument), the Dockerfile, or a prompt by its path.

Example:
  ateam cat 42
  ateam cat 42 43 44 --verbose
  ateam cat 42 --no-color
  ateam cat --last
  ateam cat .ateam/logs/roles/security/agent.jsonl
  ateam cat --defaults
  ateam cat --defaults Dockerfile
  ateam cat --defaults prompts/report/project.security.prompt.md`,
	RunE: runCat,
}

func init() {
	catCmd.Flags().BoolVar(&catVerbose, "verbose", false, "show full tool inputs and text content")
	catCmd.Flags().BoolVar(&catNoColor, "no-color", false, "disable color output")
	catCmd.Flags().BoolVar(&catLast, "last", false, "format the most recent run (when no ID is given)")
	catCmd.Flags().BoolVar(&catDefaults, "defaults", false, "print the built-in defaults: ateam.hcl, Dockerfile, or an embedded prompt path")
}

func runCat(cmd *cobra.Command, args []string) error {
	if catDefaults {
		return runCatDefaults(args)
	}
	if len(args) == 0 && !catLast {
		return fmt.Errorf("specify an ID, file path, or --last")
	}
	if len(args) > 0 && looksLikeFiles(args) {
		return runCatFiles(args)
	}
	return runCatIDs(args)
}

// runCatDefaults prints embedded default files: ateam.hcl when no path is
// given. Paths are relative to the embedded defaults tree.
func runCatDefaults(paths []string) error {
	if len(paths) == 0 {
		paths = []string{runtime.ConfigFile}
	}
	for i, p := range paths {
		data, err := defaults.FS.ReadFile(p)
		if err != nil {
			return fmt.Errorf("no built-in default %q (try ateam.hcl, Dockerfile, or prompts/...)", p)
		}
		if i > 0 {
			fmt.Println()
		}
		if len(paths) > 1 {
			fmt.Printf("═══ %s ═══\n", p)
		}
		os.Stdout.Write(data)
	}
	return nil
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
		var sessionStart time.Time
		if t, ok := runner.ParseTimestampPrefix(filepath.Base(path)); ok {
			sessionStart = t
		}
		f := &runner.StreamFormatter{
			Verbose:      catVerbose,
			Color:        color,
			SessionStart: sessionStart,
		}
		if err := f.FormatFile(path, os.Stdout); err != nil {
			return fmt.Errorf("error reading %s: %w", path, err)
		}
	}
	return nil
}

func runCatIDs(args []string) error {
	env, err := lookupEnv()
	if err != nil {
		return fmt.Errorf("cannot find project: %w", err)
	}

	db, err := requireStateDB(env)
	if err != nil {
		return err
	}
	defer db.Close()

	ids, err := resolveExecIDs(db, args, catLast)
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

	// Load runtime config for agent pricing lookup.
	rtCfg, _ := loadRuntime(env)
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

		if row.AgentFile == "" {
			fmt.Println("  (no stream file recorded)")
			continue
		}

		streamPath := root.ResolveStreamPath(env.StateDir(), row.AgentFile)

		pricing, defaultModel := agentPricing(row.Agent)
		var sessionStart time.Time
		if t, err := time.Parse(time.RFC3339, row.StartedAt); err == nil {
			sessionStart = t
		}
		f := &runner.StreamFormatter{
			Verbose:      catVerbose,
			Color:        color,
			Model:        row.Model,
			DefaultModel: defaultModel,
			Pricing:      pricing,
			SessionStart: sessionStart,
		}
		if err := f.FormatFile(streamPath, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "  error reading %s: %v\n", row.AgentFile, err)
		}
	}

	return nil
}
