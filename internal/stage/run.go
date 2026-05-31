package stage

import (
	"errors"
	"fmt"

	"github.com/ateam/internal/runner"
)

// Run drives a Stage end-to-end against c:
//
//  1. Pre actions, in declaration order. Each can mutate c (set Executor,
//     DB, etc). A non-nil error aborts the whole chain; the ErrSkip
//     sentinel ends the stage successfully (no agent run, no Post).
//     One of the Pre actions is expected to set c.Executor.
//  2. BuildPrompt fills c.Prompt.
//  3. BuildRunOpts builds the RunOpts and the agent is invoked via
//     c.Executor.Execute — c.Result captures the summary.
//  4. Post actions, in declaration order. Each sees c.Result. A non-nil
//     error from any action aborts the remaining Post chain.
//
// Returns the first error encountered (wrapped with the stage name and
// the action's struct type), or nil. Run does NOT close c.DB — that's
// the cmd-layer's responsibility (typical pattern: a deferred Close in
// the cmd wrapper, since the DB is needed by Pre actions before Run
// could possibly own its lifetime).
func Run(s Stage, c *Ctx) error {
	if s.BuildPrompt == nil {
		return fmt.Errorf("stage %q: BuildPrompt is required", s.Name)
	}
	if s.BuildRunOpts == nil {
		return fmt.Errorf("stage %q: BuildRunOpts is required", s.Name)
	}
	if c == nil {
		return fmt.Errorf("stage %q: nil Ctx", s.Name)
	}

	for _, a := range s.Pre {
		if err := a.Run(c); err != nil {
			if errors.Is(err, ErrSkip) {
				return nil
			}
			return fmt.Errorf("stage %q: pre %T: %w", s.Name, a, err)
		}
	}

	prompt, err := s.BuildPrompt(c)
	if err != nil {
		return fmt.Errorf("stage %q: BuildPrompt: %w", s.Name, err)
	}
	c.Prompt = prompt

	if c.Executor == nil {
		return fmt.Errorf("stage %q: no executor set on Ctx — a Pre action must populate Ctx.Executor before the agent runs", s.Name)
	}
	runOpts := s.BuildRunOpts(c)
	// Default path: c.OnProgress is nil for verify/review-shape stages and
	// non-nil for auto_setup/code. The cmd-layer owns lifetime of whatever
	// underlies the callback (e.g. a chan it created via runner.ProgressChan);
	// Stage.Run just forwards. When the stage supplies a custom RunAgent,
	// that closure owns the agent invocation in full (used by code --tail
	// to interleave Execute with a tailer).
	var result runner.RunSummary
	if s.RunAgent != nil {
		result = s.RunAgent(c, runOpts)
	} else {
		result = c.Executor.Execute(c.Context, c.Prompt, runOpts, c.OnProgress)
	}
	c.Result = &result

	for _, a := range s.Post {
		if err := a.Run(c); err != nil {
			return fmt.Errorf("stage %q: post %T: %w", s.Name, a, err)
		}
	}
	return nil
}
