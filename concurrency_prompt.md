Please review this plan and provide feedback. Our goal is to make concurrency bugs very unlikely but having strong architectural patterns that future code changes can follow.

Provide critical feedback.



Concurrency Audit & Non-Shared Pool Boundary

Context

Two SIGSEGVs in production pool runs (fixed one, saw another) prompted a
comprehensive look at what the N parallel pool goroutines actually share.
Slapping mutexes on every new race is not sustainable. The goal is:

1. Enumerate every mutable thing the pool path touches.
2. Decide what stays shared (and why), what gets cloned, what flows through
channels.
3. Codify the contract so future changes can't silently reintroduce a race.

Scope = the execution path cmd.runPool → runner.RunPool → Runner.Run → Agent.Run, for all supported agents (Claude, Codex, Mock) and containers
(none, Docker, DockerExec).

Audit findings

Safe — cloned per task (established by prior commits 1842bbf + 02c3c95)

┌─────────────┬──────────────────────────────┬──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┐
│    Field    │           Location           │                                                                                    How it's isolated                                                                                     │
├─────────────┼──────────────────────────────┼──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
│ r.Agent     │ internal/runner/runner.go:77 │ ResolveAgentTemplateArgs → CloneWithResolvedTemplates returns a fresh struct with fresh Args/Env (internal/agent/claude.go:39, codex.go:38).                                             │
├─────────────┼──────────────────────────────┼──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
│ r.Container │ runner.go:78                 │ r.Container.Clone() at the top of Runner.Run (runner.go:282). Deep-copies ExtraArgs/ExtraVolumes/Env/ForwardEnv; shares *PrepareGuard intentionally so Prepare still runs once per pool. │
└─────────────┴──────────────────────────────┴──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┘

Safe — read-only after construction (Runner fields)

- Scalars: ProjectName, SourceDir, ProjectDir, OrgDir, Profile,
ProjectID, ContainerType, ContainerNameSource, ConfigDir,
LogFile, ContainerName (no longer written during Run — the write at
the old runner.go:495 was removed in 1842bbf).
- Slices/maps never mutated during Run: ExtraArgs, ArgsInsideContainer,
ArgsOutsideContainer, and all Sandbox sub-slices (RWPaths,
ROPaths, Denied, ExtraWrite, ExtraRead, ExtraDomains,
ExtraExcludedCmd, ExtraWriteDirs). Run copies r.ExtraArgs into a
local before appending (runner.go:199–208); Sandbox slices are read only.

Safe — shared but thread-safe primitive

- r.CallDB (internal/calldb/*): *sql.DB is safe for concurrent use;
SetMaxOpenConns(1) further serializes writes against the sqlite file.
Used from multiple pool goroutines via InsertCall/SetPID/UpdateCall.
- Container.prepareGuard (*PrepareGuard / *KeyedPrepareGuard in
internal/container/prepare_guard.go): sync.Once (+ sync.Mutex for
the keyed variant) ensures side-effectful Prepare (docker build / docker
cp / precheck) fires at most once per pool (or per resolved container
name, for docker-exec).
- progress chan<- RunProgress / completed chan<- RunSummary: Go
channels; payloads are value types or owned-once maps (ToolCounts
transfers ownership to the summary at runner.go:413 and isn't mutated
after).
- cmd/table.go:runPool display state (statusRows, renderedRows):
mutex-protected by statusMu; labelIndex is built once before
goroutines spawn and never written.

Safe — process-global but written only from the main goroutine

- os.Setenv (internal/secret/validate.go:71): called from
secret.ValidateSecrets → resolveRequirement. Call sites are inside
cmd.newRunner/cmd.newRunnerFromAgent (cmd/table.go:125, 228), which
the CLI invokes before runner.RunPool dispatches workers.
Even the per-role resolveRunner calls in cmd/report.go:213 happen in
the task-building loop, single-threaded.
Rule to preserve: os.Setenv is never called from a pool goroutine.

Safe — package-level vars are effectively immutable

Grep for ^var  across internal/runner, internal/agent,
internal/container, internal/streamutil, internal/calldb,
internal/secret, cmd:

- parseClaudeLine = streamutil.ParseClaudeLine (claude.go:17) — function
pointer assigned at init, never reassigned outside tests.
- parseStreamLine = streamutil.ParseClaudeLine (events.go:30) — same.
- knownErrors []string (format.go:10) — read-only literal.
- keyringOnce, keyringAvail (secret/store.go:33) — sync.Once-guarded.

No sync.Pool, no shared json.Decoder, no cached *sql.Stmt.

Minor — shared, but not heap-corrupting

- r.LogFile via appendLog (runner.go:743): O_APPEND|O_CREATE|O_WRONLY
 - single fmt.Fprintln. Writes ≤ PIPE_BUF (4 KiB on Linux, 512 B on
Darwin) are atomic; longer lines can interleave. Current formatting
stays well under the Linux bound; Darwin under typical paths is also
fine. Not a crash vector.
- Direct fmt.Fprintf(os.Stderr, ...) from pool workers (runner.go:216,
272, 309, 311, 366, 529, 532, 579): same PIPE_BUF story — interleaving
is visual, not corrupt. Not a crash vector.

Not-audit-scope (but called out)

The second SIGSEGV (json scanner nil-deref at addr=0) is not explained
by the audit above — the reviewed code is clean. Remaining possibilities:
(a) a race in a dependency (sqlite driver, modernc) that the pool exercises
only rarely, (b) a Go 1.26.2 runtime/stdlib issue, (c) a race outside this
scope. Follow-up requires the -race binary (the build-all-race
Makefile target c126a33) to capture an actual trace.

Proposed boundary contract

1. Pool workers own their mutable state. Anything a pool goroutine
writes is either stack-local, a parameter it owns, or a per-task clone.
Shared mutable state is forbidden unless it's a thread-safe primitive
with a narrow, documented contract (sync.Once/Mutex/atomic).
2. Runner is read-only after pool dispatch. Once cmd.runPool calls
runner.RunPool(..., r, ...), no Runner field may be written. All
Runner setup happens in the main goroutine, sequentially.
3. Clone at one boundary. Cloning happens at the top of Runner.Run
(or the equivalent single entry point). Resist spreading Clone calls
through sub-helpers.
4. Channels for fan-in/out. Pool workers publish RunProgress and
RunSummary on channels. Payloads must be self-contained (value types
or ownership-transferred maps).
5. Package-level state is read-only after init (function pointers,
constants, sync.Once-wrapped caches). Any writable package var gets a
sync primitive and a comment.
6. Filesystem paths are per-task, keyed by timestamp + RoleID. The
runner log is the one shared append-only sink; keep writes short.
7. Subprocess env is per-task (exec.Cmd.Env). os.Setenv is legal
only in pre-pool construction code.

Enforcement plan (this change)

Deliver the audit as a living document, and add a lightweight regression
guard. No refactor of Runner struct in this iteration — current state
already satisfies the contract.

Files to create/modify

1. CONCURRENCY.md (new, at repo root) — the 7-point contract above,
plus a short "what runs on which goroutine" diagram for the pool path.
Cross-link from DEV.md (which already has an "Architecture" section).
2. internal/runner/runner.go — add // Concurrency: ... doc comment
on the Runner struct (around line 75) summarizing "all fields are
read-only after the Runner is passed to RunPool; mutate during
construction only". Annotate the two fields whose semantics could be
misread: Agent/Container ("cloned per-run inside Run").
3. internal/container/container.go — one-line reminder on the
Container interface: "Implementations must be safe to share under
sync.Once-guarded Prepare and one Clone() per pool worker."
4. internal/runner/race_test.go — add
TestRunPoolRunnerFieldsImmutable: snapshot *Runner with gob (or
field-by-field diff) before RunPool, assert byte-equal after. Uses
MockAgent + nil container. Runs at maxParallel=3, 8 tasks.
Complements the existing TestRunPoolSharedContainer* tests. Will
fail loudly if anyone reintroduces a write to a Runner field during
Run.
5. internal/agent/race_test.go (new or folded into existing) — a
TestClaudeAgentCloneIsolation that verifies CloneWithResolvedTemplates
returns a clone whose Args/Env are independent backing memory
(write to clone's slice, assert original unchanged). We have this for
the template-mutation angle already (race_test.go:23); extend it to
explicitly cover the clone-isolation invariant.

Deferred for separate decision (not in this plan)

- Split Runner into PoolConfig (read-only) + per-task TaskContext
(explicit types encoding the rules). Compile-time enforcement is
appealing but requires touching every command + every test that
constructs a Runner. Worth doing only if we hit another "hidden mutation"
bug.
- Mutex-wrap appendLog / move stderr through a channel-fed logger.
Defer — neither is a crash vector and both add friction for small gains.
- Dedicated goroutine owning CallDB (actor pattern). Defer —
*sql.DB is already safe; no correctness issue.

Verification

1. go build ./... clean.
2. go test -race ./... — all existing + new tests pass.
3. go test -race ./internal/runner -run RunnerFieldsImmutable -count=5 —
new guard test passes.
4. Read through CONCURRENCY.md with fresh eyes; confirm each bullet maps
to a code reality (not aspirational).
5. Revisit the unexplained json.Unmarshal SIGSEGV using make build-all-race and the listmanager repro scenario. If it recurs with a
race report, the report points at the next fix — independent of this
plan.

Files touched

- CONCURRENCY.md — new doc (≤150 lines).
- internal/runner/runner.go — doc comment on Runner struct only.
- internal/container/container.go — doc comment on Container interface only.
- internal/runner/race_test.go — one new test.
- internal/agent/race_test.go — one new test (or extend existing).
- DEV.md — cross-link to CONCURRENCY.md.

No behavior changes. The code already satisfies the contract as of 02c3c95;
this plan codifies the rules and adds a regression guard.