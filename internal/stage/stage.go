// Package stage describes ateam's built-in workflow shape — the
// pre/agent-run/post sequence each top-level command follows.
//
// A Stage is a static declaration: which actions fire before the agent,
// how to assemble the prompt, what RunOpts to give the executor, and which
// actions fire after. Run drives the sequence against a *Ctx carrying the
// per-invocation state.
//
// Scope: built-in commands (report, review, verify, code, auto_setup).
// ateam exec and ateam parallel stay out — they take a raw prompt and have
// no fixed shape worth abstracting. This package is internal on purpose;
// the supported integration surface for external orchestration is
// `ateam exec` as a subprocess (see plans/python_framework_examples/).
package stage

import (
	"context"
	"errors"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
)

// Stage is the static description of a built-in command's workflow.
//
// Fields are read-only after construction. A new Stage is built per
// invocation in the cmd-layer (closures capture CLI flags). Run does
// not mutate the Stage — only the *Ctx threaded through it.
type Stage struct {
	// Name is the user-facing command label ("report", "review", ...).
	Name string

	// Action is the value recorded in agent_execs.action; usually one
	// of the runner.Action* constants. Reuses runner.Action vocabulary
	// for ps / inspect / call-DB consistency.
	Action string

	// BuildPrompt assembles the prompt to send to the agent. Called
	// after every Pre action runs; closures typically capture the
	// CLI inputs (--extra-prompt, --pre-prompt, etc.) needed to drive
	// the assembler. Required.
	BuildPrompt func(*Ctx) (string, error)

	// BuildRunOpts builds the runner.RunOpts for the AgentExecutor.Execute
	// call. Closures typically capture per-stage values (CanonicalDestFile,
	// OutputKind, PromptName, ...). Required.
	BuildRunOpts func(*Ctx) runner.RunOpts

	// RunAgent overrides the default agent-invocation step. When nil,
	// Stage.Run calls Ctx.Executor.Execute(Ctx.Context, Ctx.Prompt,
	// runOpts, Ctx.Progress) directly. Set it when the stage needs
	// non-default execution mechanics — notably code --tail, which runs
	// Execute concurrently with a DB tailer goroutine. The closure
	// owns the agent invocation in full (progress channel, cancellation,
	// any concurrent UI it drives) and returns the resulting summary;
	// Stage.Run still populates Ctx.Result from the return value.
	RunAgent func(c *Ctx, runOpts runner.RunOpts) runner.RunSummary

	// Pre runs before the agent invocation, in declaration order. Each
	// action can mutate the Ctx (set Executor, DB, …) and returns:
	//   - nil          → continue to the next action
	//   - ErrSkip      → end the stage successfully without running the
	//                    agent or any Post actions
	//   - any other    → abort; Post actions do NOT run
	Pre []Action

	// Post runs after the agent invocation, in declaration order. Each
	// action sees Ctx.Result (the agent's RunSummary). A Post action's
	// non-nil error aborts the remaining Post chain and is returned by
	// Run. ErrSkip is not meaningful here and behaves like any error.
	Post []Action
}

// Ctx carries per-invocation state across the action chain.
//
// Inputs the cmd-layer wants every action to see (Env, runtime context)
// live here. Stage-specific configuration (profile, model, …) is captured
// by the action structs themselves (the cmd-layer constructs actions with
// the values it needs). Result is populated by Run after the agent
// finishes and is the canonical access point for Post actions.
type Ctx struct {
	// Context is the cancellation context for the agent run. Threaded
	// straight to AgentExecutor.Execute.
	Context context.Context

	// Env is the resolved ateam env (project + org + config). Set by the
	// cmd-layer before Run.
	Env *root.ResolvedEnv

	// Executor is the configured agent runner for this stage. Typically
	// set by a Pre action (e.g. actions.ResolveExecutor) to a
	// *runner.AgentExecutor; the interface surface keeps stage tests
	// fakable without a full runner setup. Actions needing AgentExecutor
	// specifics (Model, Effort, etc.) type-assert at the call site.
	Executor Executor

	// DB is the open call DB. A Pre action opens it; Run closes it
	// after the Post chain (success or failure).
	DB *calldb.CallDB

	// Prompt is the assembled prompt string returned by BuildPrompt.
	// Visible to Post actions for inspection but should not be mutated.
	Prompt string

	// Result is the AgentExecutor.Execute summary. nil until the agent
	// run has completed. Read by Post actions.
	Result *runner.RunSummary

	// Progress is the channel passed straight to Executor.Execute. Nil
	// means no progress events are emitted (the verify/review shape).
	// Lifetime is the cmd-layer's: it creates the chan, spawns the
	// drain goroutine, and closes the chan / waits for the goroutine
	// after stage.Run returns. Stage.Run only forwards the chan; it
	// does not own it.
	Progress chan<- runner.RunProgress

	// Extra is an escape hatch for action-specific scratch data that
	// must flow between actions. Use sparingly — prefer typed fields
	// on the action struct itself when possible.
	Extra map[string]any
}

// Executor is the minimal surface Stage.Run consumes from the agent
// runner. The production implementation is *runner.AgentExecutor.
// Lives in the stage package so tests can stand up a fake without
// pulling in the runner machinery.
type Executor interface {
	Execute(ctx context.Context, prompt string, opts runner.RunOpts, progress chan<- runner.RunProgress) runner.RunSummary
}

// Action is implemented by both Pre and Post actions. The phase is
// determined by which slice on Stage holds the action, not by the
// interface. Method name Run is short and consistent across both
// phases; the action struct name (e.g. OpenDB, PrintArtifact) carries
// the semantic.
type Action interface {
	Run(*Ctx) error
}

// ErrSkip signals a Pre action wants to terminate the stage
// successfully without running the agent or any Post actions. Typical
// uses: --dry-run printing the prompt then returning, an empty-roles
// selector that has nothing to do.
var ErrSkip = errors.New("stage skipped")
