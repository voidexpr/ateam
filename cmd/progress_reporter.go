package cmd

import (
	"github.com/ateam/internal/flow"
	"github.com/ateam/internal/runner"
)

// channelProgressReporter bridges flow's AgentEvent callback back to a
// runner.RunProgress channel — the shape every pre-flow caller already
// uses (printProgress, autoRolesProgress, etc.). Used by the single-
// bundle verbs (auto_roles, inspect --auto-debug) that don't need
// flow's StdoutReporter machinery.
//
// Non-blocking send so a slow consumer can't stall agent progress
// forwarding. Closing the channel is the caller's responsibility (after
// flow.RunBundle returns and the progress goroutine drains).
type channelProgressReporter struct {
	flow.BaseReporter
	ch chan<- runner.RunProgress
}

func (r *channelProgressReporter) AgentEvent(_ flow.BundleInfo, p runner.RunProgress) {
	select {
	case r.ch <- p:
	default:
	}
}
