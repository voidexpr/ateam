# Flow Package Architecture

This document describes the implemented `internal/flow` package and how it
fits into agent execution, containers, artifacts, and the call database.

The short version: `internal/flow` is an in-process composition layer for
agent-running CLI commands. It decides which prompt bundles run, in what order,
and how lifecycle events are reported. It does not execute subprocesses itself,
own containers, or persist workflow state. Those responsibilities remain in
`internal/runner`, `internal/agent`, `internal/container`, and `internal/calldb`.

## Scope

`internal/flow` is used by the agent-running commands:

- `exec`
- `parallel`
- `report`
- `review`
- `code`
- `verify`
- `auto-setup`

`ateam all` is not itself a single `flow.Pipeline`; it remains a cmd-layer
sequence that calls `runReport`, `runReview`, `runCode`, and `runVerify`.
Each phase then uses `flow` internally.

The package is intentionally internal. The supported external workflow surface
is still the CLI, especially `ateam exec` and `ateam parallel`, not Go imports.

## Package Structure

```text
internal/flow/
  flow.go
    RunCtx, RuntimeEnv, Executor
    Flow, Result, PipelineResult
    Step implementations:
      PromptBundle
      Pipeline
      Parallel

  reporter.go
    Reporter interface
    BaseReporter / NoopReporter
    StageInfo, BundleInfo, StageOutcome

  reporters_stdout.go
    StdoutReporter for single-bundle commands

  actions/
    CheckConcurrentRuns
    PrintArtifactPath
    PrintArtifactBody

cmd/table_reporter.go
  tableReporter, the concurrent live table implementation of flow.Reporter
```

The important split is:

- `flow` owns composition and lifecycle callbacks.
- `cmd` owns CLI option parsing, prompt assembly, runner resolution, DB opening,
  and command-specific post-processing.
- `runner.AgentExecutor` owns the actual agent invocation, artifact layout,
  stream parsing, DB row lifecycle, and container setup.

## Core Types

### `RunCtx`

`RunCtx` is the session carrier passed unchanged through the step tree:

```go
type RunCtx struct {
    Ctx      context.Context
    DB       *calldb.CallDB
    Resolved *root.ResolvedEnv
    Reporter flow.Reporter
}
```

It contains shared resources for the whole command invocation:

- cancellation context
- call database handle
- resolved project/org environment
- one reporter instance

`flow` itself only reads these values or passes them to actions and reporters.
The framework does not open or close the DB.

### `RuntimeEnv`

`RuntimeEnv` is the execution configuration that can be rebound at any
composition boundary:

```go
type RuntimeEnv struct {
    Executor  flow.Executor
    WorkDir   string
    Role      string
    Action    string
    DryRun    bool
    Batch     string
    PromptDir string
}
```

The key field is `Executor`, a small interface implemented by
`*runner.AgentExecutor`:

```go
Execute(ctx, prompt, opts, onProgress) runner.RunSummary
```

`report` uses this to bind different per-role profiles by assigning a
per-bundle `RuntimeEnv` with a role-specific `Executor`.

### `PromptBundle`

`PromptBundle` is the leaf node. One bundle maps to at most one
`runner.AgentExecutor.Execute` call.

Lifecycle:

1. Apply bundle-level `Env`, `Role`, and `Action` overrides.
2. Emit `Reporter.BundleStart`.
3. Run `PreExec` actions in order.
4. Render the prompt with `Render(env)`.
5. Build `runner.RunOpts` with `RunOpts(env)`.
6. In dry-run mode, stop before executing the agent.
7. Call `env.Executor.Execute`.
8. Convert `RunSummary.IsError` into `Result.Flow.StateError`.
9. If the agent succeeded, run `PostExec` actions.
10. Emit `Reporter.BundleEnd`.

`PostExec` intentionally does not run after a skipped pre-action, render error,
or agent failure. This preserves the old "only print artifact pointers on real
success" behavior.

### `Pipeline`

`Pipeline` runs child steps sequentially.

- First errored step stops the pipeline.
- Later steps are recorded as skipped with reason `earlier step failed`.
- `StateSkip` results from a child count as success for pipeline control flow.
- Top-level `flow.Run(Pipeline{...})` returns one `StepOutcome` per immediate
  child.
- Nested pipelines flatten their leaf results when they are children of another
  step; nested structure is visible through reporter stage events, not through a
  nested result tree.

### `Parallel`

`Parallel` fans out child steps concurrently.

- `Workers` caps concurrent workers.
- `Workers <= 0` defaults to `min(len(Steps), 5)`.
- Dry-run and single-step cases use the sequential path.
- Errors do not short-circuit sibling work.
- `PreDispatch`, when set, runs once per step before acquiring a worker slot.
  A non-nil error stops dispatching more work and produces skipped results for
  the remaining steps.
- Worker panics are recovered and converted to error results.

Current concurrent users are `report` and `parallel`.

## Command Shapes

```text
exec
  PromptBundle("exec")

review
  PromptBundle("review", role="supervisor")

code
  PromptBundle("code", role="supervisor")

verify
  PromptBundle("verify", role="supervisor")

auto-setup
  PromptBundle("auto-setup", role="supervisor")

parallel
  Parallel("agents", PromptBundle per user prompt)

report
  Parallel("roles", PromptBundle per role)
```

`report` is the main reason `flow` exists. The previous single-stage abstraction
could not naturally represent "N role agents in parallel, each with its own
profile, with aggregate post-processing after all results are known."

## Execution Boundary

`flow` stops at the `Executor` interface. Production execution crosses into:

```text
flow.PromptBundle
  -> runner.AgentExecutor.Execute
       -> Insert agent_execs row
       -> derive logs/<exec_id>/ and runtime/<exec_id>/
       -> render sandbox/settings/prompt/cmd.md
       -> clone and prepare container, if any
       -> clone agent with resolved templates
       -> agent.Run(ctx, agent.Request)
       -> consume normalized agent.StreamEvent values
       -> emit runner.RunProgress callbacks
       -> promote runtime files on success
       -> update agent_execs row
```

Agents do not know whether they are running on the host or through Docker. The
container layer supplies `agent.Request.CmdFactory`; agents use that factory
instead of `exec.CommandContext` when present.

## Agent And Container Relation

The execution stack is layered like this:

```text
cmd resolves runtime.hcl/profile
  -> runner.AgentExecutor
       Agent:     internal/agent.Agent
       Container: internal/container.Container or nil
       CallDB:    *calldb.CallDB

flow carries AgentExecutor as RuntimeEnv.Executor
  -> PromptBundle calls Execute

runner.Execute
  -> clones Container
  -> resolves container templates
  -> calls Container.Prepare
  -> installs Container.CmdFactory into agent.Request
  -> clones Agent with resolved templates
  -> calls Agent.Run
```

Important details:

- `Container` is optional. `nil` means host execution.
- Docker and docker-exec are transparent to the agent through `CmdFactory`.
- Request `WorkDir` and settings paths are translated to container paths when a
  container is active.
- Stream and stderr files are not translated because the host-side runner opens
  them and captures agent output into those files.
- Container `Prepare` runs on a per-exec clone, but shared `PrepareGuard` /
  `KeyedPrepareGuard` pointers deduplicate image/container setup across clones.

## Database Model

There is one current execution table: `agent_execs` in `state.sqlite`.

The DB location is:

- project mode: `.ateam/state.sqlite`
- scratch `exec` / `parallel` mode: `.ateamorg/state.sqlite`

`flow` has no table of its own and no persistent workflow record. The only
persistence relation is through the runner-created `agent_execs` rows.

### Logical Relations

```text
project_id
  groups rows by ateam project

action
  report | review | code | verify | exec | parallel | debug ...

role
  role id for report/parallel/exec, "supervisor" for review/code/verify

batch
  groups rows from one command invocation
  examples: report-<timestamp>, parallel-<timestamp>, code-<timestamp>

stream_file
  relative path to logs/<exec_id>/stream.jsonl

output_file
  relative path to the immutable per-exec runtime primary output when present

pid/container_id
  liveness and inspection metadata for running rows
```

There are no SQL foreign keys. The relations are convention-based and enforced
by command and runner code.

### Row Lifecycle

```text
runner.Execute starts
  -> InsertCall(project_id, profile, agent, container, action, role, batch,
                model, prompt_hash, started_at, git_start_hash, work_dir)
  -> exec_id returned by SQLite autoincrement
  -> UpdateStreamFile(exec_id, "logs/<exec_id>/stream.jsonl")
  -> SetPID(exec_id, pid, container_id) when the agent reports process start

runner.Execute finalizes
  -> promote files if successful
  -> UpdateOutputFile(exec_id, "runtime/<exec_id>/<primary>.md") when present
  -> UpdateCall(ended_at, duration, exit_code, is_error, cost, tokens,
                git_end_hash, git_end_branch, model, context metrics)
```

`ended_at IS NULL` means the run is still live or crashed before finalization.
The concurrency guard uses this state plus PID liveness checks.

### Artifact Relation

Per execution:

```text
<state-dir>/
  logs/<exec_id>/
    stream.jsonl
    stderr.out
    settings.json
    prompt.md
    cmd.md

  runtime/<exec_id>/
    <primary output and sidecars>
```

On success, runtime files are cloned to the action's canonical destination:

```text
report      -> .ateam/shared/report/<role>.md
review      -> .ateam/shared/review.md
verify      -> .ateam/shared/verify.md
auto-setup  -> .ateam/shared/auto_setup.md
code        -> .ateam/shared/code/<exec_id>/
exec        -> no promotion
parallel    -> no promotion
```

The database `output_file` points at the immutable runtime copy, not the
canonical file that later runs overwrite.

## Reporter Architecture

`Reporter` is the single flow observability surface:

```go
StageStart(StageInfo)
StageEnd(StageInfo, StageOutcome)
StepSkipped(parent, stepName, reason)
BundleStart(BundleInfo)
BundleEnd(BundleInfo, Result)
AgentEvent(BundleInfo, runner.RunProgress)
```

Two main implementations exist:

- `flow.StdoutReporter` for single-bundle commands.
- `cmd.tableReporter` for parallel `report` and `parallel` commands.

Reporter methods may be called concurrently from `Parallel` workers.
Implementations must own synchronization.

`StdoutReporter` is intended for single-bundle use and is not synchronized.
`tableReporter` uses a mutex around live row state, result collection, counters,
and rendering throttles.

## Concurrency Architecture

### Flow-Level Goroutine Map

```text
cmd goroutine
  -> flow.Run
       -> Pipeline: sequential calls
       -> Parallel:
            - shared Reporter
            - semaphore channel bounds workers
            - one goroutine per dispatched child step
            - mutex protects Parallel's aggregate result slice
            - each worker calls child.execute(...)
                 -> PromptBundle
                      -> runner.AgentExecutor.Execute
                           -> agent process / stream event channel
                           -> synchronous progress callback
                                -> Reporter.AgentEvent
```

`Parallel` results are collected in completion order, not submission order.
Command code that needs display order uses labels or role IDs as keys.

### Progress Fan-In

The older runner pool API exposed `RunProgress` through channels. The current
flow path uses callback fan-in:

```text
runner.Execute emits RunProgress
  -> PromptBundle callback
  -> rc.Reporter.AgentEvent(bundle, progress)
```

The callback is synchronous. It may be invoked from multiple goroutines when
there are multiple parallel bundles, and `runner.Execute` also documents that
callbacks may come from more than one internal goroutine. Therefore reporter
thread-safety is part of the reporter contract.

### DB Concurrency

All parallel bundles in one command share one `*calldb.CallDB`.

`calldb.Open` configures SQLite with:

- WAL mode
- `busy_timeout=5000`
- `SetMaxOpenConns(1)`

This serializes DB access through one connection and avoids `SQLITE_BUSY` on
concurrent inserts/updates. The tradeoff is that code must not hold a result
cursor open while trying to write through the same DB handle. `MigrateLogsLayout`
explicitly collects rows and closes the cursor before issuing updates for this
reason.

### AgentExecutor, Agent, And Container Ownership

The same `AgentExecutor` may be reachable by many `Parallel` workers. The
contract is:

- command code mutates it only during construction
- after dispatch, fields are read-only
- `AgentExecutor.Execute` clones mutable agent/container state for each run
- shared safe resources are intentionally shared, especially `*calldb.CallDB`
  and container prepare guards

The clone boundary remains inside `runner.AgentExecutor.Execute`; `flow` does
not clone executors.

### Panic Boundaries

There are two panic recovery layers:

- `PromptBundle.execute` recovers panics so `BundleStart` is paired with
  `BundleEnd`.
- `Parallel` workers also recover and convert panics to error results.

The bundle-level recovery is the important one for reporters: it prevents a
live table row from being left mid-state when render, pre-exec, execute, or
post-exec panics.

## Match Against `CONCURRENCY.md`

Overall, the implemented `flow` path mostly preserves the `CONCURRENCY.md`
pool-boundary contract, but the document is now partly stale because the
parallel command path has moved from `runner.RunPool` channels to
`flow.Parallel` plus reporter callbacks.

### Still Matches

1. **AgentExecutor is read-only after dispatch.**
   `flow` passes `RuntimeEnv.Executor` to worker goroutines but does not mutate
   it. Per-role report runners are fully constructed before `flow.Run`.

2. **Workers own mutable run state.**
   `flow.Parallel` owns only its semaphore, wait group, mutex, and aggregate
   results. Per-run mutable execution state is still allocated inside
   `runner.Execute`.

3. **Clone at one boundary.**
   Agent and container cloning still happens once at the top of
   `AgentExecutor.Execute`, not throughout helper code and not in `flow`.

4. **Progress payloads remain self-contained.**
   `runner.RunProgress` is still value-shaped. `RunSummary.ToolCounts` is still
   produced per run and attached to a leaf `Result`.

5. **Shared DB remains safe by design.**
   `*calldb.CallDB` wraps `*sql.DB`, and the implementation serializes through
   one SQLite connection.

6. **No process-global env/CWD mutation in worker execution.**
   `AgentExecutor.Execute`, agents, and containers still pass environment and
   workdir through request/config fields rather than `os.Setenv` or `os.Chdir`.

### Partly Stale Or Needs Updating

1. **Goroutine map is obsolete.**
   `CONCURRENCY.md` describes `cmd/table.go:runPool`, a progress consumer, a
   completed channel, and `runner.RunPool`. The migrated `report` and
   `parallel` commands now use `flow.Parallel`, `flow.Reporter`, and
   callback-based progress. `runner.RunPool` still exists and is tested, but it
   is no longer the main architecture for those commands.

2. **Channel contract does not describe the flow path.**
   The `completed` channel deadlock guard still applies to callers of
   `runner.RunPool`, but `flow.Parallel` has no completed channel. It uses a
   mutex-protected result slice and reporter callbacks.

3. **Filesystem path rule is stale.**
   `CONCURRENCY.md` says stream, stderr, settings, prompt, exec, and output
   paths are keyed by `startedAt` timestamp plus `RoleID`, and mentions a shared
   runner log. Current runner code keys artifacts by `agent_execs.id`:
   `logs/<exec_id>/...` and `runtime/<exec_id>/...`. The old `runner.log` has
   been removed from the current layout.

4. **Display concurrency changed.**
   The old doc mentions separate display/progress-consumer goroutines guarded
   by `statusMu`. Current parallel display is `cmd.tableReporter`: reporter
   callbacks mutate rows under `tableReporter.mu`, and rendering is throttled.
   In live terminal mode it also redirects process-wide `os.Stdout` and
   `os.Stderr` through pipes during the run so stray Go-side writes do not
   corrupt the live table.

5. **Process-global stdout/stderr redirection should be documented.**
   This is not a violation of the worker-execution rule because it happens in
   cmd-layer UI setup, not inside `AgentExecutor.Execute`, `Agent.Run`, or a
   `Container` method. It is still process-global mutation during a parallel
   command and should be called out in `CONCURRENCY.md` as an intentional UI
   exception with restore-before-close ordering.

6. **PreDispatch skip accounting differs by layer.**
   `flow.Parallel` returns skipped `Result` values when `PreDispatch` stops
   dispatch, but no `BundleStart` / `BundleEnd` fires for those skipped steps.
   `tableReporter.StageEnd` compensates by marking still-queued rows as skipped.
   This is a flow-specific behavior that is not covered by the older pool
   contract.

## Architectural Assessment

`internal/flow` is deliberately thin and currently well-placed:

- It removes duplicated command envelopes.
- It expresses both single-agent and parallel-agent command shapes.
- It leaves process execution and persistence in the existing runner/database
  layer.
- It keeps the future door open for richer workflows without committing to a
  persistent workflow engine.

The main documentation debt is `CONCURRENCY.md`: its core immutability and
clone-first rules still match the code, but its concrete goroutine map,
channel model, and filesystem path section describe the pre-flow or pre-exec-id
architecture.

The main implementation caveat is that `flow` does not enforce executor
immutability. It relies on the same construction-time discipline as the older
pool path. That is consistent with the current runner tests, but future flow
extensions should avoid adding mutable shared state to `RuntimeEnv`,
`PromptBundle`, or reporter implementations.
