// Package actions provides reusable PreExec / PostExec actions for
// flow.PromptBundle. Each action is a small struct implementing
// flow.Action.Run; closures capture cmd-layer state when needed.
//
// Scope: the agent-invocation envelope. Setup work (resolving the
// AgentExecutor, applying overrides, opening the DB) stays in the
// cmd-layer because it consumes CLI-specific helpers and abstracting it
// would force a giant options struct that mirrors each cmd's opts anyway.
//
// Successor to internal/stage/actions. FailOnExecError is gone — the
// flow framework sets Result.Flow.State = StateError directly from the
// AgentExecutor's Summary.IsError, and Pipeline stops on errored steps
// without a translation layer. PrintDone is gone too — StdoutReporter
// emits the "Done (dur, cost)" line as part of BundleEnd.
package actions

import (
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/flow"
)

// CheckConcurrentRuns is a PreExec action that aborts when another live
// process for the same (project, action[, roles]) exists. Mirrors the
// cmd/db.go::checkConcurrentRunsEnv helper for use inside a PromptBundle.
//
// If gates the action: when false the action is a no-op (lets --force
// skip the check without removing the action from the chain).
//
// Roles is optional; nil → check every role for the action. When set, the
// guard is scoped to those role IDs only.
type CheckConcurrentRuns struct {
	If     bool
	Action string
	Roles  []string
}

func (a CheckConcurrentRuns) Run(rc flow.RunCtx, env flow.RuntimeEnv, _ *flow.Result) flow.Flow {
	if !a.If {
		return flow.Flow{State: flow.StateContinue}
	}
	if rc.DB == nil {
		return errFlow(fmt.Errorf("CheckConcurrentRuns: rc.DB is nil — open the DB before this action"))
	}
	if rc.Resolved == nil {
		return errFlow(fmt.Errorf("CheckConcurrentRuns: rc.Resolved is nil"))
	}
	projectID := rc.Resolved.ProjectID()
	if projectID == "" && rc.Resolved.ProjectDir == "" {
		// Scratch mode (no project) — concurrency guard doesn't apply.
		return flow.Flow{State: flow.StateContinue}
	}
	if projectID == "" && rc.Resolved.OrgDir != "" {
		return errFlow(fmt.Errorf("cannot determine project ID for concurrency guard"))
	}
	action := a.Action
	if action == "" {
		action = env.Action
	}
	return checkRunning(rc.DB, projectID, action, a.Roles)
}

// checkRunning is the per-test entrypoint for the concurrency guard.
// Mirrors cmd/db.go::checkConcurrentRuns but lives here to avoid the
// cmd → internal import path.
func checkRunning(db *calldb.CallDB, projectID, action string, roles []string) flow.Flow {
	running, err := db.FindRunning(projectID, action)
	if err != nil {
		return errFlow(fmt.Errorf("FindRunning: %w", err))
	}
	if len(running) == 0 {
		return flow.Flow{State: flow.StateContinue}
	}
	roleSet := make(map[string]bool, len(roles))
	for _, r := range roles {
		roleSet[r] = true
	}
	var alive []string
	for _, r := range running {
		if len(roles) > 0 && !roleSet[r.Role] {
			continue
		}
		// Stale rows (process died without writing ended_at) must not block
		// new runs. Match cmd/db.go::checkConcurrentRuns: only treat the
		// row as live when the PID still exists.
		if r.PID <= 0 || !processAlive(r.PID) {
			continue
		}
		alive = append(alive, fmt.Sprintf("%s/%s (pid=%d, exec=%d)", r.Action, r.Role, r.PID, r.ID))
	}
	if len(alive) == 0 {
		return flow.Flow{State: flow.StateContinue}
	}
	return errFlow(fmt.Errorf("already running:\n  %s\n(pass --force to override)",
		strings.Join(alive, "\n  ")))
}

// processAlive checks whether the given PID has a live process. Mirrors
// cmd/table.go::isProcessAlive; duplicated to avoid a cmd→internal/flow
// reverse import.
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// PrintArtifactPath is a PostExec action that emits "<Label>: <Path>" so
// the user can find the bundle's primary output. Empty Label or Path → no-op.
type PrintArtifactPath struct {
	Label string
	Path  string
}

func (a PrintArtifactPath) Run(rc flow.RunCtx, env flow.RuntimeEnv, _ *flow.Result) flow.Flow {
	if a.Label == "" || a.Path == "" {
		return flow.Flow{State: flow.StateContinue}
	}
	fmt.Printf("%s: %s\n", a.Label, a.Path)
	return flow.Flow{State: flow.StateContinue}
}

// PrintArtifactBody is a PostExec action that prints the on-disk artifact
// for `--print`. Reads Path; falls back to the agent's stream output
// (Result.Summary.Output) when the file is missing/empty.
//
// If gates the action: false → no-op.
type PrintArtifactBody struct {
	If   bool
	Path string
}

func (a PrintArtifactBody) Run(rc flow.RunCtx, env flow.RuntimeEnv, res *flow.Result) flow.Flow {
	if !a.If {
		return flow.Flow{State: flow.StateContinue}
	}
	if a.Path != "" {
		if data, err := os.ReadFile(a.Path); err == nil && len(data) > 0 {
			fmt.Printf("\n%s", string(data))
			if data[len(data)-1] != '\n' {
				fmt.Println()
			}
			return flow.Flow{State: flow.StateContinue}
		}
	}
	// File missing or empty — fall back to the agent's stream output.
	if res != nil && res.Summary != nil && res.Summary.Output != "" {
		fmt.Printf("\n%s\n", res.Summary.Output)
	}
	return flow.Flow{State: flow.StateContinue}
}

func errFlow(err error) flow.Flow {
	return flow.Flow{State: flow.StateError, Reason: err.Error(), Err: err}
}
