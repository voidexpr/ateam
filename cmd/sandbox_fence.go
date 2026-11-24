package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	goruntime "runtime"
	"slices"
	"strings"

	"github.com/ateam/internal/container"
	"github.com/ateam/internal/sandboxpolicy"
	"github.com/fencesandbox/fence/pkg/fence"
	"github.com/spf13/cobra"
)

var (
	fenceExecSettings string
	fenceExecWorkDir  string
)

// fenceExecCmd is what a fence container runs: ateam itself, holding the
// sandbox library. The domain proxies live in this process for as long as
// the command runs, so each run has its own and none is shared.
var fenceExecCmd = &cobra.Command{
	Use:    sandboxpolicy.FenceExecCommand + " --settings FILE --workdir DIR -- COMMAND [ARG]...",
	Short:  "Run a command under a fence configuration, with the embedded sandbox",
	Hidden: true,
	Args:   cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		code, err := runFenceExec(fenceExecSettings, fenceExecWorkDir, args)
		if err != nil {
			return err
		}
		if code != 0 {
			return &ExitError{Code: code}
		}
		return nil
	},
}

func init() {
	fenceExecCmd.Flags().StringVar(&fenceExecSettings, "settings", "", "fence configuration file")
	fenceExecCmd.Flags().StringVar(&fenceExecWorkDir, "workdir", "", "the command's working folder")
	rootCmd.AddCommand(fenceExecCmd)
}

// runFenceExec runs argv under the fence configuration in settings and
// returns its exit code.
func runFenceExec(settings, workDir string, argv []string) (int, error) {
	if settings == "" || workDir == "" {
		return 0, fmt.Errorf("--settings and --workdir are required")
	}
	if container.IsInContainer() {
		return 0, fmt.Errorf("already inside a sandbox (%s): a sandbox cannot start inside another", container.IsolationSource())
	}
	if err := fencePreflight(); err != nil {
		return 0, err
	}
	cfg, err := loadFenceConfig(settings)
	if err != nil {
		return 0, err
	}
	manager := fence.NewManager(cfg, false, false)
	defer manager.Cleanup()
	if goruntime.GOOS == "linux" {
		// On Linux fence starts parts of itself inside the sandbox: ateam
		// is that helper too (fence.DispatchInternalHelper in main).
		self, err := os.Executable()
		if err != nil {
			return 0, err
		}
		if err := manager.SetLinuxHelperPath(self); err != nil {
			return 0, fmt.Errorf("fence: %w", err)
		}
	}
	if err := manager.Initialize(); err != nil {
		return 0, fmt.Errorf("fence: %w", err)
	}
	wrapped, err := manager.WrapCommandInDir(shellJoin(argv), workDir)
	if err != nil {
		return 0, fmt.Errorf("fence: %w", err)
	}
	cmd := exec.Command("/bin/sh", "-c", wrapped)
	cmd.Dir = workDir
	cmd.Env = append(fenceEnv(os.Environ()), sandboxpolicy.InSandboxEnv+"=1")
	return runAttached(cmd), nil
}

// fenceEnv drops the variables that load a library into every process:
// the fence command does the same around what it runs.
func fenceEnv(env []string) []string {
	return slices.DeleteFunc(slices.Clone(env), func(entry string) bool {
		return strings.HasPrefix(entry, "LD_") || strings.HasPrefix(entry, "DYLD_")
	})
}

// fencePreflight fails, before anything is written or started, when this
// host lacks what the embedded fence needs.
func fencePreflight() error {
	if err := fenceRequirements(goruntime.GOOS, exec.LookPath); err != nil {
		return err
	}
	if goruntime.GOOS != "linux" {
		return nil
	}
	// The probe fence itself makes. When it fails fence goes on without a
	// private network, with the domain rules carried by proxy variables
	// alone: a weakened rule, which ateam refuses.
	truePath, err := exec.LookPath("true")
	if err != nil {
		return err
	}
	if out, err := exec.Command("bwrap", "--unshare-net", "--ro-bind", "/", "/", "--", truePath).CombinedOutput(); err != nil {
		return fmt.Errorf("fence cannot isolate the network here: bwrap cannot create a network namespace (%s). Usual causes: a Docker container without --cap-add=NET_ADMIN, a CI runner, or AppArmor restricting user namespaces (Ubuntu 24.04 and later)", strings.TrimSpace(string(out)))
	}
	return nil
}

// fenceRequirements names the programs fence runs on goos that are not
// installed.
func fenceRequirements(goos string, lookPath func(string) (string, error)) error {
	var need []string
	hint := ""
	switch goos {
	case "darwin":
		need = []string{"sandbox-exec"}
	case "linux":
		need, hint = []string{"bwrap", "socat"}, " (install the packages bubblewrap and socat)"
	default:
		return fmt.Errorf("the embedded fence sandbox does not run on %s", goos)
	}
	var missing []string
	for _, name := range need {
		if _, err := lookPath(name); err != nil {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("the fence sandbox needs %s on %s: not found in PATH: %s%s", strings.Join(need, " and "), goos, strings.Join(missing, ", "), hint)
	}
	return nil
}

func loadFenceConfig(file string) (*fence.Config, error) {
	cfg, err := fence.LoadConfig(file)
	if err != nil {
		return nil, fmt.Errorf("fence configuration %s: %w", file, err)
	}
	if cfg == nil {
		return nil, fmt.Errorf("fence configuration %s: not found or empty", file)
	}
	return cfg, nil
}

// writeFenceConfig renders a policy for the embedded fence, writes it to
// configFile and has fence itself confirm every rule of the policy on the
// file it will load. It returns the ateam binary that runs it. What fence
// does not enforce on this system is said on stderr at every launch, or
// refused when the container is strict.
func writeFenceConfig(policy *sandboxpolicy.Policy, configFile string, strict bool) (string, error) {
	rendered, err := sandboxpolicy.RenderFence(policy, goruntime.GOOS)
	if err != nil {
		return "", err
	}
	if err := fencePreflight(); err != nil {
		return "", err
	}
	err = sandboxpolicy.WriteFenceConfig(rendered, configFile, func(tmp string) error {
		cfg, err := loadFenceConfig(tmp)
		if err != nil {
			return err
		}
		return checkFenceConfig(cfg, policy)
	})
	if err != nil {
		return "", err
	}
	if err := reportFenceGaps(os.Stderr, sandboxpolicy.FenceGaps(policy, goruntime.GOOS), strict); err != nil {
		_ = os.Remove(configFile)
		return "", err
	}
	return os.Executable()
}

// reportFenceGaps says which rules fence does not enforce: a warning on w,
// or an error when strict.
func reportFenceGaps(w io.Writer, gaps []sandboxpolicy.Rule, strict bool) error {
	if len(gaps) == 0 {
		return nil
	}
	paths := make([]string, len(gaps))
	for i, r := range gaps {
		paths[i] = r.Path
	}
	msg := fmt.Sprintf("NOT ENFORCED by fence on Linux: these %d paths do not exist yet and the command can create them:\n  %s", len(paths), strings.Join(paths, "\n  "))
	if strict {
		return fmt.Errorf("%s\nthe container is strict: create the paths first, or remove `strict`", msg)
	}
	fmt.Fprintf(w, "ateam: %s\n", msg)
	return nil
}

// checkFenceConfig asks fence, rule by rule, whether cfg does what the
// policy says. fence has paths it never lets be written (.gitconfig, shell
// startup files, .vscode, ...): a grant of one is an error that names the
// rule, not a grant that does nothing.
//
// fence compares the path it is asked about as written, and its own rules
// by where they really are, as the kernel does: a rule is asked about by
// its real path, or an excluded link would look open.
func checkFenceConfig(cfg *fence.Config, policy *sandboxpolicy.Policy) error {
	expect := func(rules []sandboxpolicy.Rule, verb string, check func(*fence.Config, string, string) error, allowed bool) error {
		for _, r := range rules {
			err := check(cfg, sandboxpolicy.RealPath(r.Path), policy.WorkDir)
			if (err == nil) == allowed {
				continue
			}
			var blocked *fence.PathBlockedError
			if errors.As(err, &blocked) {
				return fmt.Errorf("fence cannot let %s be %s (%s): %s", r.Path, verb, r.Source, blocked.Reason)
			}
			return fmt.Errorf("fence would let %s be %s, which the profile forbids (%s)", r.Path, verb, r.Source)
		}
		return nil
	}
	writable := policy.Writable()
	return errors.Join(
		expect(writable, "written", fence.CheckWritePath, true),
		expect(slices.Concat(writable, policy.Read, policy.ReadOnly), "read", fence.CheckReadPath, true),
		expect(policy.Exclude, "read", fence.CheckReadPath, false),
		expect(slices.Concat(policy.Exclude, policy.ReadOnly), "written", fence.CheckWritePath, false),
	)
}
