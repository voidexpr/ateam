// Package actions provides reusable Pre/Post Action implementations
// for stage.Stage. Each action is a small struct with a Run method
// that mutates / reads stage.Ctx.
//
// Scope: the agent-invocation envelope. Setup work — resolving the
// AgentExecutor, applying overrides, opening the DB — stays in the
// cmd-layer because it consumes CLI-specific helpers and the
// abstraction value would be marginal. The cmd-layer threads the
// configured Executor and DB onto the Ctx before calling stage.Run,
// then the Pre/Post actions in this package handle the gates and
// the artifact processing that's actually shared across stages.
package actions

import (
	"errors"
	"fmt"
	"os"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/display"
	"github.com/ateam/internal/stage"
)

// CheckConcurrentRuns is a Pre action that aborts when another process
// for the same (project, action[, role]) is already live. Maps to the
// cmd-layer's checkConcurrentRuns / checkConcurrentRunsEnv helpers.
//
// If is the gate: when false the action is a no-op (lets --force skip
// the check without removing the action from the chain).
type CheckConcurrentRuns struct {
	If     bool
	Action string
	Roles  []string // nil → all roles
}

func (a CheckConcurrentRuns) Run(c *stage.Ctx) error {
	if !a.If {
		return nil
	}
	if c.DB == nil {
		return fmt.Errorf("CheckConcurrentRuns: Ctx.DB is nil — open the DB before this action")
	}
	if c.Env == nil {
		return fmt.Errorf("CheckConcurrentRuns: Ctx.Env is nil")
	}
	projectID := c.Env.ProjectID()
	// Scratch mode (no project) has no per-project namespace.
	if projectID == "" && c.Env.ProjectDir == "" {
		return nil
	}
	if projectID == "" && c.Env.OrgDir != "" {
		return fmt.Errorf("cannot determine project ID for concurrency guard")
	}
	return checkRunning(c.DB, projectID, a.Action, a.Roles)
}

// checkRunning is the per-test entrypoint for the concurrency guard.
// Mirrors cmd/db.go::checkConcurrentRuns but lives here to avoid the
// cmd → internal import path.
func checkRunning(db *calldb.CallDB, projectID, action string, roles []string) error {
	running, err := db.FindRunning(projectID, action)
	if err != nil || len(running) == 0 {
		return nil
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
		alive = append(alive, fmt.Sprintf("%s/%s (pid=%d, exec=%d)", r.Action, r.Role, r.PID, r.ID))
	}
	if len(alive) == 0 {
		return nil
	}
	return fmt.Errorf("already running:\n  %s\n(pass --force to override)", joinLines(alive))
}

func joinLines(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += "\n  "
		}
		out += v
	}
	return out
}

// FailOnExecError is a Post action that returns the agent's error
// wrapped with the given Label, matching today's "<label> failed: <err>"
// pattern at the top of every stage's post-Execute block. Sits first
// in Post so the rest of the chain (PrintDone, PrintArtifact, ...)
// doesn't run when the agent failed.
type FailOnExecError struct {
	Label string
}

func (a FailOnExecError) Run(c *stage.Ctx) error {
	if c.Result == nil {
		return errors.New("FailOnExecError: Ctx.Result is nil — Stage.Run did not populate it")
	}
	if c.Result.Err != nil {
		label := a.Label
		if label == "" {
			label = "agent run"
		}
		return fmt.Errorf("%s failed: %w", label, c.Result.Err)
	}
	return nil
}

// PrintDone is a Post action that emits the standard "Done (duration,
// cost)" summary line. Mirrors cmd/table.go::printDone.
type PrintDone struct{}

func (a PrintDone) Run(c *stage.Ctx) error {
	if c.Result == nil {
		return errors.New("PrintDone: Ctx.Result is nil")
	}
	costSuffix := ""
	if cost := display.FmtCost(c.Result.Cost); cost != "" {
		costSuffix = ", " + cost
	}
	fmt.Printf("Done (%s%s)\n\n", display.FormatDuration(c.Result.Duration), costSuffix)
	return nil
}

// PrintArtifactPath is a Post action that emits a single "Label: path"
// line pointing at the canonical destination — e.g. "Verification
// report: /…/shared/verify.md". Used by review/verify/auto_setup to
// orient the user.
type PrintArtifactPath struct {
	Label string
	Path  string
}

func (a PrintArtifactPath) Run(c *stage.Ctx) error {
	if a.Label == "" || a.Path == "" {
		return nil
	}
	fmt.Printf("%s: %s\n", a.Label, a.Path)
	return nil
}

// PrintArtifactBody is a Post action that prints the on-disk artifact
// for --print. Reads Path; falls back to the agent's stream output
// when the file is missing/empty (the prompts' documented
// "Write failed → emit body" recovery). Mirrors
// cmd/table.go::printArtifact.
//
// If gates the action: false → no-op. Used to wire to opts.Print
// without conditional Action slices in the Stage declaration.
type PrintArtifactBody struct {
	If   bool
	Path string
}

func (a PrintArtifactBody) Run(c *stage.Ctx) error {
	if !a.If {
		return nil
	}
	if c.Result == nil {
		return errors.New("PrintArtifactBody: Ctx.Result is nil")
	}
	if a.Path != "" {
		if data, err := os.ReadFile(a.Path); err == nil && len(data) > 0 {
			fmt.Printf("\n%s", string(data))
			if data[len(data)-1] != '\n' {
				fmt.Println()
			}
			return nil
		}
	}
	if c.Result.Output != "" {
		fmt.Printf("\n%s\n", c.Result.Output)
	}
	return nil
}
