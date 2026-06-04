# Concurrency Contract — Pool Boundary

The ateam pool (`runner.RunPool`) runs N agents in parallel. Two SIGSEGVs
in production came from shared mutable state bleeding into worker
goroutines. This document is the contract that prevents new ones.

If you're adding a field to `AgentExecutor`, a method to `Container` / `Agent`,
or a new resource used under the pool — read this first.

## Goroutine map

```
                      cmd/parallel|report|code|run_all (main)
                      │
                      └─ flow.Run(step, env, rc) — dispatches steps
                                  │
                                  └─ flow.Parallel.runConcurrently
                                              │
                                              ├─ acquires semaphore (Workers)
                                              └─ spawns N worker goroutines
                                                  │
                                                  └─ each: PromptBundle.execute →
                                                           AgentExecutor.Execute →
                                                           Agent.Run + exec.Cmd + JSON scanner
                                                       (reporter callbacks fire here)
```

The production path (cmd → flow) is callback-based:

- `onProgress func(RunProgress)` — synchronous callback invoked from
  inside each worker for every progress event. Implementations must be
  thread-safe.
- Step results are returned synchronously from `Step.execute` and
  accumulated under a `flow.Parallel` mutex.

The `runner.RunPool` channel interface still exists for tests and direct
callers (`cmd/parallel_test.go`, `internal/runner/race_test.go`):

- `progress chan<- RunProgress` — lossy, non-blocking send from workers.
- `completed chan<- RunSummary` — blocking, one per agent exec + close.
  Caller MUST provide `cap(completed) ≥ len(execs)` OR drain concurrently
  with `RunPool`; otherwise `RunPool` refuses to dispatch (see pool.go
  guard).

## Flow-level concurrency

`flow.Parallel.runConcurrently` (`internal/flow/flow.go`) is the
orchestration-layer pool used by `cmd/parallel`, `cmd/report`, `cmd/code`,
and `cmd/run_all`. It mirrors `runner.RunPool`'s guarantees:

- Semaphore-bounded fan-out (`Workers`, default `min(len(steps), 5)`).
- Per-worker panic recovery — a panicking step becomes a synthetic Error
  `Result` instead of tearing down its siblings.
- `PreDispatch` hook fires once per step before its slot is acquired;
  returning an error short-circuits dispatch and remaining steps become
  Skip results.
- No short-circuit on regular errors — every step runs to completion.

Reporter callbacks (`StageStart`, `BundleStart`, `AgentExecStart`,
`AgentEvent`, `BundleEnd`, `StageEnd`) are invoked **synchronously from
worker goroutines**. Any reporter (`MultiReporter`, `BundleLogReporter`,
`JSONReporter`, `tableReporter`) must guard its own internal state — the
shipped ones use `sync.Mutex`. If you add a new reporter, assume
concurrent calls.

`flow.Parallel` eventually calls `AgentExecutor.Execute` per step, so the
7-rule contract below still applies end-to-end.

## The 7-rule contract

### 1. Every AgentExecutor reachable from the exec slice is read-only after dispatch

`PoolExec.AgentExecutor` can override the pool's shared AgentExecutor per agent exec
(`internal/runner/pool.go:12`, used from `cmd/report.go:213`). **All**
such AgentExecutors — the shared one plus every override — must be immutable
once `RunPool` is called. Mutate during construction only.

### 2. Workers own their mutable state

Stack-local, function parameters, or per-agent exec clones. **No** shared
mutable state unless it's a thread-safe primitive with a narrow,
documented contract:

- `*sql.DB` (`AgentExecutor.CallDB`) — stdlib-safe, further serialized with
  `SetMaxOpenConns(1)`.
- `*container.PrepareGuard` / `*KeyedPrepareGuard` — sync.Once (+ mutex
  for the keyed variant) shared on purpose across clones so `Prepare`
  dedupes.
- Channel payloads (below).

### 3. Clone at one boundary

Cloning happens at the top of `AgentExecutor.Execute`:

- `AgentExecutor.Container.Clone()` → per-agent exec container with independent slice
  and map backing memory.
- `ResolveAgentTemplateArgs(r.Agent, vars)` → per-agent exec agent via
  `Agent.CloneWithResolvedTemplates`, which re-allocates `Args`, `Env`,
  and `Pricing` (see `PricingTable.Clone`). No map or slice backing
  memory is shared between clones and the original.

Do not spread `Clone()` calls through sub-helpers. If you find yourself
cloning inside a Run sub-function, lift it to `Run` instead.

### 4. Channels are self-contained

`RunProgress`: value types only, no pointers into shared memory.

`RunSummary`: the `ToolCounts` map is **ownership-transferred** at send
time (the worker stops touching it after putting it in the summary).
Don't add fields that reference state the worker keeps mutating.

### 5. Package-level state is read-only after init

Safe examples:

- Function pointers (`var parseClaudeLine = streamutil.ParseClaudeLine`).
- Compiled regexps (`var dateSuffix = regexp.MustCompile(...)`) — `regexp`
  is documented safe for concurrent use.
- Map literals used as read-only lookups (`var preserveEntries = ...`).
- `sync.Once`-guarded caches (`var keyringOnce sync.Once`).

Forbidden: any `var foo = X` that multiple goroutines write to without
a sync primitive.

### 6. Per-agent exec filesystem paths

Stream, stderr, exec, settings, prompt, and output paths are keyed by
`startedAt` timestamp + `RoleID`. The one shared sink is the runner log
(`AgentExecutor.LogFile`, append-only). Keep log lines short — concurrent
`write(2)` to a regular file is best-effort and may interleave on long
writes. The log is never read back for crash recovery, so interleaving is
a cosmetic issue, not a correctness one.

### 7. No process-global mutation from the pool path

Forbidden inside `AgentExecutor.Execute`, `Agent.Run`, or any `Container` method
running under a pool worker:

- `os.Setenv`, `os.Unsetenv`, `os.Chdir` — these mutate the process
  env / CWD that every goroutine reads from.
- Global flag or config writes.
- Changing any package-level var.

Secrets resolved by the CLI flow through `ac.Env` via `IsolateCredentials`
(`internal/secret/validate.go`) at construction time. Agents receive
them via `c.Env` on their per-agent exec clone; containers receive them via
`d.Env` on theirs. `os.Setenv` is not on the path.

## What stays shared (and why it's safe)

| Resource | Why it's safe |
|---|---|
| `AgentExecutor.CallDB` | `*sql.DB` stdlib-concurrent, `SetMaxOpenConns(1)` serializes. |
| `AgentExecutor.Container.prepareGuard` | `sync.Once` (or `sync.Mutex`-guarded per-key `sync.Once`). Shared on purpose so `Prepare` runs once per pool. |
| `progress` / `completed` channels | Go channels. Payloads are value types or owned-once maps. |
| `cmd/table_reporter.go:tableReporter` internal state | `tableReporter.mu sync.Mutex`. Reporter callbacks fire from worker goroutines. |
| Flow reporters (`BundleLogReporter`, `JSONReporter`, ...) | Each carries its own `sync.Mutex`. Mandatory: callbacks are invoked concurrently. |
| `AgentExecutor.LogFile` | Append-only. Best-effort; may interleave on long lines. |
| `os.Stderr` verbose/warn writes | Kernel-serialized per `write(2)`; long multi-write messages may interleave cosmetically. |

## Tests that enforce this

- `internal/runner/race_test.go`:
  - `TestResolveAgentTemplateArgsConcurrentRace/{claude,codex}` —
    clone returns fresh `Args` backing memory.
  - `TestResolveAgentTemplateArgsClonesPricing/{claude,codex}` —
    clone returns fresh `Pricing` map; writes through a clone don't
    affect the original.
  - `TestRunPoolSharedContainerRace` — Docker container clone isolates
    `ExtraArgs` / `ExtraVolumes` / `Env`.
  - `TestRunPoolSharedDockerExecRace` — same for docker-exec.
  - `TestRunPoolSharedContainerDoesNotMutateTemplate` — the shared
    original keeps its templates.
  - `TestRunPoolSharedPrepareGuardRunsOnce` — `PrepareGuard` dedupes
    across clones.
  - `TestRunPoolCompletedChannelDeadlockGuard` — undersized `completed`
    is rejected.
  - `TestRunPoolRunnerFieldsUnchanged` — reflection-walk over
    scalar/string/slice/map fields; fails if any AgentExecutor field is
    mutated during `Run`.
- `internal/container/prepare_guard_test.go`:
  - `TestPrepareGuardRunsOnce`, `TestKeyedPrepareGuardDedupesPerKey`,
    `TestPrepareGuardCachesError`.

`make test` runs all of these under `-race`. A race-enabled linux
companion binary is available via `make build-all-race` for reproducing
production SIGSEGVs against the race detector.

## When in doubt

Prefer cloning over locking. Prefer channels over shared maps. If you're
tempted to add a `sync.Mutex` to a AgentExecutor field, you're probably about to
violate rule 1 or 2 — revisit and see if the data can be cloned or moved
onto `RunOpts` / `TaskContext` instead.
