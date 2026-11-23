package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/ateam/internal/runtime"
	"github.com/ateam/internal/sandboxpolicy"
	"github.com/spf13/cobra"
)

var filterShowPaths bool

var filterCmd = &cobra.Command{
	Use:   "filter",
	Short: "List and inspect the filters profiles name in read, write and exclude",
}

func loadFilters() (*runtime.Config, error) {
	_, _, layers, err := launchPlaces()
	if err != nil {
		return nil, err
	}
	return runtime.Load(layers)
}

var filterListCmd = &cobra.Command{
	Use:   "list",
	Short: "List every filter and where it is defined",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadFilters()
		if err != nil {
			return err
		}
		names := sandboxpolicy.FilterSet(cfg.Filters).Names()
		for _, name := range names {
			src, _ := cfg.Source("filter." + name + ".name")
			fmt.Printf("%-24s %s\n", name, src.Layer)
		}
		return nil
	},
}

var filterShowCmd = &cobra.Command{
	Use:   "show NAME",
	Short: "Print a filter as written, or with --paths the paths it resolves to",
	Long: `Print a filter in the shared filter syntax, as written. With --paths,
print the absolute paths it includes and excludes on this machine, the
filters it uses included: what a profile naming it gets.

Example:
  ateam filter show agent/secrets
  ateam filter show dev/go-write --paths`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadFilters()
		if err != nil {
			return err
		}
		filters := sandboxpolicy.FilterSet(cfg.Filters)
		text, ok := filters[args[0]]
		if !ok {
			return fmt.Errorf("unknown filter %q (known: %s)", args[0], strings.Join(filters.Names(), ", "))
		}
		if !filterShowPaths {
			fmt.Print(text)
			if !strings.HasSuffix(text, "\n") {
				fmt.Println()
			}
			return nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		include, exclude, err := filters.Paths(args[0], home)
		if err != nil {
			return err
		}
		sort.Strings(include)
		sort.Strings(exclude)
		for _, path := range include {
			fmt.Printf("include  %s\n", path)
		}
		for _, path := range exclude {
			fmt.Printf("exclude  %s\n", path)
		}
		return nil
	},
}

func init() {
	filterShowCmd.Flags().BoolVar(&filterShowPaths, "paths", false, "print the absolute paths the filter resolves to")
	filterCmd.AddCommand(filterListCmd, filterShowCmd)
	rootCmd.AddCommand(filterCmd)
}
