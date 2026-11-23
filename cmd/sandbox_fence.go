package cmd

import (
	"errors"
	"fmt"
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
	cfg, err := loadFenceConfig(settings)
	if err != nil {
		return 0, err
	}
	manager := fence.NewManager(cfg, false, false)
	defer manager.Cleanup()
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
// file it will load. It returns the ateam binary that runs it.
func writeFenceConfig(policy *sandboxpolicy.Policy, configFile string) (string, error) {
	rendered, err := sandboxpolicy.RenderFence(policy, goruntime.GOOS)
	if err != nil {
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
	return os.Executable()
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
