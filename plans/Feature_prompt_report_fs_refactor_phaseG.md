# Phase G: flow framework — replacing internal/stage

Successor to Task 2 (Stage abstraction) in
`Feature_prompt_report_fs_refactor_impl_steps.md`. Stage landed for 4 of 5
supervisor cmds; report stayed hand-written because Stage's single-execute
shape can't express N parallel agents + aggregate post-processing
(`cmd/report.go:230-410`). Phase G replaces Stage with a composition
framework that handles report cleanly and unifies the shape across every
agent-running cmd.

Design sketch: `plans/flow_sketch/{flow.go,reporter.go,cmd_examples.go}`
(build tag `sketch`, not in normal builds).

Reference reading before starting:
- `internal/stage/stage.go` — what we're replacing
- `internal/stage/actions/actions.go` — actions surface
- `cmd/report.go` — the cmd this refactor unblocks
- `plans/python_framework_examples/ateam.py` — the prior-art that inspired the model

## Design decisions (locked)

### Why composition, not "extend Stage"

Three Stage-extension options were considered for report and rejected
([Phase F section in impl-steps][]):

1. `Stage.RunPool` variant or `Stages` plural — significant API expansion for one consumer
2. `RunAgent` returning a synthetic single-`RunSummary` — wrong shape, loses per-role visibility
3. Leave report hand-written — what Phase F chose

The composition model (Pipeline/Parallel/PromptBundle) expresses report as
`Pipeline(Parallel(per-role-bundles))` with per-role profile baked into
each bundle's RuntimeEnv override. It also converges exec, parallel,
verify, review, auto_setup, code, and report into one shape — no
hand-written envelope per cmd.

[Phase F section in impl-steps]: Feature_prompt_report_fs_refactor_impl_steps.md#phase-f-why-report-doesnt-migrate

### Core types

```go
// internal/flow/flow.go
type RunCtx struct {
    Ctx      context.Context
    DB       *calldb.CallDB
    Resolved *root.ResolvedEnv
    Reporter Reporter
}

type RuntimeEnv struct {
    Executor  *runner.AgentExecutor
    WorkDir   string
    Role      string
    Action    string
    DryRun    bool
    Batch     string
    PromptDir string
}

type Step interface { /* internal */ }   // PromptBundle, Pipeline, Parallel
type Result struct { Bundle *PromptBundle; Env RuntimeEnv; Flow Flow; Summary *runner.RunSummary }
type StepOutcome struct { Name string; Results []Result; Skipped bool; Reason string }
type PipelineResult struct { Steps []StepOutcome; FirstErrorIndex int }

func Run(top Step, env RuntimeEnv, rc RunCtx) PipelineResult
```

Key splits:
- **RunCtx**: session-scoped resources (Ctx, DB, Resolved, Reporter). Threaded unchanged through every Step.Execute call.
- **RuntimeEnv**: "where & how to invoke" config. Freely rebound at Pipeline/Parallel/PromptBundle boundaries via `Env *RuntimeEnv`.
- **Pipeline stops on first errored step.** Skip counts as success. Skipped downstream steps appear in `PipelineResult.Steps` with `Skipped:true`; the Reporter sees `StepSkipped(parent, name, reason)`.
- **No nested-result tree.** Inner Pipeline structure flattens when wrapped in a parent composition (only the outer Pipeline's `Steps` carries skip info). Reporter events convey full tree structure for renderers that need it.

### Reporter — single observability surface

```go
// internal/flow/reporter.go
type Reporter interface {
    StageStart(StageInfo)
    StageEnd(StageInfo, StageOutcome)
    StepSkipped(parent StageInfo, stepName, reason string)
    BundleStart(BundleInfo)
    BundleEnd(BundleInfo, Result)
    AgentEvent(BundleInfo, runner.RunProgress)
}
```

- 6 methods. `BaseReporter` no-ops every method for embed-and-override.
- One Reporter instance per `Run()`; Pipeline/Parallel children share it (no `Sub(stage) Reporter`).
- Implementations own thread-safety — methods fire concurrently from Parallel children.
- Two ship implementations:
  - **`StdoutReporter`** — single-bundle default (exec, verify, review, auto_setup, code). Streams bundle lifecycle + agent progress to stdout. Owns the "Done {bundle} in {dur}" line (replaces today's `PrintDone` action).
  - **`TableReporter`** — concurrent default (parallel, report). Today's `runPool` live-table renderer adapted to consume Reporter callbacks.

### What goes away

- **`internal/stage/`** (whole package) — replaced by `internal/flow/`.
- **`internal/stage/actions/FailOnExecError`** — framework now sets `Result.Flow.State == StateError` directly from `Summary.IsError`; Pipeline gates on that. No translation layer needed.
- **`internal/stage/actions/PrintDone`** — `StdoutReporter.BundleEnd` prints it.
- **`code --tail`** — use `ateam tail` as a separate process. This removes the need for `PromptBundle.RunAgent` (the only consumer was code's concurrent tailer goroutine), simplifying the framework.
- **`cmd/table.go::printDone` and the inline `printArtifact` helpers** — moved into the actions package and into `StdoutReporter`/`PrintArtifactBody` respectively.

### What stays

- **`runner.AgentExecutor`** — concrete type, unchanged. PromptBundle.Env carries one. No interface abstraction at the runner level.
- **`runner.RunOpts`, `runner.RunSummary`, `runner.RunProgress`** — unchanged. PromptBundle's `RunOpts` closure builds the same shape today's cmds build.
- **`cmd/db.go::checkConcurrentRunsEnv`** — exists today, used by `parallel`. The flow action wraps the same helper.

### Action catalog

`internal/flow/actions/`:

| Action | Today's name | Notes |
|---|---|---|
| `CheckConcurrentRuns{If, Action, Roles}` | same | reads `rc.DB` + `rc.Resolved` |
| `PrintArtifactPath{Label, Path}` | same | no Ctx — just prints |
| `PrintArtifactBody{If, Path}` | same | reads `Result.Summary.Output` for fallback |

`cmd/code.go::printCodeSessionAction` stays cmd-local (only meaningful for code).

### PromptBundle shape (final)

```go
type PromptBundle struct {
    Name   string
    Role   string         // optional override of env.Role
    Action string         // optional override of env.Action
    Env    *RuntimeEnv    // optional override of parent's env (used for per-role profile)

    Render   func(env RuntimeEnv) (string, error)
    RunOpts  func(env RuntimeEnv) runner.RunOpts
    PreExec  []Action
    PostExec []Action
}
```

No `RunAgent` hook (code --tail goes away). No `OnSummary` / `OnEvent`
callbacks (use a custom Reporter if you need them).

## Implementation steps

### Step 1 — Framework + tests, no cmd touched

Lands `internal/flow/` with all framework code, all framework tests, and
the `StdoutReporter`. **`TableReporter` is deferred to Step 2e** — only
report and parallel need it, and report's migration sequences last.

Files:
```
internal/flow/
  flow.go                    types + Step impls + Run()
  reporter.go                Reporter interface + BaseReporter
  reporters_stdout.go        StdoutReporter
  flow_test.go               framework tests
  reporter_test.go           reporter wiring tests
  testutil_test.go           fakeExecutor, recordingReporter

internal/flow/actions/
  check_concurrent_runs.go
  check_concurrent_runs_test.go    real *calldb.CallDB, tempdir
  print_artifact_path.go
  print_artifact_path_test.go      stdout capture
  print_artifact_body.go
  print_artifact_body_test.go      file + Summary.Output fallback
```

Test inventory for `internal/flow/flow_test.go`:
```
PromptBundle:
  TestPromptBundle_HappyPath                — single bundle, success
  TestPromptBundle_PreSkip                  — Pre Skip → no agent, success
  TestPromptBundle_PreError                 — Pre Error → no agent, no Post
  TestPromptBundle_RenderError              — Render err → Error Result
  TestPromptBundle_ExecutorError            — Summary.IsError → Error Result, Post still runs
  TestPromptBundle_DryRun                   — env.DryRun → no Execute
  TestPromptBundle_EnvOverride              — bundle.Env overrides parent

Pipeline:
  TestPipeline_SequentialOrder              — A then B then C
  TestPipeline_StopOnError                  — A errors → B,C marked Skipped
  TestPipeline_SkipDoesNotStop              — A skips → B runs
  TestPipeline_StepSkippedReporterFires     — Reporter.StepSkipped called for B,C

Parallel:
  TestParallel_RunsConcurrently             — fakeExecutor with timing
  TestParallel_WorkersBound                 — cap obeyed
  TestParallel_AllRunEvenIfOneErrors        — no short-circuit
  TestParallel_DryRun                       — sequential

Run() + PipelineResult:
  TestRun_TopLevelSingleBundle              — wraps in single-step result
  TestRun_TopLevelParallel                  — same
  TestRun_TopLevelPipelineGetsRunDetailed   — multi-step result
  TestPipelineResult_FirstError             — convenience reducer
  TestPipelineResult_FirstErrorIndex        — set / -1
  TestRunCtx_ReporterDefault                — nil → NoopReporter
```

Test inventory for `internal/flow/reporter_test.go`:
```
TestStdoutReporter_BundleLifecycle          — capture output, assert lines
TestStdoutReporter_SkipAndError             — state-specific lines
TestStdoutReporter_AgentEventTerse          — phase/turn/tokens line format
TestBaseReporter_AllNoOp                    — embedding works
```

Shared fixtures in `testutil_test.go`:
- `fakeExecutor` — implements `Execute(ctx, prompt, opts, progress) RunSummary`; configurable result + delay; records calls
- `recordingReporter` — captures every callback in arrival order; embeddable for assertions

Acceptance for Step 1:
- `make run-ci` green
- `go test ./internal/flow/...` green
- Zero changes outside `internal/flow/`
- StdoutReporter renders the same shape that today's `cmd/table.go::printDone` + per-cmd start lines render (golden output match)

### Step 2 — Per-cmd migrations, one commit each

Each commit:
1. Rewrite the cmd's `runXxx` body in flow shape (closures over cmd-layer state, Bundle/Pipeline/Parallel construction, `flow.Run()`, aggregate post-processing in cmd-layer).
2. Delete the cmd's `stage.Stage{...}` block.
3. Cmd's existing tests pass unchanged (entrypoint signature unchanged).
4. Add framework-level tests if the migration revealed a gap.

Ordering (simplest → most complex):

**Step 2a — `cmd/verify.go`** (reference shape, single bundle)
- Replaces ~50 lines of `stage.Stage{...} + stage.Ctx{...} + stage.Run(...)` with the flow equivalent.
- Reporter: `StdoutReporter`.
- No Env override; default RuntimeEnv built from cmd.

**Step 2b — `cmd/review.go`** (single bundle, --print branch)
- Same shape as verify; PrintArtifactBody with `If: opts.Print`.
- Reporter: `StdoutReporter`.

**Step 2c — `cmd/auto_setup.go`** (single bundle, always-print)
- Same shape; PrintArtifactBody with `If: true`.
- Reporter: `StdoutReporter`.

**Step 2d — `cmd/code.go`** (single bundle, drops `--tail`)
- Bundle posts `printCodeSessionAction` (cmd-local) after `PrintDone`'s replacement (BundleEnd).
- **Removes the `--tail` flag.** Users who want live tailing run `ateam tail` separately. Doc this in the commit body; ROLES.md / COMMANDS.md updates land here.

**Step 2e — `cmd/report.go`** (Parallel, per-role profile)
- This is the migration Phase F deferred. Lands `internal/flow/reporters_table.go` as part of this commit (moving ~200 lines from `cmd/runpool*` display).
- `Pipeline(Parallel(per-role-PromptBundles))`; per-role `Executor` baked into each `Bundle.Env`.
- Aggregate post-processing (count, conditional --review, hint) stays in cmd-layer.
- **`--review` semantics corrected**: today's `succeeded > 0` becomes `failed == 0 && succeeded > 0` (the bug previously called out in the design discussion).
- Reporter: `TableReporter`.

**Step 2f — `cmd/exec.go`** (single bundle, never had Stage)
- Today exec runs the agent inline. Flow translation: single PromptBundle whose Render returns the user's raw prompt.
- Reporter: `StdoutReporter`.
- Opens future ability to attach `--pre-prompt`/`--post-prompt`/`--pre-exec`/`--post-exec` via the bundle's PreExec/PostExec.

**Step 2g — `cmd/parallel.go`** (Parallel of bundles, never had Stage)
- N user-provided prompts → N PromptBundles → one `Parallel{}`.
- Reporter: `TableReporter` (now exists from Step 2e).
- Future `--pre-prompt`/`--post-prompt` follow the same pattern as exec — defer until needed.

### Step 3 — Delete `internal/stage/`

One commit:
- `rm -rf internal/stage/ internal/stage/actions/`
- Update `cmd/table.go` (remove now-dead `printDone` / `printArtifact` if they aren't referenced after Step 2).
- Update `plans/Feature_prompt_report_fs_refactor_impl_steps.md` — replace the "Task 2: Stage abstraction" section with a pointer to this Phase G doc and a "Stage retired" entry.
- Update `CLAUDE.md` if it mentions Stage anywhere.

Verification:
- `make run-ci` green
- `grep -r internal/stage .` returns nothing
- All cmd tests still pass

## Out of scope for Phase G

- **Tree-aware result navigation** — `StepOutcome.Sub *PipelineResult` for nested-Pipeline structure. Add when a real consumer needs it. Today's PipelineResult flattening is sufficient.
- **`TreeReporter`** — a Reporter that renders nested compositions hierarchically. Default `TableReporter` collapses nested Parallel into a flat table; that's good enough for the only nested case ateam has today (none, actually).
- **`JSONReporter` / structured progress events** — the natural home for Task 3 (progress telemetry). Lands after Phase G.
- **`exec` / `parallel` pre/post-prompt flags** — composition is now ready for them; flag wiring waits for a user ask.
- **Higher-level "workflow" framework** — name reserved for a future persistent workflow layer that builds on `internal/flow`. Out of scope here.

## Open items (resolve during implementation, not blocking)

- **PrintDone-into-BundleEnd** confirmed; the explicit `PrintDone` action goes away. If a future cmd needs a customized "done" line, it implements a custom Reporter (or uses a `OnceAction`-style Post action that prints).
- **`bundleProgress` lifecycle** — sketch leaves `openBundleProgress` / `closeBundleProgress` elided. Wiring at impl time: bundle allocates a buffered chan, spawns a goroutine that forwards each `RunProgress` to `rc.Reporter.AgentEvent(bi, p)`, returns the write end; after `Execute` returns, the chan is closed, goroutine drains and exits. ~20 lines in `flow.go`.
- **Reporter cleanup / Close()** — none of the current Reporters need it (terminal output flushes on its own; the table reporter's renderer goroutine exits on StageEnd). If a future Reporter needs cleanup, add `Reporter.Close()` then.

## Estimated diff size

Step 1: ~+700 lines (framework + tests), ~0 lines removed.
Step 2 (cumulative across 7 cmds):
  ~+250 lines new cmd-layer code (mostly cmd_examples.go shape)
  ~+300 lines new framework (TableReporter, lands in 2e)
  ~-450 lines removed (each cmd loses 30-50 lines of Stage boilerplate; PrintDone duplication etc.)
Step 3: ~-350 lines removed (`internal/stage/` whole package).

Net: roughly break-even on LoC; substantial increase in testability and uniformity. The 5 cmds that today have nearly-identical Stage wiring blocks collapse into a single shape with cmd-specific bundle factories.

## Commit message style for the series

```
flow: framework skeleton + stdout reporter + actions
flow: cmd/verify migrated; stage usage removed from verify
flow: cmd/review migrated
flow: cmd/auto_setup migrated
flow: cmd/code migrated; --tail removed (use `ateam tail`)
flow: cmd/report migrated; table reporter lands
flow: cmd/exec migrated to flow
flow: cmd/parallel migrated to flow
flow: remove internal/stage; doc updates
```
