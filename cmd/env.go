package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ateam/internal/config"
	"github.com/ateam/internal/container"
	"github.com/ateam/internal/display"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/ateam/internal/runtime"
	"github.com/ateam/internal/secret"
	"github.com/spf13/cobra"
)

var (
	envClaudeSandbox bool
	envPrintHome     bool
	envModels        bool
	envRoles         bool
)

var envCmd = &cobra.Command{
	Use:   "env",
	Short: "Show the current ATeam environment",
	Long: `Print what a run in this project will use, profile by profile: agent,
model, effort, config dir, auth, and the file that set each value. Then
the runtime.hcl layers, container detection, credentials, and the project.

  ateam env --models   # also print the per-layer model/effort chain
  ateam env --roles    # also print the role table with last report times

This command is read-only — it never creates or modifies anything.`,
	Args: cobra.NoArgs,
	RunE: runEnv,
}

func init() {
	envCmd.Flags().BoolVar(&envClaudeSandbox, "claude-sandbox", false, "print the generated Claude sandbox settings JSON")
	envCmd.Flags().BoolVar(&envPrintHome, "print-home", false, "print the absolute path to the user ateam directory")
	envCmd.Flags().BoolVar(&envModels, "models", false, "print the full per-layer model/effort chain for each profile in use")
	envCmd.Flags().BoolVar(&envRoles, "roles", false, "print the role table with the last report time per role")
}

func runEnv(cmd *cobra.Command, args []string) error {
	env, err := lookupEnv()
	if err != nil {
		return err
	}

	if envPrintHome {
		fmt.Println(env.HomeDir)
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
	data, err := r.RenderSettings(env.WorkDir)
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

	fmt.Printf("Home: %s\n", tildeHome(env.HomeDir))

	printRuntimeSection(env, cwd)

	if env.ProjectDir == "" {
		return nil
	}

	printProjectSection(env, cwd)

	return nil
}

func printRuntimeSection(env *root.ResolvedEnv, cwd string) {
	fmt.Println("\nRuntime")

	rtCfg, err := loadRuntime(env)
	if err != nil {
		fmt.Printf("  Warning: cannot load runtime.hcl: %v\n", err)
	}
	if rtCfg != nil {
		printProfilesInUse(env, rtCfg)
		printLayers(rtCfg)
		if w := pricingStaleWarning(rtCfg, time.Now()); w != "" {
			fmt.Printf("  Warning: %s\n", w)
		}
	}
	printLayerWarnings(env)
	printContainerMode()
	printAuthLines(env, rtCfg)

	if rtCfg == nil {
		return
	}
	var names []string
	for name := range rtCfg.Profiles {
		names = append(names, name)
	}
	if len(names) > 0 {
		sort.Strings(names)
		fmt.Printf("  All profiles: %s\n", strings.Join(names, ", "))
	}

	printDockerfileLine(env, cwd)
}

// printProfilesInUse answers, for every profile a flag-less run in this
// project can pick, what will run and which file decided it.
func printProfilesInUse(env *root.ResolvedEnv, rtCfg *runtime.Config) {
	resolver := secretResolver(env, secret.DefaultBackend())
	for _, choice := range relevantProfiles(env) {
		fmt.Printf("  Profile %s (%s)\n", choice.Name, choice.Source)
		prof, ac := resolveProfileAgent(rtCfg, choice.Name)
		if ac == nil {
			fmt.Printf("    Warning: not defined in runtime.hcl\n")
			continue
		}
		rep := rtCfg.ModelReport(prof, ac, runtime.CLIOverrides{}, nativeConfigFor(env, rtCfg, prof, ac))
		containerName := "none"
		if prof != nil {
			containerName = prof.Container
		}
		fmt.Printf("    agent:      %s (type %s, container %s)\n", ac.Name, rep.AgentType, containerName)
		fmt.Printf("    model:      %s\n", rep.ModelText())
		fmt.Printf("    effort:     %s\n", rep.EffortText())
		fmt.Printf("    config dir: %s\n", describeConfigDir(rtCfg, ac, env))
		fmt.Printf("    auth:       %s\n", describeAgentAuth(ac, resolver))
		for _, w := range rtCfg.ModelWarnings(prof, ac, "") {
			fmt.Printf("    Warning: %s\n", w)
		}
		if envModels {
			fmt.Println("    resolved from (lowest to highest precedence):")
			for _, line := range rep.ChainLines() {
				fmt.Printf("      %s\n", line)
			}
		}
	}
}

func describeConfigDir(rtCfg *runtime.Config, ac *runtime.AgentConfig, env *root.ResolvedEnv) string {
	dir, err := agentConfigDirPath(ac, env.StateDir())
	switch {
	case err != nil:
		return ac.ConfigDir + " (" + err.Error() + ")"
	case dir == "":
		return "(" + agentTypeName(ac) + " CLI default)"
	}
	src, _ := rtCfg.Source("agent." + ac.Name + ".config_dir")
	return fmt.Sprintf("%s (%s)", tildeHome(dir), tildeHome(src.Layer))
}

func agentTypeName(ac *runtime.AgentConfig) string {
	if ac.Type == "" {
		return "claude"
	}
	return ac.Type
}

// describeAgentAuth names the credential the agent would run with and its
// scope, by running IsolateCredentials on a copy of the agent config.
func describeAgentAuth(ac *runtime.AgentConfig, resolver *secret.Resolver) string {
	key, source := agentAuthStatus(ac, resolver)
	if key == "" {
		if len(ac.RequiredEnv) == 0 {
			return "(none required)"
		}
		return "(none found) — run 'ateam login' or 'ateam secret " + strings.Split(ac.RequiredEnv[0], "|")[0] + "'"
	}
	if source == "env" {
		return key + " (environment)"
	}
	return key + " (ateam secret, " + source + " scope)"
}

// agentAuthStatus returns the credential key and scope an agent would use.
func agentAuthStatus(ac *runtime.AgentConfig, resolver *secret.Resolver) (key, source string) {
	// Copy so IsolateCredentials can't mutate the cached config.
	acCopy := *ac
	acCopy.Env = nil
	for _, ir := range secret.IsolateCredentials(&acCopy, resolver) {
		if ir.ActiveKey != "" {
			return ir.ActiveKey, ir.ActiveSource
		}
	}
	return "", ""
}

// printLayers lists the runtime.hcl sources in precedence order, marking
// the files that do not exist. Paths use the ~ form, like the model
// lines and warnings above them.
func printLayers(rtCfg *runtime.Config) {
	parts := make([]string, 0, len(rtCfg.Layers))
	for _, l := range rtCfg.Layers {
		label := tildeHome(l.Label)
		if !l.Found {
			label += " (no file)"
		}
		parts = append(parts, label)
	}
	fmt.Printf("  Layers: %s\n", strings.Join(parts, " → "))
}

// printLayerWarnings reports layer config files that are broken symlinks,
// which Load silently treats as missing.
func printLayerWarnings(env *root.ResolvedEnv) {
	for _, dir := range env.Layers {
		p := filepath.Join(dir, root.ConfigFile)
		if isBrokenSymlink(p) {
			fmt.Printf("  Warning: %s is a broken symlink (ignored)\n", tildeHome(p))
		}
	}
}

// relevantProfiles lists the profiles a run in this project can pick
// without flags, each once: the default, the supervisor's per-action
// choices, and every role override from config.toml. In scratch mode
// (no project) only the built-in default applies.
func relevantProfiles(env *root.ResolvedEnv) []config.ProfileChoice {
	if env.Config == nil {
		return []config.ProfileChoice{{Name: "default", Source: config.ProfileSourceBuiltin}}
	}
	cfg := env.Config
	choices := []config.ProfileChoice{
		cfg.ResolveProfile("", ""),
		cfg.ResolveProfile(runner.ActionReview, ""),
		cfg.ResolveProfile(runner.ActionCode, ""),
		cfg.ResolveSupervisorProfile(runner.ActionCode),
	}
	var roles []string
	for roleID := range cfg.Profiles.Roles {
		roles = append(roles, roleID)
	}
	sort.Strings(roles)
	for _, roleID := range roles {
		choices = append(choices, cfg.ResolveProfile(runner.ActionReport, roleID))
	}
	seen := make(map[string]bool, len(choices))
	var out []config.ProfileChoice
	for _, c := range choices {
		if !seen[c.Name] {
			seen[c.Name] = true
			out = append(out, c)
		}
	}
	return out
}

// printAuthLines lists every configured credential, including the
// shadowed ones, and marks the one the default agent will use.
func printAuthLines(env *root.ResolvedEnv, rtCfg *runtime.Config) {
	resolver := secretResolver(env, secret.DefaultBackend())
	activeKey, defaultAgent := "", ""
	if rtCfg != nil {
		if _, ac, _, err := rtCfg.ResolveProfile("default"); err == nil && ac != nil {
			activeKey, _ = agentAuthStatus(ac, resolver)
			defaultAgent = ac.Name
		}
	}

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
				if defaultAgent != "" {
					annotation += fmt.Sprintf(" — used by default agent %q", defaultAgent)
				}
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
		fmt.Println("  Auth: (none) — run 'ateam login' or 'ateam secret ANTHROPIC_API_KEY' to configure")
	}
}

func printDockerfileLine(env *root.ResolvedEnv, cwd string) {
	var candidates []string
	if env.ProjectDir != "" {
		candidates = append(candidates, filepath.Join(env.ProjectDir, "Dockerfile"))
	}
	for i := len(env.ConfigLayers()) - 1; i >= 0; i-- {
		candidates = append(candidates, filepath.Join(env.ConfigLayers()[i], "Dockerfile"))
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

	// Registry check: the project should be registered under its name at this path.
	if entry, ok := root.LookupRegistry(env.HomeDir, env.ProjectName); !ok {
		fmt.Printf("  Warning: not registered in %s; run 'ateam project-rename' to register it\n", tildeHome(filepath.Join(env.HomeStateDir(), root.RegistryFile)))
	} else if entry.Path != env.SourceDir {
		fmt.Printf("  Warning: registered at %s, found at %s; run 'ateam project-rename' after a move\n", tildeHome(entry.Path), tildeHome(env.SourceDir))
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
			fmt.Printf("  Git: %s\n", tildeHome(env.GitRepoDir))
		} else {
			fmt.Printf("  Git: %s (NOT FOUND)\n", tildeHome(env.GitRepoDir))
		}
	}
	if env.WorkDir != "" {
		fmt.Printf("  Agent Work Dir: %s\n", tildeHome(env.WorkDir))
	}

	if env.Config == nil || len(env.Config.Roles) == 0 {
		return
	}
	enabled := env.Config.EnabledRoles()
	fmt.Printf("  Roles: %d enabled", len(enabled))
	if !envRoles {
		fmt.Print(" (ateam env --roles for the table)")
	}
	fmt.Println()
	if envRoles {
		printRoleTable(env, cwd)
	}
}

func printRoleTable(env *root.ResolvedEnv, cwd string) {
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
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", status, roleID, display.FmtDateAge(fi.ModTime()), relPath(cwd, reportPath))
		} else {
			fmt.Fprintf(w, "%s\t%s\t-\t\n", status, roleID)
		}
	}

	reviewPath := env.ReviewPath()
	if fi, err := os.Stat(reviewPath); err == nil {
		fmt.Fprintf(w, " \t%s\t%s\t%s\n", "review", display.FmtDateAge(fi.ModTime()), relPath(cwd, reviewPath))
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

// printContainerMode prints the agent-in-container-mode summary line
// plus a two-line matrix showing, per detection layer, whether the
// toggle is on and what (if anything) was detected.
//
// The matrix resolves the historical ambiguity of "Isolation: none but
// the probe found fence" — that state now reads as
// "Agent in container mode: false / Sandbox detection: off, detected:
// fence (set sandbox_detection=true to apply)".
func printContainerMode() {
	mode := container.IsInContainer()
	via := ""
	if cat := container.IsolationCategory(); cat != "" {
		via = " (via " + cat + ")"
	}
	fmt.Printf("  Agent in container mode: %v%s\n", mode, via)

	fmt.Println("    " + describeDetectionLayer(
		"Docker ", container.DockerDetectionEnabled(), container.DockerMarker(), "docker_detection"))

	sandboxSrc := ""
	if yes, s := container.IsInSandbox(); yes {
		sandboxSrc = s
	}
	fmt.Println("    " + describeDetectionLayer(
		"Sandbox", container.SandboxDetectionEnabled(), sandboxSrc, "sandbox_detection"))
}

// describeDetectionLayer renders one row of the detection matrix:
// fact (detected yes/no with source) first, then the rule (toggle
// state and whether it applies) in a trailing clause. Avoids the
// confusing "detection: off, detected: fence" framing where both
// halves named "detection".
func describeDetectionLayer(name string, enabled bool, source string, toggleName string) string {
	var fact string
	if source == "" {
		fact = "no"
	} else {
		fact = fmt.Sprintf("yes (%s)", source)
	}

	var rule string
	switch {
	case source != "" && enabled:
		rule = fmt.Sprintf("%s=true, applied", toggleName)
	case source != "" && !enabled:
		// No "set to true" hint here: the user may have explicitly
		// disabled the toggle. The fact + the rule are enough; the
		// reader can decide whether to flip it.
		rule = fmt.Sprintf("%s=false, NOT applied", toggleName)
	default:
		// Nothing detected; just show the toggle for audit.
		rule = fmt.Sprintf("%s=%v", toggleName, enabled)
	}
	return fmt.Sprintf("%s detected: %s — %s", name, fact, rule)
}
