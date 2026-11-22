package container

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// nonoGraceDelay is how long a cancelled nono run gets between SIGTERM and
// SIGKILL: the same grace the agent package gives a host run.
const nonoGraceDelay = 30 * time.Second

// NonoContainer runs commands on this host under nono (https://nono.sh),
// an OS-level sandbox, with a profile ateam rendered before the run. Paths
// and environment are the host's: nothing is translated.
//
// Every field is set once, before the pool dispatches, and never mutated:
// the profile file is the same for every run of the runner.
type NonoContainer struct {
	Prefix  []string // the nono command line up to and including "--"
	WorkDir string   // the folder the profile makes writable; the command's cwd
}

func (n *NonoContainer) Type() string { return "nono" }

// CmdFactory wraps a command in `nono run`. The environment is left unset
// so the agent builds it as for a host run.
func (n *NonoContainer) CmdFactory() CmdFactory {
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		argv := append(append([]string{}, n.Prefix[1:]...), name)
		cmd := exec.CommandContext(ctx, n.Prefix[0], append(argv, args...)...)
		cmd.Dir = n.WorkDir
		ConfigureProcessLifecycle(cmd, nonoGraceDelay)
		return cmd
	}
}

func (n *NonoContainer) Run(ctx context.Context, opts RunOpts) error {
	cmd := n.CmdFactory()(ctx, opts.Command, opts.Args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = opts.Stdin, opts.Stdout, opts.Stderr
	return cmd.Run()
}

func (n *NonoContainer) DebugCommand(opts RunOpts) string {
	return strings.Join(append(append(append([]string{}, n.Prefix...), opts.Command), opts.Args...), " ")
}

func (n *NonoContainer) Prepare(context.Context) error { return nil }

func (n *NonoContainer) GetContainerName() string { return "" }

func (n *NonoContainer) TranslatePath(path string) string { return path }

func (n *NonoContainer) ResolveTemplates(*strings.Replacer) {}

func (n *NonoContainer) SetSourceWritable(bool) {}

func (n *NonoContainer) SetContainerName(string) bool { return false }

// ApplyAgentEnv is a no-op: the command runs on this host and the agent
// builds its environment itself.
func (n *NonoContainer) ApplyAgentEnv(map[string]string) {}

// ApplyContainerExtra is a no-op: container_extra describes docker runs.
func (n *NonoContainer) ApplyContainerExtra(_, _ []string, _ map[string]string) {}

func (n *NonoContainer) Clone() Container {
	return &NonoContainer{Prefix: append([]string{}, n.Prefix...), WorkDir: n.WorkDir}
}
