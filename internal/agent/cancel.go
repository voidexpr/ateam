package agent

import (
	"os/exec"
	"time"

	"github.com/ateam/internal/container"
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

// configureProcessLifecycle makes cmd the leader of its own process group,
// signalled as a whole on cancellation, with processGraceDelay before the
// kill: see container.ConfigureProcessLifecycle. Only for a cmd created
// here (host execution); a CmdFactory configures its own.
func configureProcessLifecycle(cmd *exec.Cmd) {
	container.ConfigureProcessLifecycle(cmd, processGraceDelay)
}
