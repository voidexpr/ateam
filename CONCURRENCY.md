# Concurrency Contract — Pool Boundary

The ateam pool (`runner.RunPool`) runs N agents in parallel. Two SIGSEGVs
in production came from shared mutable state bleeding into worker
goroutines. This document is the contract that prevents new ones.

If you're adding a field to `Runner`, a method to `Container` / `Agent`,
or a new resource used under the pool — read this first.

## Goroutine map

```
                      cmd/table.go:runPool (main)
                      │
                      ├─ spawns: display goroutine   (statusMu-guarded rendering)
                      ├─ spawns: progress consumer   (statusMu-guarded updates)
                      └─ spawns: runner.RunPool goroutine
                                  │
                                  ├─ acquires semaphore (maxParallel)
                                  └─ spawns N worker goroutines
                                      │
                                      └─ each: Runner.Run → Agent.Run →
                                               its own exec.Cmd + JSON scanner
```

Channels carry data between these goroutines:

- `progress chan<- RunProgress` — lossy, non-blocking send from workers.
- `completed chan<- RunSummary` — blocking, one per task + close. Caller
  MUST provide `cap(completed) ≥ len(tasks)` OR drain concurrently with
  `RunPool`; otherwise `RunPool` refuses to dispatch (see pool.go guard).

## The 7-rule contract

### 1. Every Runner reachable from the task slice is read-only after dispatch

`PoolTask.Runner` can override the pool's shared Runner per task
(`internal/runner/pool.go:12`, used from `cmd/report.go:213`). **All**
such Runners — the shared one plus every override — must be immutable
once `RunPool` is called. Mutate during construction only.

### 2. Workers own their mutable state

Stack-local, function parameters, or per-task clones. **No** shared
mutable state unless it's a thread-safe primitive with a narrow,
documented contract:

- `*sql.DB` (`Runner.CallDB`) — stdlib-safe, further serialized with
  `SetMaxOpenConns(1)`.
- `*container.PrepareGuard` / `*KeyedPrepareGuard` — sync.Once (+ mutex
  for the keyed variant) shared on purpose across clones so `Prepare`
  dedupes.
- Channel payloads (below).

### 3. Clone at one boundary

Cloning happens at the top of `Runner.Run`:

- `Runner.Container.Clone()` → per-task container with independent slice
  and map backing memory.
- `ResolveAgentTemplateArgs(r.Agent, vars)` → per-task agent via
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

### 6. Per-task filesystem paths

Stream, stderr, exec, settings, prompt, and output paths are keyed by
`startedAt` timestamp + `RoleID`. The one shared sink is the runner log
(`Runner.LogFile`, append-only). Keep log lines short — concurrent
`write(2)` to a regular file is best-effort and may interleave on long
writes. The log is never read back for crash recovery, so interleaving is
a cosmetic issue, not a correctness one.

### 7. No process-global mutation from the pool path

Forbidden inside `Runner.Run`, `Agent.Run`, or any `Container` method
running under a pool worker:

- `os.Setenv`, `os.Unsetenv`, `os.Chdir` — these mutate the process
  env / CWD that every goroutine reads from.
- Global flag or config writes.
- Changing any package-level var.

Secrets resolved by the CLI flow through `ac.Env` via `IsolateCredentials`
(`internal/secret/validate.go`) at construction time. Agents receive
them via `c.Env` on their per-task clone; containers receive them via
`d.Env` on theirs. `os.Setenv` is not on the path.

## What stays shared (and why it's safe)

| Resource | Why it's safe |
|---|---|
| `Runner.CallDB` | `*sql.DB` stdlib-concurrent, `SetMaxOpenConns(1)` serializes. |
| `Runner.Container.prepareGuard` | `sync.Once` (or `sync.Mutex`-guarded per-key `sync.Once`). Shared on purpose so `Prepare` runs once per pool. |
| `progress` / `completed` channels | Go channels. Payloads are value types or owned-once maps. |
| `cmd/table.go` display `statusRows` / `renderedRows` | `statusMu`-protected. All reads and writes hold the mutex. |
| `Runner.LogFile` | Append-only. Best-effort; may interleave on long lines. |
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
    scalar/string/slice/map fields; fails if any Runner field is
    mutated during `Run`.
- `internal/container/prepare_guard_test.go`:
  - `TestPrepareGuardRunsOnce`, `TestKeyedPrepareGuardDedupesPerKey`,
    `TestPrepareGuardCachesError`.

`make test` runs all of these under `-race`. A race-enabled linux
companion binary is available via `make build-all-race` for reproducing
production SIGSEGVs against the race detector.

## When in doubt

Prefer cloning over locking. Prefer channels over shared maps. If you're
tempted to add a `sync.Mutex` to a Runner field, you're probably about to
violate rule 1 or 2 — revisit and see if the data can be cloned or moved
onto `RunOpts` / `TaskContext` instead.
