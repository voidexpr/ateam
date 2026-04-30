package agent

import (
	"os/exec"
	"syscall"
	"time"
)

// processGraceDelay is the grace period given to an agent subprocess between
// the SIGTERM sent on context cancellation and the SIGKILL escalation that
// exec's WaitDelay performs. It also caps how long Wait will block waiting
// for inherited stdout/stderr pipes (e.g. held open by a subagent that was
// forked off the agent CLI) before forcibly closing them.
//
// 30 seconds gives a Claude/Codex CLI enough time to abort an in-flight HTTP
// request gracefully (cancelling the API call, returning tokens) without
// allowing a hung run to drag on indefinitely.
const processGraceDelay = 30 * time.Second

// configureProcessLifecycle wires up two behaviors on cmd that exec.CommandContext
// does not provide by default:
//
//  1. The subprocess becomes its own process-group leader (Setpgid), and
//     the Cancel hook sends SIGTERM to the entire group on context
//     cancellation. This kills any subagent or shell descendants the agent
//     CLI forked, not just the agent itself — necessary to free the OS
//     resources (file descriptors, API quota) that a stuck subagent would
//     otherwise hold.
//
//  2. WaitDelay is set so cmd.Wait does not block indefinitely on stdout
//     or stderr pipes inherited by descendants that survive the SIGTERM
//     (or escape the process group). After the delay, the runtime sends
//     SIGKILL and force-closes the pipes.
//
// Only applicable when the cmd was created via exec.CommandContext (host
// execution). Container-backed runs go through CmdFactory and have their
// own lifecycle.
func configureProcessLifecycle(cmd *exec.Cmd) {
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
	cmd.WaitDelay = processGraceDelay
}
