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
var envPrintOrg bool

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
	envCmd.Flags().BoolVar(&envPrintOrg, "print-org", false, "print the absolute path to the org directory")
}

func runEnv(cmd *cobra.Command, args []string) error {
	env, err := root.Lookup(orgFlag, projectFlag)
	if err != nil {
		fmt.Printf("Org: (not found — run 'ateam install' to set up)\n")
		return nil
	}

	if envPrintOrg {
		fmt.Println(env.OrgDir)
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

	rtCfg, _ := runtime.Load(env.ProjectDir, env.OrgDir)
	printAuthLines(env, rtCfg)

	// Config resolution chain
	chain := []string{"built-in"}
	var brokenLinks []string
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
		if _, err := os.Stat(p); err == nil {
			chain = append(chain, relPath(cwd, p))
		} else if isBrokenSymlink(p) {
			brokenLinks = append(brokenLinks, relPath(cwd, p))
		}
	}
	fmt.Printf("  Config: %s\n", strings.Join(chain, " → "))
	for _, bl := range brokenLinks {
		fmt.Printf("  Warning: %s is a broken symlink (ignored, using built-in defaults)\n", bl)
	}

	// Check org defaults directory itself
	if env.OrgDir != "" {
		defaultsDir := filepath.Join(env.OrgDir, "defaults")
		if isBrokenSymlink(defaultsDir) {
			fmt.Printf("  Warning: %s is a broken symlink\n", relPath(cwd, defaultsDir))
		} else if info, err := os.Stat(defaultsDir); err == nil && info.IsDir() {
			entries, err := os.ReadDir(defaultsDir)
			if err != nil {
				fmt.Printf("  Warning: %s exists but cannot be read: %v\n", relPath(cwd, defaultsDir), err)
			} else if len(entries) == 0 {
				fmt.Printf("  Warning: %s is empty\n", relPath(cwd, defaultsDir))
			}
		}
	}

	if rtCfg == nil {
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

func printAuthLines(env *root.ResolvedEnv, rtCfg *runtime.Config) {
	resolver := secretResolver(env, secret.DefaultBackend())
	activeKey, defaultAgent := defaultAgentAuthStatus(rtCfg, resolver)

	authVars := []string{"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"}
	any := false
	for _, name := range authVars {
		all := resolver.ResolveAll(name)
		for i, r := range all {
			prefix := "        "
			if !any {
				prefix = "  Auth: "
			}
			annotation := ""
			switch {
			case name == activeKey && i == 0:
				annotation = " ✓ active"
			case name == activeKey:
				// Lower-priority source for the same active key.
				annotation = " ✗ ignored (shadowed by " + all[0].Source + "-scope " + name + ")"
			case activeKey != "":
				annotation = " ✗ ignored (shadowed by " + activeKey + ")"
			}
			fmt.Printf("%s%s=%s (%s, %s)%s\n", prefix, name, secret.MaskValue(r.Value), r.Source, r.Backend, annotation)
			any = true
		}
	}

	if !any {
		fmt.Println("  Auth: (none) — run 'ateam secret ANTHROPIC_API_KEY' to configure")
		return
	}
	if activeKey != "" && defaultAgent != "" {
		fmt.Printf("        → used by default agent %q\n", defaultAgent)
	}
}

// defaultAgentAuthStatus returns the active auth key and the default agent
// name by running IsolateCredentials on a copy of the default profile's
// agent config. Returns empty strings if rtCfg is nil or the default
// profile is absent.
func defaultAgentAuthStatus(rtCfg *runtime.Config, resolver *secret.Resolver) (active, agentName string) {
	if rtCfg == nil {
		return "", ""
	}
	_, ac, _, err := rtCfg.ResolveProfile("default")
	if err != nil || ac == nil {
		return "", ""
	}
	// Copy so IsolateCredentials can't mutate the cached config.
	acCopy := *ac
	acCopy.Env = nil
	for _, ir := range secret.IsolateCredentials(&acCopy, resolver) {
		if ir.ActiveKey != "" {
			return ir.ActiveKey, ac.Name
		}
	}
	return "", ac.Name
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

	// Bidi check: verify org knows about this project at the correct path
	if env.OrgDir != "" && env.SourceDir != "" {
		projectID := env.ProjectID()
		if projectID != "" {
			stateDir := filepath.Join(env.OrgDir, "projects", projectID)
			if _, err := os.Stat(stateDir); os.IsNotExist(err) {
				fmt.Printf("  Warning: project not registered in org (no %s/projects/%s)\n", filepath.Base(env.OrgDir), projectID)
				fmt.Println("  Run 'ateam project-rename' to register this project")
			}
		}
	}

	dbPath := env.ProjectDBPath()
	if fileOrSymlinkExists(dbPath) {
		fmt.Printf("  DB: %s\n", dbPath)
	} else {
		fmt.Printf("  DB: %s (NOT FOUND)\n", dbPath)
	}
	if env.ProjectDir != "" {
		logsDir := filepath.Join(env.ProjectDir, "logs")
		if !fileOrSymlinkExists(logsDir) {
			fmt.Printf("  Logs: %s (NOT FOUND)\n", logsDir)
		}
	}
	if env.GitRepoDir != "" {
		if fileOrSymlinkExists(env.GitRepoDir) {
			fmt.Printf("  Git: %s (%s)\n", env.RelPath(env.GitRepoDir), tildeHome(env.GitRepoDir))
		} else {
			fmt.Printf("  Git: %s (%s) (NOT FOUND)\n", env.RelPath(env.GitRepoDir), tildeHome(env.GitRepoDir))
		}
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

// isBrokenSymlink returns true if path is a symlink whose target doesn't exist.
func isBrokenSymlink(path string) bool {
	fi, err := os.Lstat(path)
	if err != nil {
		return false
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		return false
	}
	_, err = os.Stat(path)
	return err != nil
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
