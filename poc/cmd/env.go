package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ateam-poc/internal/prompts"
	"github.com/ateam-poc/internal/root"
	"github.com/ateam-poc/internal/runtime"
	"github.com/spf13/cobra"
)

var envCmd = &cobra.Command{
	Use:   "env",
	Short: "Show the current ATeam environment",
	Long: `Print organization, project status, and latest report/review timestamps.

This command is read-only — it never creates or modifies anything.`,
	Args: cobra.NoArgs,
	RunE: runEnv,
}

func runEnv(cmd *cobra.Command, args []string) error {
	// Show auth status first — works even without .ateamorg
	printAuthStatus()

	env, err := root.Lookup()
	if err != nil {
		fmt.Printf("     Org: (not found — run 'ateam install' to set up)\n")
		return nil
	}

	orgRoot := env.OrgRoot()
	cwd, err := resolvedCwd()
	if err != nil {
		return err
	}

	relOrg, _ := filepath.Rel(cwd, orgRoot)
	fmt.Printf("     Org: %s (%s)\n", relOrg, tildeHome(orgRoot))

	// Show runtime.hcl resolution
	printRuntimePaths(env, cwd)

	if env.ProjectDir == "" {
		return nil
	}

	fmt.Printf(" Project: %s\n", env.ProjectName)

	if env.StateDir != "" {
		fmt.Printf("   State: %s\n", env.RelPath(env.StateDir))
	}

	if env.GitRepoDir != "" {
		fmt.Printf("     Git: %s (%s)\n", env.RelPath(env.GitRepoDir), tildeHome(env.GitRepoDir))
	}
	if env.Config != nil && env.Config.Git.RemoteOriginURL != "" {
		fmt.Printf("  Remote: %s\n", env.Config.Git.RemoteOriginURL)
	}

	if env.Config != nil {
		roles := env.Config.EnabledRoles()
		if len(roles) > 0 {
			fmt.Printf("   Roles: %s\n", strings.Join(roles, ", "))

			fmt.Println()
			w := newTable()
			fmt.Fprintln(w, "ROLE\tLAST\tPATH")
			for _, roleID := range roles {
				reportPath := filepath.Join(env.ProjectDir, "roles", roleID, prompts.ReportFile)
				if fi, err := os.Stat(reportPath); err == nil {
					fmt.Fprintf(w, "%s\t%s\t%s\n", roleID, fmtDateAge(fi.ModTime()), relPath(cwd, reportPath))
				} else {
					fmt.Fprintf(w, "%s\t-\t\n", roleID)
				}
			}

			reviewPath := filepath.Join(env.ProjectDir, "supervisor", "review.md")
			if fi, err := os.Stat(reviewPath); err == nil {
				fmt.Fprintf(w, "%s\t%s\t%s\n", "review", fmtDateAge(fi.ModTime()), relPath(cwd, reviewPath))
			}
			w.Flush()
		}
	}

	return nil
}

func printAuthStatus() {
	oauth := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN")
	apiKey := os.Getenv("ANTHROPIC_API_KEY")

	if oauth == "" && apiKey == "" {
		fmt.Println("    Auth: (none)")
		return
	}

	// CLAUDE_CODE_OAUTH_TOKEN takes precedence
	if oauth != "" {
		label := "active"
		if apiKey != "" {
			label = "active, takes precedence"
		}
		fmt.Printf("    Auth: CLAUDE_CODE_OAUTH_TOKEN=%s (%s)\n", maskEnvVar(oauth), label)
	}
	if apiKey != "" {
		label := "active"
		if oauth != "" {
			label = "set but unused (CLAUDE_CODE_OAUTH_TOKEN takes precedence)"
		}
		fmt.Printf("          ANTHROPIC_API_KEY=%s (%s)\n", maskEnvVar(apiKey), label)
	}
}

func maskEnvVar(val string) string {
	if len(val) <= 8 {
		return "***"
	}
	return val[:4] + "..." + val[len(val)-4:]
}

func printRuntimePaths(env *root.ResolvedEnv, cwd string) {
	// Build the resolution chain: embedded -> org/defaults -> org -> project
	var paths []string
	if env.OrgDir != "" {
		paths = append(paths,
			filepath.Join(env.OrgDir, "defaults", "runtime.hcl"),
			filepath.Join(env.OrgDir, "runtime.hcl"),
		)
	}
	if env.ProjectDir != "" {
		paths = append(paths, filepath.Join(env.ProjectDir, "runtime.hcl"))
	}

	fmt.Print(" Runtime: (embedded defaults)")
	for _, p := range paths {
		if fileOrSymlinkExists(p) {
			fmt.Printf(" → %s", relPath(cwd, p))
		}
	}
	fmt.Println()

	// Show loaded profiles and Dockerfile resolution
	rtCfg, err := runtime.Load(env.ProjectDir, env.OrgDir)
	if err == nil {
		var names []string
		for name := range rtCfg.Profiles {
			names = append(names, name)
		}
		if len(names) > 0 {
			sort.Strings(names)
			fmt.Printf("Profiles: %s\n", strings.Join(names, ", "))
		}

		printDockerfilePath(env, cwd)
	}
}

// fileOrSymlinkExists returns true if path exists as a file or symlink.
// Uses Lstat first so broken symlinks are still detected, then Stat to confirm.
func fileOrSymlinkExists(path string) bool {
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	// Check for broken symlink
	_, err = os.Lstat(path)
	return err == nil
}

func printDockerfilePath(env *root.ResolvedEnv, cwd string) {
	// Show the Dockerfile resolution chain (same order as runtime.ResolveDockerfile)
	var candidates []string
	if env.ProjectDir != "" {
		candidates = append(candidates, filepath.Join(env.ProjectDir, "Dockerfile"))
	}
	if env.OrgDir != "" {
		candidates = append(candidates, filepath.Join(env.OrgDir, "Dockerfile"))
		candidates = append(candidates, filepath.Join(env.OrgDir, "defaults", "Dockerfile"))
	}

	for _, path := range candidates {
		if fileOrSymlinkExists(path) {
			fmt.Printf("  Docker: %s\n", relPath(cwd, path))
			return
		}
	}
	fmt.Println("  Docker: (embedded default)")
}

func tildeHome(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}

