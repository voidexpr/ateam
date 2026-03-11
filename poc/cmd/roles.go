package cmd

import (
	"fmt"
	"sort"

	"github.com/ateam-poc/internal/root"
	"github.com/spf13/cobra"
)

var (
	rolesEnabled   bool
	rolesAvailable bool
)

var rolesCmd = &cobra.Command{
	Use:   "roles",
	Short: "List roles for the current project",
	Long: `List roles configured for the current project.

By default (--available), shows all known roles with their status.
With --enabled, shows only enabled roles.

Example:
  ateam roles
  ateam roles --enabled
  ateam roles --available`,
	Args: cobra.NoArgs,
	RunE: runRoles,
}

func init() {
	rolesCmd.Flags().BoolVar(&rolesEnabled, "enabled", false, "list enabled roles only")
	rolesCmd.Flags().BoolVar(&rolesAvailable, "available", false, "list all roles with status (default)")
	rolesCmd.MarkFlagsMutuallyExclusive("enabled", "available")
}

func runRoles(cmd *cobra.Command, args []string) error {
	env, err := root.Resolve(orgFlag, projectFlag)
	if err != nil {
		return err
	}

	if env.Config == nil || len(env.Config.Roles) == 0 {
		fmt.Println("No roles configured.")
		return nil
	}

	if rolesEnabled {
		roles := env.Config.EnabledRoles()
		if len(roles) == 0 {
			fmt.Println("No enabled roles.")
			return nil
		}
		for _, name := range roles {
			fmt.Println(name)
		}
		return nil
	}

	// Default: --available — all roles with status
	var names []string
	for name := range env.Config.Roles {
		names = append(names, name)
	}
	sort.Strings(names)

	w := newTable()
	fmt.Fprintln(w, "ROLE\tSTATUS")
	for _, name := range names {
		fmt.Fprintf(w, "%s\t%s\n", name, env.Config.Roles[name])
	}
	w.Flush()

	return nil
}
