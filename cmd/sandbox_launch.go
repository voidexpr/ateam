package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/runtime"
	"github.com/ateam/internal/sandboxpolicy"
	"github.com/spf13/cobra"
)

// launchFlags are the profile options shared by `ateam chat`, `run`,
// `shell` and the `ateam profile` subcommands.
type launchFlags struct {
	profiles []string
	read     []string
	write    []string
	exclude  []string
	domains  []string
	command  string
	dryRun   bool
}

func addLaunchFlags(cmd *cobra.Command, f *launchFlags) {
	cmd.Flags().StringSliceVar(&f.profiles, "profile", nil, "profile name or .hcl file to compose, in order (repeatable, comma-separated)")
	cmd.Flags().StringArrayVar(&f.read, "read", nil, "path readable for this launch (repeatable)")
	cmd.Flags().StringArrayVar(&f.write, "write", nil, "path writable for this launch (repeatable)")
	cmd.Flags().StringArrayVar(&f.exclude, "exclude", nil, "path unreadable for this launch (repeatable)")
	cmd.Flags().StringArrayVar(&f.domains, "domain", nil, "host reachable for this launch (repeatable)")
}

// launchMode is what a launch runs.
type launchMode int

const (
	modeRun   launchMode = iota // the profile's command, or the one given
	modeShell                   // the user's shell
	modeChat                    // the profile's agent, attended
)

// launchPlan is a resolved launch: what runs, where, under which rules.
type launchPlan struct {
	prof      *runtime.ProfileConfig
	agentCfg  *runtime.AgentConfig
	container *runtime.ContainerConfig
	argv      []string // the command, before any sandbox wrapper
	grantDir  string   // the folder the sandbox makes writable
	ateamHome string
	policy    *sandboxpolicy.Policy // nil without a sandbox backend
	files     []string              // profile files named by path
}

// launchPlaces returns the folder a launch may write (the project root
// when the current folder is inside a project, else the current folder),
// the project's .ateam directory ("" without one) and the config layers.
func launchPlaces() (grantDir, projectDir string, layers []string, err error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", nil, err
	}
	if resolved, rerr := filepath.EvalSymlinks(cwd); rerr == nil {
		cwd = resolved
	}
	env, _ := lookupEnv()
	if env == nil {
		return cwd, "", envLayers(nil), nil
	}
	grantDir = cwd
	if env.SourceDir != "" {
		if root, rerr := filepath.EvalSymlinks(env.SourceDir); rerr == nil && sandboxpolicy.Within(cwd, root) {
			grantDir = root
		}
	}
	return grantDir, env.ProjectDir, env.Layers, nil
}

// splitProfileArgs separates profile names from profile files (an entry
// with a path separator or ending in .hcl), made absolute.
func splitProfileArgs(args []string) (order []string, files []string, err error) {
	for _, arg := range args {
		if strings.ContainsRune(arg, filepath.Separator) && fileExists(arg) || strings.HasSuffix(arg, ".hcl") {
			abs, err := filepath.Abs(arg)
			if err != nil {
				return nil, nil, err
			}
			files = append(files, abs)
			order = append(order, abs)
			continue
		}
		order = append(order, arg)
	}
	return order, files, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// composeLaunch loads the configuration with the launch's profile files
// and composes its profiles and flags into one.
func composeLaunch(f *launchFlags, layers []string) (*runtime.Config, runtime.ProfileConfig, []string, error) {
	order, files, err := splitProfileArgs(f.profiles)
	if err != nil {
		return nil, runtime.ProfileConfig{}, nil, err
	}
	cfg, err := runtime.LoadWithFiles(layers, files)
	if err != nil {
		return nil, runtime.ProfileConfig{}, nil, err
	}
	var names []string
	for _, entry := range order {
		if !slices.Contains(files, entry) {
			names = append(names, entry)
			continue
		}
		defined := cfg.ProfilesDefinedIn(entry)
		if len(defined) == 0 {
			return nil, runtime.ProfileConfig{}, nil, fmt.Errorf("%s defines no profile", entry)
		}
		names = append(names, defined...)
	}
	extra := runtime.ProfileConfig{Read: f.read, Write: f.write, Exclude: f.exclude, Domains: f.domains}
	if f.command != "" {
		extra.Command = strings.Fields(f.command)
	}
	composed, err := cfg.ComposeProfiles(names, extra)
	return cfg, composed, files, err
}

// planLaunch resolves a launch and checks that its rules can be enforced.
func planLaunch(f *launchFlags, mode launchMode, args []string) (*launchPlan, error) {
	grantDir, projectDir, layers, err := launchPlaces()
	if err != nil {
		return nil, err
	}
	cfg, composed, files, err := composeLaunch(f, layers)
	if err != nil {
		return nil, err
	}
	prof, ac, cc, err := cfg.ResolveComposed(composed)
	if err != nil {
		return nil, err
	}
	// Under a sandbox backend, an attended agent also needs its own files
	// and API hosts: the agent/<type> profile.
	if mode == modeChat && cc.Type == runtime.ContainerTypeNono {
		if prof, ac, cc, err = cfg.ResolveComposed(cfg.WithAgentProfile(composed, ac.TypeName())); err != nil {
			return nil, err
		}
	}
	plan := &launchPlan{prof: prof, agentCfg: ac, container: cc, grantDir: grantDir, ateamHome: homeDirFlag(), files: files}
	if plan.argv, err = launchCommand(mode, prof, ac, args); err != nil {
		return nil, err
	}
	switch cc.Type {
	case runtime.ContainerTypeNono:
		if plan.policy, err = buildSandboxPolicy(prof, grantDir, projectDir, plan.ateamHome, files); err != nil {
			return nil, err
		}
	case "", "none":
		if prof.HasPolicy() {
			return nil, fmt.Errorf("profile %q sets read, write, exclude or domains, but its container %q enforces none of them; add a sandbox backend (--profile sandbox/nono)", prof.Name, cc.Name)
		}
	default:
		return nil, fmt.Errorf("container type %q (container %q) is not supported by chat, run and shell yet; use a nono or none container", cc.Type, cc.Name)
	}
	return plan, nil
}

// launchCommand returns what a mode runs.
func launchCommand(mode launchMode, prof *runtime.ProfileConfig, ac *runtime.AgentConfig, args []string) ([]string, error) {
	switch mode {
	case modeShell:
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/sh"
		}
		return append([]string{shell}, args...), nil
	case modeChat:
		command := ac.Command
		if command == "" {
			command = agent.DefaultCommand(ac.TypeName())
		}
		if command == "" {
			return nil, fmt.Errorf("agent %q (type %q) has no CLI to chat with", ac.Name, ac.TypeName())
		}
		return append([]string{command}, args...), nil
	}
	if len(prof.Command) > 0 {
		return append(slices.Clone(prof.Command), args...), nil
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("nothing to run: profile %q saves no command; give one after --", prof.Name)
	}
	return args, nil
}

// buildSandboxPolicy turns a composed profile into the policy of one
// launch, attended or not: the same rules either way.
func buildSandboxPolicy(prof *runtime.ProfileConfig, grantDir, projectDir, ateamHome string, files []string) (*sandboxpolicy.Policy, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return sandboxpolicy.Build(sandboxpolicy.Input{
		Name: prof.Name, Home: home, WorkDir: grantDir,
		Read: prof.Read, Write: prof.Write, Exclude: prof.Exclude, Domains: prof.Domains,
		Origin:    prof.EntryOrigin,
		AteamHome: ateamHome, ProjectDir: projectDir, ProfileFiles: files,
	})
}

// sandboxStateDir holds the backend profiles of launches.
func sandboxStateDir(ateamHome string) string {
	return filepath.Join(ateamHome, runtime.StateSubdir, "sandbox")
}

const sandboxProfileKeep = 7 * 24 * time.Hour

func pruneSandboxProfiles(dir string) {
	entries, _ := filepath.Glob(filepath.Join(dir, "profile.*.json"))
	for _, file := range entries {
		if info, err := os.Stat(file); err == nil && time.Since(info.ModTime()) > sandboxProfileKeep {
			_ = os.Remove(file)
		}
	}
}

// writeNonoProfile renders a policy with what the installed nono applies
// on its own, writes it to profileFile and returns the nono binary.
func writeNonoProfile(ctx context.Context, policy *sandboxpolicy.Policy, containerName, profileFile string) (string, error) {
	nono, err := exec.LookPath("nono")
	if err != nil {
		return "", fmt.Errorf("container %q needs nono, which is not in PATH (https://nono.sh): %w", containerName, err)
	}
	info, err := sandboxpolicy.QueryNono(ctx, nono)
	if err != nil {
		return "", err
	}
	prof, err := sandboxpolicy.RenderNono(policy, info, goruntime.GOOS)
	if err != nil {
		return "", err
	}
	return nono, sandboxpolicy.WriteNonoProfile(ctx, nono, prof, profileFile)
}

// claudeSandboxOff turns Claude's own sandbox off for one launch: under an
// outside sandbox it cannot nest.
const claudeSandboxOff = `{"sandbox":{"enabled":false}}`

// wrapped returns the full command line of the plan: under a sandbox
// backend, the backend's command around argv, with its profile written to
// profileFile.
func (p *launchPlan) wrapped(ctx context.Context, mode launchMode, profileFile string) ([]string, error) {
	argv := slices.Clone(p.argv)
	binary, err := exec.LookPath(argv[0])
	if err != nil {
		return nil, fmt.Errorf("%s not found in PATH: %w", argv[0], err)
	}
	argv[0] = binary
	if p.policy == nil {
		return argv, nil
	}
	if mode == modeChat && p.agentCfg.TypeName() == agent.NameClaude && !slices.Contains(argv, "--settings") {
		argv = append([]string{argv[0], "--settings", claudeSandboxOff}, argv[1:]...)
	}
	nono, err := writeNonoProfile(ctx, p.policy, p.container.Name, profileFile)
	if err != nil {
		return nil, err
	}
	return sandboxpolicy.NonoCommand(nono, profileFile, p.grantDir, argv), nil
}

// describe prints the plan: the profile, its rules and what they leave open.
func (p *launchPlan) describe(w io.Writer) {
	fmt.Fprintf(w, "profile    %s\n", p.prof.Name)
	fmt.Fprintf(w, "container  %s (%s)\n", p.container.Name, p.container.Type)
	if p.policy == nil {
		fmt.Fprintln(w, "sandbox    none: the command runs with your own access")
		return
	}
	fmt.Fprintf(w, "writable   %s (working folder)\n", p.policy.WorkDir)
	printRules(w, "writable  ", p.policy.Write)
	printRules(w, "readable  ", p.policy.Read)
	printRules(w, "excluded  ", p.policy.Exclude)
	if len(p.policy.Domains) == 0 {
		fmt.Fprintln(w, "network    closed")
	} else {
		fmt.Fprintf(w, "network    %s, localhost\n", strings.Join(p.policy.Domains, ", "))
	}
	if _, ok := sandboxpolicy.SnapshotGit(p.policy.WorkDir); ok {
		fmt.Fprintln(w, "checked    .git/config and .git/hooks, compared after the run (they stay writable)")
	}
}

func printRules(w io.Writer, label string, rules []sandboxpolicy.Rule) {
	for _, r := range rules {
		from := r.Source
		if r.Reason != "" {
			from += ": " + r.Reason
		}
		fmt.Fprintf(w, "%s %s (%s)\n", label, r.Path, from)
	}
}

// shellSafe reports whether r needs no quoting in a shell word.
func shellSafe(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./=:,@+", r)
}

func shellJoin(argv []string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = a
		if a == "" || strings.IndexFunc(a, func(r rune) bool { return !shellSafe(r) }) >= 0 {
			quoted[i] = shellQuoteSingle(a)
		}
	}
	return strings.Join(quoted, " ")
}

// runLaunch plans a launch and runs it, or with --dry-run prints what it
// would run. Without a sandbox the command replaces this process; under
// one, ateam waits for it to compare the repository's hooks afterwards.
func runLaunch(f *launchFlags, mode launchMode, args []string) error {
	plan, err := planLaunch(f, mode, args)
	if err != nil {
		return err
	}
	dir := sandboxStateDir(plan.ateamHome)
	pruneSandboxProfiles(dir)
	profileFile := filepath.Join(dir, fmt.Sprintf("profile.%d.json", os.Getpid()))
	argv, err := plan.wrapped(context.Background(), mode, profileFile)
	if err != nil {
		return err
	}
	if f.dryRun {
		plan.describe(os.Stdout)
		fmt.Println()
		fmt.Println(shellJoin(argv))
		return nil
	}
	if plan.policy == nil {
		return syscall.Exec(argv[0], argv, os.Environ())
	}
	code := runGuarded(argv, plan.policy.WorkDir, filepath.Join(dir, fmt.Sprintf("git.%d", os.Getpid())))
	if code != 0 {
		os.Exit(code)
	}
	return nil
}

// runGuarded runs argv attached to this terminal and returns its exit
// code. No sandbox can keep .git/config and .git/hooks read-only inside a
// writable working folder, and what a command plants there runs outside
// the sandbox later: they are compared after the run, and a change is
// reported with the copy to restore from.
func runGuarded(argv []string, workDir, backupDir string) int {
	guard, guarded := sandboxpolicy.SnapshotGit(workDir)
	if guarded {
		if err := guard.Backup(backupDir); err != nil {
			fmt.Fprintf(os.Stderr, "ateam: cannot back up .git/config and .git/hooks: %v\n", err)
			return 1
		}
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	// The command shares the terminal: an interrupt reaches it directly.
	// Signals sent to ateam alone are passed on.
	signal.Ignore(syscall.SIGINT, syscall.SIGQUIT)
	forwarded := make(chan os.Signal, 1)
	signal.Notify(forwarded, syscall.SIGTERM, syscall.SIGHUP)
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "ateam: %v\n", err)
		return 127
	}
	go func() {
		for sig := range forwarded {
			_ = cmd.Process.Signal(sig)
		}
	}()
	code := 0
	if err := cmd.Wait(); err != nil {
		code = 1
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		}
	}
	signal.Stop(forwarded)
	if !guarded {
		return code
	}
	changed := guard.Changed()
	if len(changed) == 0 {
		_ = os.RemoveAll(backupDir)
		return code
	}
	fmt.Fprintf(os.Stderr, "\nateam: the command changed files git runs or reads outside the sandbox:\n")
	for _, name := range changed {
		fmt.Fprintf(os.Stderr, "  %s\n", name)
	}
	fmt.Fprintf(os.Stderr, "Review them before using git here. The versions from before the run are in:\n  %s\n", backupDir)
	if code == 0 {
		code = 3
	}
	return code
}
