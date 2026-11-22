package container

import (
	"os/exec"
	"syscall"
	"time"
)

// ConfigureProcessLifecycle wires up two behaviors on a local cmd that
// exec.CommandContext does not provide by default:
//
//  1. The subprocess becomes its own process-group leader (Setpgid), and
//     the Cancel hook sends SIGTERM to the entire group on context
//     cancellation. This kills any subagent or shell descendants the agent
//     CLI forked, not just the agent itself.
//
//  2. WaitDelay is set so cmd.Wait does not block indefinitely on stdout
//     or stderr pipes inherited by descendants that survive the SIGTERM.
//     After the delay, the runtime sends SIGKILL and force-closes the pipes.
//
// For commands that run as processes of this host: plain host execution
// and sandbox wrappers such as nono. Docker-backed runs have their own
// lifecycle.
func ConfigureProcessLifecycle(cmd *exec.Cmd, grace time.Duration) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
	cmd.WaitDelay = grace
}
