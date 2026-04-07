package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ateam/internal/config"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runtime"
	"github.com/ateam/internal/secret"
	"github.com/spf13/cobra"
)

var envClaudeSandbox bool

var envCmd = &cobra.Command{
	Use:   "env",
	Short: "Show the current ATeam environment",
	Long: `Print organization, project status, and latest report/review timestamps.

This command is read-only — it never creates or modifies anything.`,
	Args: cobra.NoArgs,
	RunE: runEnv,
}

func init() {
	envCmd.Flags().BoolVar(&envClaudeSandbox, "claude-sandbox", false, "print the generated Claude sandbox settings JSON")
}

func runEnv(cmd *cobra.Command, args []string) error {
	env, err := root.Lookup(orgFlag, projectFlag)
	if err != nil {
		fmt.Printf("Org: (not found — run 'ateam install' to set up)\n")
		return nil
	}

	if envClaudeSandbox {
		return printClaudeSandbox(env)
	}

	return printEnv(env)
}

func printClaudeSandbox(env *root.ResolvedEnv) error {
	r, err := newRunnerDefault(env)
	if err != nil {
		return err
	}
	data, err := r.RenderSettings(env.SourceDir)
	if err != nil {
		return err
	}
	if data == nil {
		fmt.Println("No sandbox settings configured for the default profile.")
		return nil
	}
	fmt.Println(string(data))
	return nil
}

func printEnv(env *root.ResolvedEnv) error {
	cwd, err := resolvedCwd()
	if err != nil {
		return err
	}

	orgRoot := env.OrgRoot()
	relOrg, _ := filepath.Rel(cwd, orgRoot)
	fmt.Printf("Org: %s (%s)\n", relOrg, tildeHome(orgRoot))

	printRuntimeSection(env, cwd)

	if env.ProjectDir == "" {
		return nil
	}

	printProjectSection(env, cwd)

	return nil
}

func printRuntimeSection(env *root.ResolvedEnv, cwd string) {
	fmt.Println("\nRuntime")

	printAuthLines(env)

	// Config resolution chain
	chain := []string{"built-in"}
	var candidates []string
	if env.OrgDir != "" {
		candidates = append(candidates,
			filepath.Join(env.OrgDir, "defaults", "runtime.hcl"),
			filepath.Join(env.OrgDir, "runtime.hcl"),
		)
	}
	if env.ProjectDir != "" {
		candidates = append(candidates, filepath.Join(env.ProjectDir, "runtime.hcl"))
	}
	for _, p := range candidates {
		if fileOrSymlinkExists(p) {
			chain = append(chain, relPath(cwd, p))
		}
	}
	fmt.Printf("  Config: %s\n", strings.Join(chain, " → "))

	rtCfg, err := runtime.Load(env.ProjectDir, env.OrgDir)
	if err != nil {
		return
	}

	if prof, _, _, err := rtCfg.ResolveProfile("default"); err == nil {
		fmt.Printf("  Default profile: agent=%s, container=%s\n", prof.Agent, prof.Container)
	}

	var names []string
	for name := range rtCfg.Profiles {
		names = append(names, name)
	}
	if len(names) > 0 {
		sort.Strings(names)
		fmt.Printf("  Profiles: %s\n", strings.Join(names, ", "))
	}

	printDockerfileLine(env, cwd)
}

func printAuthLines(env *root.ResolvedEnv) {
	resolver := secretResolver(env, secret.DefaultBackend())

	authVars := []string{"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"}
	anyFound := false
	for i, name := range authVars {
		result := resolver.Resolve(name)
		if !result.Found {
			continue
		}
		prefix := "  Auth: "
		if i > 0 && anyFound {
			prefix = "        "
		}
		fmt.Printf("%s%s=%s (%s, %s)\n", prefix, name, maskEnvVar(result.Value), result.Source, result.Backend)
		anyFound = true

		// Check for project/global override
		if result.Source == "project" || result.Source == "env" {
			globalResolver := secret.NewResolver("", "", secret.DefaultBackend(), nil)
			globalResult := globalResolver.Resolve(name)
			if globalResult.Found && globalResult.Source != "env" && globalResult.Value != result.Value {
				fmt.Printf("        ⚠ project value overrides global value\n")
			}
		}
	}
	if !anyFound {
		fmt.Println("  Auth: (none) — run 'ateam secret ANTHROPIC_API_KEY' to configure")
	}
}

func maskEnvVar(val string) string {
	if len(val) <= 8 {
		return "***"
	}
	return val[:4] + "..." + val[len(val)-4:]
}

func printDockerfileLine(env *root.ResolvedEnv, cwd string) {
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
			fmt.Printf("  Dockerfile: %s\n", relPath(cwd, path))
			return
		}
	}
	fmt.Println("  Dockerfile: (built-in)")
}

func printProjectSection(env *root.ResolvedEnv, cwd string) {
	fmt.Printf("\nProject: %s\n", env.ProjectName)

	fmt.Printf("  DB: %s\n", env.ProjectDBPath())
	if env.GitRepoDir != "" {
		fmt.Printf("  Git: %s (%s)\n", env.RelPath(env.GitRepoDir), tildeHome(env.GitRepoDir))
	}
	if env.Config != nil && env.Config.Git.RemoteOriginURL != "" {
		fmt.Printf("  Remote: %s\n", env.Config.Git.RemoteOriginURL)
	}

	if env.Config == nil || len(env.Config.Roles) == 0 {
		return
	}

	// Collect all role names, sorted
	var allRoles []string
	for name := range env.Config.Roles {
		allRoles = append(allRoles, name)
	}
	sort.Strings(allRoles)

	fmt.Println()
	w := newTable()
	fmt.Fprintln(w, " \tROLE\tLAST\tPATH")
	for _, roleID := range allRoles {
		enabled := config.IsRoleEnabled(env.Config.Roles[roleID])
		reportPath := env.RoleReportPath(roleID)
		fi, err := os.Stat(reportPath)
		hasReport := err == nil

		if !enabled && !hasReport {
			continue
		}

		status := "✓"
		if !enabled {
			status = "-"
		}
		if hasReport {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", status, roleID, fmtDateAge(fi.ModTime()), relPath(cwd, reportPath))
		} else {
			fmt.Fprintf(w, "%s\t%s\t-\t\n", status, roleID)
		}
	}

	reviewPath := env.ReviewPath()
	if fi, err := os.Stat(reviewPath); err == nil {
		fmt.Fprintf(w, " \t%s\t%s\t%s\n", "review", fmtDateAge(fi.ModTime()), relPath(cwd, reviewPath))
	}
	w.Flush()
}

// fileOrSymlinkExists returns true if path exists as a file or symlink.
func fileOrSymlinkExists(path string) bool {
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	_, err = os.Lstat(path)
	return err == nil
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
