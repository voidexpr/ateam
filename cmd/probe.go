package cmd

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	goruntime "runtime"

	"github.com/ateam/internal/container"
	"github.com/ateam/internal/runtime"
	"github.com/ateam/internal/sandboxpolicy"
	"github.com/ateam/internal/sandboxprobe"
	"github.com/spf13/cobra"
)

// secretsFilter is the built-in filter whose paths nothing should read.
const secretsFilter = "agent/secrets"

type probeOptions struct {
	launch       launchFlags
	startSandbox bool
	sel          sandboxprobe.Selection
	list         bool
	all          bool
	quiet        bool
	json         bool
	programs     []string
	connects     []string
	urls         []string
	listens      []string
	runChecks    string
}

var probeOpts probeOptions

var probeCmd = &cobra.Command{
	Use:   "probe [flags] [PATH]...",
	Short: "Report what this environment can read, write, reach and do",
	Long: `Check what the environment ateam runs in can read, write, replace, reach
and do: a plain terminal, a sandbox, a container, a remote host. Each row is
a file, a network destination, a capability or an environment variable, with
what was found and a verdict: ok, RISK (more open than it should be) or
UNMET (more closed than a profile asks).

Nothing is started unless asked for, and the first line of the output says
what was:
  --start-sandbox   starts the profile's sandbox and runs the checks inside
  --with ID         runs an active check (a browser, a docker build)

With --profile and without --start-sandbox, the current environment is
compared with what the profile asks for.

The network checks leave the machine (example.com, 1.1.1.1, a DNS lookup):
--skip network turns them off. --only and --skip take groups and check ids;
--list shows them all.

Exit code: 0 when every row is ok, 1 with a RISK or an UNMET, 2 for a usage
or launch error.

Example:
  ateam probe
  ateam probe ~/.ssh ./secrets.env --only paths
  ateam probe --skip network,programs
  ateam probe --profile dev/go
  ateam probe --start-sandbox --profile sandbox/nono,dev/go
  ateam probe --with playwright-launch,docker-build
  ateam probe --json > here.json`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		err := runProbe(&probeOpts, args)
		var exit *ExitError
		if err == nil || errors.As(err, &exit) {
			return err
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return &ExitError{Code: 2}
	},
}

func init() {
	o := &probeOpts
	flags := probeCmd.Flags()
	addLaunchFlags(probeCmd, &o.launch)
	flags.BoolVar(&o.startSandbox, "start-sandbox", false, "start the sandbox the profile describes and run the checks inside it")
	flags.StringSliceVar(&o.sel.Only, "only", nil, "run only these groups or check ids (repeatable, comma-separated)")
	flags.StringSliceVar(&o.sel.Skip, "skip", nil, "leave out these groups or check ids (repeatable, comma-separated)")
	flags.StringSliceVar(&o.sel.With, "with", nil, "also run these active checks, which start something (repeatable, comma-separated)")
	flags.BoolVar(&o.list, "list", false, "print every group and check, and what the active ones start; check nothing")
	flags.StringArrayVar(&o.programs, "program", nil, "extra program to check (repeatable)")
	flags.StringArrayVar(&o.connects, "connect", nil, "extra HOST[:PORT] to connect to (repeatable)")
	flags.StringArrayVar(&o.urls, "url", nil, "extra URL to reach, through the proxy and directly (repeatable)")
	flags.StringArrayVar(&o.listens, "listen-check", nil, "extra [ADDR:]PORT to try to listen on (repeatable)")
	flags.BoolVar(&o.all, "all", false, "also list the ok rows with nothing there, and the folded ones")
	flags.BoolVar(&o.quiet, "quiet", false, "only the rows that are not ok")
	flags.BoolVar(&o.json, "json", false, "print one JSON document instead of the table")
	flags.BoolVar(&o.launch.dryRun, "dry-run", false, "print the checks, what each is compared with and the sandbox command line; check nothing")
	flags.StringVar(&o.runChecks, "run-checks", "", "run an encoded check list (used inside a sandbox ateam started)")
	_ = flags.MarkHidden("run-checks")
	rootCmd.AddCommand(probeCmd)
}

// probePayload is what the ateam that starts a sandbox hands to the one
// running inside it.
type probePayload struct {
	Started []string             `json:"started"`
	Checks  []sandboxprobe.Check `json:"checks"`
	Skipped int                  `json:"skipped"`
	All     bool                 `json:"all,omitempty"`
	Quiet   bool                 `json:"quiet,omitempty"`
	JSON    bool                 `json:"json,omitempty"`
}

// encode packs the payload into one argument: a file may be unreadable
// inside the sandbox, and stdin is the terminal. Compressed, because one
// argument is limited to 128 KiB on Linux.
func (p probePayload) encode() (string, error) {
	var buf bytes.Buffer
	zip := gzip.NewWriter(&buf)
	if err := json.NewEncoder(zip).Encode(p); err != nil {
		return "", err
	}
	if err := zip.Close(); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf.Bytes()), nil
}

func decodeProbePayload(encoded string) (probePayload, error) {
	var p probePayload
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return p, fmt.Errorf("--run-checks: %w", err)
	}
	zip, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return p, fmt.Errorf("--run-checks: %w", err)
	}
	if err := json.NewDecoder(zip).Decode(&p); err != nil {
		return p, fmt.Errorf("--run-checks: %w", err)
	}
	return p, nil
}

func (o *probeOptions) hasPolicyFlags() bool {
	f := &o.launch
	return len(f.profiles)+len(f.read)+len(f.write)+len(f.exclude)+len(f.domains)+len(f.denied)+len(f.listen) > 0
}

func runProbe(o *probeOptions, args []string) error {
	if o.runChecks != "" {
		payload, err := decodeProbePayload(o.runChecks)
		if err != nil {
			return err
		}
		env, err := sandboxprobe.CurrentEnv("", nil)
		if err != nil {
			return err
		}
		return probeReport(os.Stdout, env, payload)
	}
	env, err := sandboxprobe.CurrentEnv(homeDirFlag(), builtinSecrets())
	if err != nil {
		return err
	}
	var plan *launchPlan
	var policy *sandboxpolicy.Policy
	switch {
	case o.startSandbox:
		if plan, err = probeLaunchPlan(o); err != nil {
			return err
		}
		policy = plan.policy
	case o.hasPolicyFlags():
		if policy, err = probePolicy(&o.launch); err != nil {
			return err
		}
	}
	checks, err := probeChecks(env, policy, o, args, plan == nil)
	if err != nil {
		return err
	}
	selected, skipped, err := sandboxprobe.Select(checks, o.sel)
	if err != nil {
		return err
	}
	if o.list {
		sandboxprobe.List(os.Stdout, checks, selected, false)
		return nil
	}
	payload := probePayload{Checks: selected, Skipped: skipped, All: o.all, Quiet: o.quiet, JSON: o.json}
	if plan != nil {
		payload.Started = append(payload.Started, fmt.Sprintf("%s sandbox, profile %s", plan.container.Type, plan.prof.Name))
	}
	for _, c := range selected {
		if c.Active {
			payload.Started = append(payload.Started, c.ID+": "+c.Starts)
		}
	}
	if o.launch.dryRun {
		return probeDryRun(os.Stdout, plan, checks, payload)
	}
	if plan == nil {
		return probeReport(os.Stdout, env, payload)
	}
	return probeInSandbox(plan, payload)
}

// builtinSecrets returns the paths of the embedded agent/secrets filter.
// Only the binary is read: the command must work where ~/.ateam is closed.
func builtinSecrets() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	cfg, err := runtime.LoadWithFiles(nil, nil)
	if err != nil {
		return nil
	}
	include, exclude, err := sandboxpolicy.FilterSet(cfg.Filters).Paths(secretsFilter, home)
	if err != nil {
		return nil
	}
	return append(include, exclude...)
}

// readableLayers drops the config layers that cannot be read, saying
// which: inside a foreign sandbox the comparison continues with the rest.
func readableLayers(layers []string) []string {
	var kept []string
	for _, dir := range layers {
		f, err := os.Open(filepath.Join(dir, runtime.ConfigFile))
		if err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "ateam probe: config layer %s cannot be read here, continuing without it: %v\n", dir, err)
			continue
		}
		if f != nil {
			_ = f.Close()
		}
		kept = append(kept, dir)
	}
	return kept
}

// probePolicy builds the policy of the launch flags without planning a
// launch: the comparison needs the rules, whatever the container.
func probePolicy(f *launchFlags) (*sandboxpolicy.Policy, error) {
	grantDir, projectDir, layers, err := launchPlaces()
	if err != nil {
		return nil, err
	}
	cfg, composed, files, err := composeLaunch(f, readableLayers(layers))
	if err != nil {
		return nil, err
	}
	prof, _, _, err := cfg.ResolveComposed(composed)
	if err != nil {
		return nil, err
	}
	return buildSandboxPolicy(cfg, prof, grantDir, projectDir, homeDirFlag(), files)
}

// probeLaunchPlan plans the sandbox --start-sandbox asks for. The plan's
// command is set later, once the check list is known.
func probeLaunchPlan(o *probeOptions) (*launchPlan, error) {
	if !o.hasPolicyFlags() {
		return nil, fmt.Errorf("--start-sandbox needs a sandbox to start: add --profile sandbox/nono or sandbox/fence")
	}
	plan, err := planLaunch(&o.launch, modeRun, []string{"probe"})
	if err != nil {
		return nil, err
	}
	if plan.policy == nil {
		if source := container.IsolationSource(); source != "" {
			return nil, fmt.Errorf("--start-sandbox: already inside a sandbox or a container (%s), where one cannot be started; without the flag the profile is compared with this environment", source)
		}
		return nil, fmt.Errorf("--start-sandbox: container %q starts no sandbox; add --profile sandbox/nono or sandbox/fence", plan.container.Name)
	}
	// Detection of an outer sandbox is off by default (it has false
	// positives), so a launch is still tried: say why it may fail.
	if in, source := container.IsInSandbox(); in {
		fmt.Fprintf(os.Stderr, "ateam probe: this looks like a sandbox already (%s): a sandbox does not start inside another, the launch may fail\n", source)
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if plan.container.Type == runtime.ContainerTypeFence && goruntime.GOOS == "linux" && sandboxpolicy.Within(sandboxpolicy.RealPath(exe), "/tmp") {
		return nil, fmt.Errorf("--start-sandbox: the ateam binary %s is under /tmp, which is private inside fence on Linux: the checks could not start there; run ateam from another folder", exe)
	}
	return plan, nil
}

// probeChecks builds the whole check list: built-in, from the policy,
// discovered, and from the command line. inPlace is false when the checks
// will run inside a sandbox: the mounts there are the sandbox's own.
func probeChecks(env sandboxprobe.Env, policy *sandboxpolicy.Policy, o *probeOptions, paths []string, inPlace bool) ([]sandboxprobe.Check, error) {
	_, projectDir, _, err := launchPlaces()
	if err != nil {
		return nil, err
	}
	env.ProjectDir = projectDir
	lists := [][]sandboxprobe.Check{sandboxprobe.Defaults(env, o.programs)}
	if policy != nil {
		lists = append(lists, sandboxprobe.FromPolicy(env, policy))
	}
	var known []string
	for _, c := range lists[0] {
		if c.Group == "secrets" {
			known = append(known, c.Target)
		}
	}
	lists = append(lists, sandboxprobe.Discover(env, known))
	if inPlace && goruntime.GOOS == "linux" {
		if data, err := os.ReadFile("/proc/self/mountinfo"); err == nil {
			lists = append(lists, sandboxprobe.MountChecks(env, string(data)))
		}
	}
	user, err := sandboxprobe.UserChecks(env, paths, o.connects, o.urls, o.listens)
	if err != nil {
		return nil, err
	}
	checks := sandboxprobe.Merge(append(lists, user)...)
	scripts, err := sandboxprobe.Scripts()
	if err != nil {
		return nil, err
	}
	for _, script := range scripts {
		checks = append(checks, script.Check())
	}
	if policy != nil {
		checks = sandboxprobe.Cover(checks, policy)
	}
	return checks, nil
}

func probeEnvironment() sandboxprobe.Environment {
	isolation := container.IsolationSource()
	if isolation == "" {
		_, isolation = container.IsInSandbox()
	}
	name := fmt.Sprintf("uid %d", os.Getuid())
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	return sandboxprobe.Environment{OS: goruntime.GOOS, Isolation: isolation, User: name,
		Proxy: sandboxprobe.ProxyVarsSet(), TmpDir: os.Getenv("TMPDIR")}
}

// probeReport runs the checks here, prints the report and returns exit
// code 1 when a row needs a look.
func probeReport(w io.Writer, env sandboxprobe.Env, payload probePayload) error {
	environment := probeEnvironment()
	if !payload.JSON {
		sandboxprobe.Header(w, payload.Started, environment)
	}
	rep := sandboxprobe.Report{Started: payload.Started, Environment: environment,
		Results: sandboxprobe.Run(env, payload.Checks), Skipped: payload.Skipped}
	if payload.JSON {
		if err := sandboxprobe.JSON(w, rep); err != nil {
			return err
		}
	} else {
		color := isTerminal() && os.Getenv("NO_COLOR") == ""
		sandboxprobe.Table(w, rep, sandboxprobe.TableOptions{All: payload.All, Quiet: payload.Quiet, Color: color})
	}
	if n := sandboxprobe.Count(rep.Results); n.Risk+n.Unmet > 0 {
		return &ExitError{Code: 1}
	}
	return nil
}

func probeDryRun(w io.Writer, plan *launchPlan, all []sandboxprobe.Check, payload probePayload) error {
	for _, line := range sandboxprobe.StartedLines(payload.Started) {
		fmt.Fprintln(w, "would be: "+line)
	}
	fmt.Fprintln(w)
	sandboxprobe.List(w, all, payload.Checks, true)
	if plan == nil {
		return nil
	}
	argv, err := probeCommand(plan, payload)
	if err != nil {
		return err
	}
	fmt.Fprintln(w)
	plan.describe(w)
	fmt.Fprintln(w)
	// The encoded check list is long and says nothing to a reader.
	argv[len(argv)-1] = "<checks>"
	fmt.Fprintln(w, shellJoin(argv))
	return nil
}

// probeCommand returns the command line that runs the payload's checks
// inside the plan's sandbox.
func probeCommand(plan *launchPlan, payload probePayload) ([]string, error) {
	for i, c := range payload.Checks {
		if c.Method == sandboxprobe.MethodFile {
			payload.Checks[i].Before = sandboxprobe.Observe(c.Target)
		}
	}
	encoded, err := payload.encode()
	if err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	plan.argv = []string{exe, "probe", "--run-checks", encoded}
	// Every refused check would be listed again by the backend as a denial
	// to fix.
	plan.silent = true
	profileFile, _ := launchFiles(plan.ateamHome)
	return plan.wrapped(context.Background(), modeRun, profileFile)
}

func probeInSandbox(plan *launchPlan, payload probePayload) error {
	argv, err := probeCommand(plan, payload)
	if err != nil {
		return err
	}
	_, gitBackup := launchFiles(plan.ateamHome)
	if code := runGuarded(argv, plan.policy.WorkDir, gitBackup); code != 0 {
		return &ExitError{Code: code}
	}
	return nil
}
