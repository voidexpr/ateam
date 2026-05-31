//go:build sketch

// Worked examples: cmd/verify.go and cmd/report.go translated into the
// flow framework. Body-only sketches — flag wiring, option struct
// definitions, and dry-run/preview paths are elided to keep the focus
// on the composition shape.
//
// The two examples cover the spectrum:
//   - verify : single bundle, single PromptBundle wrapped in a Pipeline of one.
//   - report : N concurrent bundles with per-role profile, Parallel inside Pipeline.
package cmd

import (
	"fmt"
	"time"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/display"
	"github.com/ateam/internal/flow"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
)

// ============================================================
// Example 1: verify — single bundle
// ============================================================

func runVerifyV2(opts VerifyOptions) error {
	env, err := resolveEnv()
	if err != nil {
		return err
	}
	if err := requireGitRepo(env, runner.ActionVerify); err != nil {
		return err
	}

	cr, err := resolveRunner(env, opts.Profile, opts.Agent, runner.ActionVerify, "", opts.DockerAutoSetup)
	if err != nil {
		return err
	}
	if err := applyRunnerOverrides(cr, env, opts.runnerOverrides(), runner.ActionVerify); err != nil {
		return err
	}

	extra, _ := prompts.ResolveOptional(opts.ExtraPrompt)
	pre, _ := prompts.ResolveOptional(opts.PrePrompt)
	post, _ := prompts.ResolveOptional(opts.PostPrompt)

	db, err := openStateDB(env)
	if err != nil {
		return err
	}
	defer db.Close()
	cr.CallDB = db

	ctx, stop := cmdContext()
	defer stop()

	rtEnv := flow.RuntimeEnv{
		Executor: cr,
		WorkDir:  env.WorkDir,
		Action:   runner.ActionVerify,
		DryRun:   opts.DryRun,
		Batch:    "verify-" + time.Now().Format(display.TimestampFormat),
	}

	verify := flow.PromptBundle{
		Name:   "verify",
		Action: runner.ActionVerify,
		Render: func(e flow.RuntimeEnv) (string, error) {
			return assembleVerifyV1(env, "supervisor", extra, pre, post)
		},
		RunOpts: func(e flow.RuntimeEnv) runner.RunOpts {
			return runner.RunOpts{
				OutputKind:        runner.OutputKindVerify,
				CanonicalDestFile: env.VerifyPath(),
				TimeoutMin:        env.Config.Verify.EffectiveTimeout(opts.Timeout),
				Verbose:           opts.Verbose,
			}
		},
		PreExec: []flow.Action{
			actionCheckConcurrentRuns{Force: opts.Force},
		},
		PostExec: []flow.Action{
			actionPrintArtifactPath{Path: env.VerifyPath()},
		},
	}

	rc := flow.RunCtx{
		Ctx:      ctx,
		DB:       db,
		Reporter: &flow.StdoutReporter{},
	}

	return flow.Run(verify, rtEnv, rc).FirstError()
}

// ============================================================
// Example 2: report — Parallel of per-role bundles
// ============================================================

func runReportV2(opts ReportOptions) error {
	env, err := resolveEnv()
	if err != nil {
		return err
	}
	if err := requireGitRepo(env, runner.ActionReport); err != nil {
		return err
	}

	if opts.AutoRoles {
		if len(opts.Roles) > 0 {
			return fmt.Errorf("--auto-roles and --roles are mutually exclusive")
		}
		if opts.RerunFailed {
			return fmt.Errorf("--auto-roles and --rerun-failed are mutually exclusive")
		}
	}

	roleIDs, openedDB, err := resolveReportRoleList(env, opts) // wraps the --auto-roles / --rerun-failed / --roles branches
	if err != nil {
		return err
	}
	if len(roleIDs) == 0 {
		return nil
	}
	if err := root.EnsureRoles(env.ProjectDir, roleIDs); err != nil {
		return err
	}

	extra, _ := prompts.ResolveOptional(opts.ExtraPrompt)
	pre, _ := prompts.ResolveOptional(opts.PrePrompt)
	post, _ := prompts.ResolveOptional(opts.PostPrompt)

	timeout := env.Config.Report.EffectiveTimeout(opts.Timeout)
	batch := "report-" + time.Now().Format(display.TimestampFormat)

	baseRunner, err := resolveRunner(env, opts.Profile, opts.Agent, runner.ActionReport, "", opts.DockerAutoSetup)
	if err != nil {
		return err
	}
	if err := applyRunnerOverrides(baseRunner, env, opts.runnerOverrides(), runner.ActionReport); err != nil {
		return err
	}

	db := openedDB
	if db == nil {
		if db, err = openStateDB(env); err != nil {
			return err
		}
		defer db.Close()
	}
	baseRunner.CallDB = db

	cliOverridesProfile := opts.Profile != "" || opts.Agent != ""
	defaultProfile := env.Config.ResolveProfile(runner.ActionReport, "")

	// Build one PromptBundle per role. Per-role profile is baked into the
	// bundle's Env override — outer Parallel doesn't see roles or profiles.
	var bundles []flow.Step
	for _, rid := range roleIDs {
		rid := rid

		roleRunner := baseRunner
		if !cliOverridesProfile {
			if rp := env.Config.ResolveProfile(runner.ActionReport, rid); rp != defaultProfile {
				if rr, err := resolveRunner(env, rp, "", runner.ActionReport, rid, opts.DockerAutoSetup); err == nil {
					_ = applyRunnerOverrides(rr, env, opts.runnerOverrides(), runner.ActionReport)
					rr.CallDB = db
					roleRunner = rr
				}
			}
		}

		roleEnv := flow.RuntimeEnv{
			Executor: roleRunner,
			WorkDir:  env.WorkDir,
			Role:     rid,
			Action:   runner.ActionReport,
			DryRun:   opts.DryRun,
			Batch:    batch,
		}

		b := flow.PromptBundle{
			Name:   rid,
			Role:   rid,
			Action: runner.ActionReport,
			Env:    &roleEnv,
			Render: func(e flow.RuntimeEnv) (string, error) {
				return assembleRoleReportV1(env, rid, "role "+rid, extra, pre, post, opts.IgnorePreviousReport)
			},
			RunOpts: func(e flow.RuntimeEnv) runner.RunOpts {
				return runner.RunOpts{
					RoleID:            rid,
					Action:            runner.ActionReport,
					OutputKind:        runner.OutputKindReport,
					PromptName:        rid,
					CanonicalDestFile: env.RoleReportPath(rid),
					WorkDir:           env.WorkDir,
					TimeoutMin:        timeout,
					Verbose:           opts.Verbose,
					Batch:             batch,
				}
			},
		}
		if opts.Print {
			b.PostExec = append(b.PostExec, actionPrintArtifactBody{Path: env.RoleReportPath(rid)})
		}

		bundles = append(bundles, b)
	}

	if !opts.Force {
		if err := checkConcurrentRunsEnv(db, env, runner.ActionReport, roleIDs); err != nil {
			return err
		}
	}

	maxParallel := env.Config.Report.EffectiveMaxParallel(opts.Parallel)

	ctx, stop := cmdContext()
	defer stop()

	pipeline := flow.Pipeline{
		Name: "report",
		Steps: []flow.Step{
			flow.Parallel{Name: "roles", Steps: bundles, Workers: maxParallel},
		},
	}

	rc := flow.RunCtx{
		Ctx:      ctx,
		DB:       db,
		Reporter: &flow.TableReporter{},
	}

	result := flow.Run(pipeline, flow.RuntimeEnv{}, rc)

	// Aggregate, cmd-shape concerns: count, conditional --review chain, hint.
	succeeded, failed := 0, 0
	for _, sr := range result.Steps {
		for _, r := range sr.Results {
			if r.Flow.State == flow.StateError {
				failed++
			} else {
				succeeded++
			}
		}
	}

	// All-success rule for --review (corrected from the old any-success bug).
	if opts.Review && failed == 0 && succeeded > 0 {
		fmt.Println()
		return runReviewV2(reviewOptionsFromReport(opts))
	}
	if succeeded > 0 {
		fmt.Printf("\nRun 'ateam review' to have the supervisor synthesize findings.\n")
	}

	return result.FirstError()
}

// ============================================================
// Action examples (move into internal/flow/actions/ in the real impl)
// ============================================================

type actionCheckConcurrentRuns struct {
	If      bool
	RoleIDs []string
}

func (a actionCheckConcurrentRuns) Run(rc flow.RunCtx, env flow.RuntimeEnv) flow.Flow {
	if !a.If {
		return flow.Flow{State: flow.StateContinue}
	}
	// rc.Resolved.ProjectID() + rc.DB.FindRunning(...) — same logic as today's
	// internal/stage/actions::CheckConcurrentRuns, just reading from RunCtx
	// instead of *stage.Ctx.
	return flow.Flow{State: flow.StateContinue}
}

type actionPrintArtifactPath struct{ Path string }

func (a actionPrintArtifactPath) Run(rc flow.RunCtx, env flow.RuntimeEnv) flow.Flow {
	fmt.Printf("→ %s\n", a.Path)
	return flow.Flow{State: flow.StateContinue}
}

type actionPrintArtifactBody struct{ Path string }

func (a actionPrintArtifactBody) Run(rc flow.RunCtx, env flow.RuntimeEnv) flow.Flow {
	// today's printArtifact(path, output) — uses Result.Summary.Output if available,
	// falls back to reading the path. Sketch only.
	return flow.Flow{State: flow.StateContinue}
}

// ============================================================
// Things this sketch deliberately doesn't address
// ============================================================
//
// 1. exec / parallel cmds. They become "build a single PromptBundle (exec)
//    or build a Parallel of bundles (parallel) and call flow.Run". exec
//    uses StdoutReporter; parallel uses TableReporter. Same shape as the
//    examples above, no new framework surface needed.
//
// 2. code --tail. The bundle sets RunAgent to a closure that runs
//    Executor.Execute concurrently with the DB tailer. The framework
//    treats it as a black box, like today's stage.Stage.RunAgent.
//
// 3. inspect --auto-debug. Single PromptBundle with a different Render.
//    Existing path.
//
// 4. parallel-of-parallel rendering. The framework supports it; the
//    default TableReporter collapses it to a flat table (StageEnd resets
//    rows on the inner Parallel, fresh table opens on the next). A
//    future TreeReporter handles nesting properly; orthogonal to this
//    refactor.
//
// 5. Action package factoring. The two example actions are inline here
//    for sketch readability; in the real package they live in
//    internal/flow/actions/ alongside the equivalents of today's
//    actions.CheckConcurrentRuns / PrintArtifactPath / etc.
