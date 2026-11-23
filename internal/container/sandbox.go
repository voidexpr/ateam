package container

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// sandboxGraceDelay is how long a cancelled sandboxed run gets between
// SIGTERM and SIGKILL: the same grace the agent package gives a host run.
const sandboxGraceDelay = 30 * time.Second

// SandboxContainer runs commands on this host under an OS-level sandbox
// backend, with a profile ateam rendered before the run: nono
// (https://nono.sh), or fence, embedded in ateam and started through
// `ateam fence-exec`. Paths and environment are the host's: nothing is
// translated.
//
// Every field is set once, before the pool dispatches, and never mutated:
// the profile file is the same for every run of the runner. A fence run
// starts its own ateam process, which holds that run's proxies: nothing is
// shared between runs.
type SandboxContainer struct {
	Kind    string   // the container type: "nono" or "fence"
	Prefix  []string // the backend's command line up to and including "--"
	WorkDir string   // the folder the profile makes writable; the command's cwd
}

func (n *SandboxContainer) Type() string { return n.Kind }

// CmdFactory wraps a command in the backend's command line. The
// environment is left unset so the agent builds it as for a host run.
func (n *SandboxContainer) CmdFactory() CmdFactory {
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		argv := append(append([]string{}, n.Prefix[1:]...), name)
		cmd := exec.CommandContext(ctx, n.Prefix[0], append(argv, args...)...)
		cmd.Dir = n.WorkDir
		ConfigureProcessLifecycle(cmd, sandboxGraceDelay)
		return cmd
	}
}

func (n *SandboxContainer) Run(ctx context.Context, opts RunOpts) error {
	cmd := n.CmdFactory()(ctx, opts.Command, opts.Args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = opts.Stdin, opts.Stdout, opts.Stderr
	return cmd.Run()
}

func (n *SandboxContainer) DebugCommand(opts RunOpts) string {
	return strings.Join(append(append(append([]string{}, n.Prefix...), opts.Command), opts.Args...), " ")
}

func (n *SandboxContainer) Prepare(context.Context) error { return nil }

func (n *SandboxContainer) GetContainerName() string { return "" }

func (n *SandboxContainer) TranslatePath(path string) string { return path }

func (n *SandboxContainer) ResolveTemplates(*strings.Replacer) {}

func (n *SandboxContainer) SetSourceWritable(bool) {}

func (n *SandboxContainer) SetContainerName(string) bool { return false }

// ApplyAgentEnv is a no-op: the command runs on this host and the agent
// builds its environment itself.
func (n *SandboxContainer) ApplyAgentEnv(map[string]string) {}

// ApplyContainerExtra is a no-op: container_extra describes docker runs.
func (n *SandboxContainer) ApplyContainerExtra(_, _ []string, _ map[string]string) {}

func (n *SandboxContainer) Clone() Container {
	return &SandboxContainer{Kind: n.Kind, Prefix: append([]string{}, n.Prefix...), WorkDir: n.WorkDir}
}
