# Phase H: progress telemetry + exec/parallel factoring

Successor to Phase G (which landed the `internal/flow` composition framework and
migrated every agent-running cmd to it). Phase H is the **Task 3 (progress
telemetry)** work from `Feature_prompt_report_fs_refactor.md`, plus a small
amount of code-sharing factoring exec/parallel naturally fall into now that
they live behind the same framework.

If you are picking this up: Phase G commits are `b057d96..d18efca`. Start by
reading `plans/Feature_prompt_report_fs_refactor_phaseG.md` and the `internal/flow/`
package docs.

## Status (2026-06-01)

Phase H is **shipped**. All four planned steps landed plus follow-up cleanup
and one new surfacing pass. CI is green at every commit (0 issues, race-clean
against the 100-bundle stress test).

| Step | Commit  | Notes |
|------|---------|-------|
| H1   | `22cdc5e` | flow.MultiReporter; StdoutReporter.SuppressBundleEnd; dropped execReporter wrapper |
| H2   | `4f53af1` | bundle.jsonl + cmd.md "## Bundle" section; runner Prepare/ExecutePrepared split (deviated from plan — see below); 4 new Reporter methods; wired into every flow-using cmd |
| H3   | `af61dc2` | JSONReporter + `ateam exec --format jsonl --progress-fd`; `exec_id=N` stderr line |
| H4   | `c777bac` | cmd/exec_bundle.go::buildRunner + staticBundle; applied to exec/parallel/report/verify/review/code/auto_setup |
| —    | `c2acb86` | cleanup: stream.jsonl → agent.jsonl rename (Layer A+B); dropped `"v":1` field; simplified BundleLogReporter mutexes |
| —    | `3211207` | `--format jsonl` defaults to stdout (revised plan open item #1) |
| —    | `a119bfd` | serve UI surfaces bundle.jsonl, settings.json, runtime/<exec_id>/ |
| —    | `e85abd7` | simplification pass: filename constants in runner package, shared bundle-event builders, lazy runtime walk for /runs, HasProject() on ResolvedEnv, failedSummary constructor, openProgressFD returns (Writer, Closer) |

### Where reality diverged from the plan

- **Runner split into Prepare + ExecutePrepared** (H2): the plan called for
  `AgentExecStart` to fire before `Executor.Execute`, but `exec_id` is
  allocated *inside* Execute via `InsertCall`. The clean fix was to split
  the runner into `Prepare()` (allocates exec_id + paths) and
  `ExecutePrepared(prepared, …)` (runs the agent). The legacy
  `Execute(ctx, prompt, opts, onProgress)` remains as a thin shim for
  non-flow callers (`auto_roles`, `inspect`, `RunPool`).
- **Dropped `"v":1` envelope field** (`c2acb86`): speculative schema
  versioning that paid no rent. If a breaking change becomes necessary,
  the doc gets a section; we don't tax every line.
- **Renamed `stream.jsonl` → `agent.jsonl`** (`c2acb86`): the plan deferred
  this "to avoid churn"; user pushed back and we did it. The legacy
  pre-rollout `_stream.jsonl` per-role layout (and its
  `IsLegacyStreamFile` detection) is preserved.
- **`--progress-fd` defaults to stdout** (`3211207`): plan open item #1
  leaned "require fd." Revisited and chose the conventional `--format X`
  shape (jq, kubectl -o json, gh --json) — defaults to stdout, takes
  --progress-fd N to redirect.
- **BundleInfo grew WorkDir + Batch** (H2): so reporters can populate the
  bundle_start payload without plumbing RuntimeEnv. Captured in
  `bundle_events.go::bundleStartPayload` after the simplification pass.

### Still on the deferred list (not done in H)

- `ateam tail / cat --source agent|bundle|both` (interleave bundle.jsonl in tail/cat)
- `agent_execs` snapshot columns + `ateam ps --progress`
- `ateam parallel --from-stdin` JSONL exec specs
- DB column rename `stream_file` → `agent_file` (Layer C — requires migration)
- Migrate non-flow callers (`auto_roles`, `inspect`, `RunPool`) off the
  `Execute` shim and onto Prepare + ExecutePrepared
- Runner-owns-cmd.md (today the BundleLogReporter's append-after-finalize
  ordering is comment-asserted, not enforced)
- Gate `exec_id=N` stderr emission on cmd (currently runner-side, fires for
  every cmd that uses the runner)

### Acceptance recap

- ~~Every PromptBundle execution writes a complete bundle.jsonl~~ ✓
- ~~A bundle with PreExec/PostExec produces pre_exec_start/end + post_exec_start/end pairs~~ ✓
- ~~`bundle_end.duration_ms` is bundle wall-time~~ ✓
- ~~cmd.md contains the bundle metadata section~~ ✓
- ~~`ateam exec --format jsonl` emits a valid JSONL stream~~ ✓ (now on stdout by default)
- ~~`exec_id=N` printed on stderr after row allocation~~ ✓
- ~~100-bundle stress test race-clean with BundleLogReporter in the chain~~ ✓
- ~~Net LoC reduction across exec.go + parallel.go + report.go from H4~~ ✓
- **NEW**: serve UI surfaces bundle.jsonl + settings.json + runtime/<exec_id>/ — covered by `a119bfd`

## Pick-up notes

- **Working dir**: `/Users/nicolas/SyncDatabox/nicmac/projects/ateam-small-fixes/`
- **Build/test**: `make build-all`, `make run-ci` (build + race-test + lint + fmt + vet + tidy diff)
- **Race-clean stress test**: `internal/flow/stress_test.go` (100 nested bundles, ~1.1s)
- **Phase G framework entry points**: `internal/flow/flow.go` (Run, RunBundle, PromptBundle, Pipeline, Parallel), `internal/flow/reporter.go` (Reporter interface), `internal/flow/reporters_stdout.go` (StdoutReporter), `cmd/table_reporter.go` (concurrent tableReporter for parallel/report).

## Background — design discussion summary

Phase H went through one round of design-review before scoping. The decisions
below are the durable record so we don't re-litigate.

### Where progress data lives — DB vs files vs stream

**Considered and rejected: separate `progress_events` table.** Per-event INSERT
storm for redundant data; the same events are already in `stream.jsonl`. Would
add a table to maintain, batched writers to tune, and history queries that
overlap what `ateam inspect` already does by reading the on-disk stream.

**Considered and deferred: current-state columns on `agent_execs`.** Add
`current_phase / current_tool / last_event_at / cum_input_tokens / cum_output_tokens
/ est_cost_usd`, throttled UPDATE on each event, exposed via `ateam ps --progress`.
This is cheap and useful for "what is each running agent doing right now."
**Skipped in this phase** — `ateam ps` + `ateam tail` together already give a
reasonable live view, and the snapshot columns can land independently when there's
a concrete consumer ask. Stays as a future micro-phase if the gap shows up.

**Chosen: file-based, dual-stream, layered fidelity.**

```
.ateam/logs/<exec_id>/
  stream.jsonl    raw agent events (per-agent format, unchanged from today)
  bundle.jsonl    NEW — flow lifecycle: bundle/pre/agent/post start+end events
  cmd.md          UPDATED — also captures PromptBundle metadata
  prompt.md, settings.json, ...  (existing, unchanged)
```

`stream.jsonl` stays the name — rename to `agent.jsonl` was considered for
symmetry with `bundle.jsonl` but **deferred** to avoid churn across the
read/write paths that touch it (calldb column `stream_file`, `ateam tail`,
`ateam cat`, `ateam inspect`, web handlers).

### `ateam tail / cat --source agent|bundle|both`

Considered. **Deferred.** Today's `ateam tail` reads `stream.jsonl` only; adding
a `--source` flag that interleaves bundle.jsonl was on the table. Not blocking
the telemetry MVP — consumers wanting both streams either use the live
`--format jsonl` path (single subprocess emits both interleaved with a `source`
field) or read both files post-mortem with their own merger. Adding `--source`
later is additive.

### Why bundle.jsonl exists at all — avoiding redundant agent/bundle logs

Strict rule for v1: **`bundle.jsonl` does NOT duplicate agent-progress or
cumulative-usage events from `stream.jsonl`.** It carries only lifecycle
start/end events (bundle, pre-exec actions, agent execution wrap, post-exec
actions). The ONE redundancy: `agent_exec_end` carries the final wrap totals
(cost, total tokens) so a consumer who only wants the summary can read
bundle.jsonl alone without parsing every progress line in stream.jsonl. Per-event
progress and the cumulative running totals stay in stream.jsonl.

The justification for bundle.jsonl's existence: the flow framework introduced
PreExec/PostExec actions (CheckConcurrentRuns, PrintArtifactPath, future
user-defined scripts) that are NOT visible in `stream.jsonl` — the runner only
knows about the agent process. A slow pre-exec script today is invisible to
`ateam ps` / `ateam tail`. bundle.jsonl makes the framework's lifecycle
observable; nothing more.

### Use cases driving H3 (`--format jsonl`)

Primary use case: **external orchestrators (Python framework, shell scripts,
CI) driving N parallel `ateam exec` subprocesses and wanting a structured
event stream per subprocess.**

```python
proc = subprocess.Popen(
    ["ateam", "exec", "--format", "jsonl", "--progress-fd", "3", ...],
    pass_fds=(3,))
for line in os.fdopen(3):
    event = json.loads(line)
    update_progress(event)
```

This is the simplest possible UX for orchestrators: one subprocess per logical
run, one pipe to read, both agent and bundle events interleaved with a `source`
discriminator. No `ateam tail` + `exec_id` correlation dance.

The alternative — spawn-then-tail — works because tail reads the same files,
but requires more orchestration code. We document both but recommend the
direct subprocess pipe pattern.

### Scoped OUT

- **`ateam parallel --from-stdin` consuming JSONL exec specs** — considered
  for code-sharing + per-task flexibility. Not in this phase. Rationale: Python
  framework (or shell) can drive N `ateam exec` subprocesses today, and gains
  the H3 telemetry "for free" once H3 lands. Native `parallel` JSONL stdin is a
  reasonable future ask but doesn't block telemetry.
- **`ateam run pipeline.json`** — explicit non-goal. The Python framework
  (`plans/python_framework_examples/ateam.py`) owns the workflow-orchestration
  altitude. A JSON-descriptor runner inside ateam would be a worse Python.
- **`agent_execs` snapshot columns + `ateam ps --progress`** — see above; deferred
  to a follow-up micro-phase.
- **`stream.jsonl` → `agent.jsonl` rename** — deferred for the same reason.
- **`tail/cat --source agent|bundle|both`** — deferred.

## Design decisions (locked)

### Reporter composition

`flow.MultiReporter(rs ...Reporter) Reporter` fans every callback to children.
Each child owns its own thread-safety (same contract as today's `Reporter`).
Nil children are silently skipped. Children are called in declaration order;
slow children delay siblings — fine at our scale.

This kills the `execReporter` wrapper-with-override pattern from Phase G
(`cmd/exec.go`). Instead:

```go
// in cmd/exec.go after H1:
Reporter: flow.MultiReporter(
    &flow.StdoutReporter{Stream: showStream, SuppressBundleEnd: true},
    &flow.BundleLogReporter{Dir: logsDir},  // H2
    jsonReporter,                            // H3, only when --format jsonl
)
```

`StdoutReporter` gains a `SuppressBundleEnd bool` field (the "wrong defaults"
problem the review pass flagged): when true, BundleEnd is silent. exec sets it
because `printExecSummary` is the richer end-of-run output.

### bundle.jsonl event vocabulary (v1)

Each line: `{ts, source:"bundle", kind, ...payload}`. Closed `kind` set:

| `kind` | Payload fields |
|---|---|
| `bundle_start` | `name`, `role`, `action`, `work_dir`, `batch` |
| `bundle_end` | `state` (continue/skip/error), `reason`, `duration_ms` |
| `pre_exec_start` | `action_type`, `index` |
| `pre_exec_end` | `action_type`, `state`, `reason`, `duration_ms` |
| `agent_exec_start` | `exec_id`, `model`, `prompt_bytes` |
| `agent_exec_end` | `exec_id`, `duration_ms`, `is_error`, `exit_code`, `cost_usd`, `input_tokens`, `output_tokens` |
| `post_exec_start` | `action_type`, `index` |
| `post_exec_end` | `action_type`, `state`, `reason`, `duration_ms` |

Notes:
- `action_type` = Go type name (e.g. `"CheckConcurrentRuns"`). For future
  user-defined script hooks this would be the script path; for now it's a
  framework-action identifier.
- `state` mirrors `flow.FlowState`: `"continue" | "skip" | "error"`.
- `ts` is unix millis.
- `agent_exec_*` are framing events. The bundle.jsonl reader sees the agent
  execution started/ended; the per-event detail (tools, tokens, thinking) lives
  in `stream.jsonl` under the same exec_id.
- `agent_exec_end`'s wrap totals (`cost_usd`, `input_tokens`, `output_tokens`)
  are the durable summary — the ONE intentional redundancy with stream.jsonl.

Forward compatibility: unknown `kind` values are forward-compatible. If a
breaking change to the payload shape ever becomes necessary, this doc gets
a new section; we don't carry a schema-version field on every line.

### Reporter interface additions

Two new methods (action lifecycle):

```go
type Reporter interface {
    StageStart(StageInfo)
    StageEnd(StageInfo, StageOutcome)
    StepSkipped(parent StageInfo, stepName, reason string)
    BundleStart(BundleInfo)
    BundleEnd(BundleInfo, Result)
    AgentEvent(BundleInfo, runner.RunProgress)
    // NEW in H2:
    ActionStart(b BundleInfo, phase ActionPhase, actionType string, index int)
    ActionEnd(b BundleInfo, phase ActionPhase, actionType string, index int, flow Flow, duration time.Duration)
}

type ActionPhase int
const (
    PreExec ActionPhase = iota
    PostExec
)
```

`BaseReporter` no-ops both; existing reporters (StdoutReporter, tableReporter)
inherit the no-op. BundleLogReporter and JSONReporter implement them.

`PromptBundle.execute` in `internal/flow/flow.go` fires these around each
PreExec / PostExec action call.

The agent-execution span is bracketed by `agent_exec_start/end` events too —
these don't need new Reporter methods because they correspond to the existing
`Executor.Execute` call boundaries inside `PromptBundle.execute`. The
`BundleLogReporter` emits them directly from its `AgentEvent` callback's
first/last event arrival, OR from explicit calls placed in `PromptBundle.execute`
right before/after `env.Executor.Execute(...)`. **Choose the latter for
correctness** — agent backends that emit zero events would otherwise skip the
start/end frame.

So add ONE more pair to the interface:

```go
AgentExecStart(b BundleInfo, opts runner.RunOpts)
AgentExecEnd(b BundleInfo, summary runner.RunSummary)
```

Total: 4 new Reporter methods. All no-op'd by BaseReporter, so existing
implementations are unaffected.

### cmd.md additions for PromptBundle metadata

Append a `## Bundle` section to the existing `cmd.md` written by the runner:

```markdown
## Bundle

- Name: <bundle.Name>
- Role: <env.Role>
- Action: <env.Action>
- WorkDir: <env.WorkDir>
- Batch: <env.Batch>

### RunOpts
<JSON of runner.RunOpts, pretty-printed>

### PreExec actions
1. CheckConcurrentRuns {If: true, Action: "verify", Roles: []}
2. ...

### PostExec actions
1. PrintArtifactPath {Label: "Verification report", Path: "..."}
2. ...
```

The runner writes `cmd.md` today (see `internal/runner/runner.go`). The
PromptBundle metadata is appended either by the runner itself (if we plumb the
bundle through to Execute) or by `BundleLogReporter.AgentExecStart` (which has
the BundleInfo). Lean toward the latter — keeps the runner unaware of flow.

### JSON output channel (H3)

`ateam exec --format jsonl --progress-fd=N`:

- Emits the same lines bundle.jsonl receives (`source:"bundle"`) PLUS each
  `agent.jsonl` line wrapped with `source:"agent"` and the existing per-agent
  fields preserved verbatim.
- Default `--progress-fd` value: `2` (stderr) if not specified — but recommend
  `3` (caller passes via Popen's `pass_fds`) in docs so the agent's normal
  output stays on its usual channels.
- `exec_id=<int>` printed on stderr right after `InsertCall`, **always**, no
  flag gate. ~10 lines of code; orchestrators trivially grep `^exec_id=`.

`--format` is the discriminator for future formats; v1 only supports `jsonl`
but the flag space is reserved.

Only `ateam exec` gets `--format jsonl` in H3. `ateam parallel` would need it
when it migrates to a JSONL-spec stdin reader; deferred along with that work.

### exec / parallel factoring

`cmd/exec_bundle.go` (new) owns `buildBundle`:

```go
type BundleSpec struct {
    Prompt          string
    Role            string
    Action          string
    Profile         string
    Agent           string
    WorkDir         string
    Timeout         int
    Verbose         bool
    Batch           string
    DockerAutoSetup bool
    ContainerName   string
    OverridesShared RunnerOverrides
    // future: PreExec []string, PostExec []string  (script paths)
}

func buildBundle(env *root.ResolvedEnv, spec BundleSpec) (flow.PromptBundle, *runner.AgentExecutor, error) {
    // resolveRunner -> applyRunnerOverrides -> assemble PromptBundle
}
```

Today `cmd/exec.go`, `cmd/parallel.go`, and `cmd/report.go` each duplicate the
resolveRunner → applyRunnerOverrides → PromptBundle assembly chain. After H4,
all three call `buildBundle` (report calls it N times in the per-role loop).
The per-role profile resolution in report stays in report (BundleSpec doesn't
need to know about that).

## Implementation steps

### H1 — Reporter composition + drop execReporter

Files:
```
internal/flow/reporters_multi.go         NEW (~40 lines)
internal/flow/reporters_multi_test.go    NEW
internal/flow/reporters_stdout.go        +SuppressBundleEnd field, BundleEnd respects it
cmd/exec.go                              drop execReporter type; use MultiReporter + SuppressBundleEnd
```

`flow.MultiReporter` is a slice + dispatch:

```go
type MultiReporter []Reporter

func (m MultiReporter) StageStart(s StageInfo) {
    for _, r := range m { if r != nil { r.StageStart(s) } }
}
// ... same shape for every method
```

Acceptance:
- All existing reporter tests stay green.
- `cmd/exec.go` no longer has `execReporter` (the 5-line wrapper deleted).
- StdoutReporter's `SuppressBundleEnd: true` produces no "Done (...)" line.
- New tests: MultiReporter fans correctly, nil children skipped, ordering preserved.

Estimate: ~80 lines new, -20 deleted.

### H2 — bundle.jsonl + cmd.md PromptBundle metadata + action lifecycle Reporter callbacks

Files:
```
internal/flow/reporter.go                add 4 new Reporter methods + BaseReporter no-ops
internal/flow/flow.go                    PromptBundle.execute fires ActionStart/End around each Pre/Post action; fires AgentExecStart/End around the Execute call
internal/flow/reporters_bundle_log.go    NEW — BundleLogReporter
internal/flow/reporters_bundle_log_test.go  NEW
internal/runner/runner.go                expose hook for appending to cmd.md after initial write (or document the path so reporter can append)
cmd/verify.go, cmd/review.go, cmd/auto_setup.go, cmd/code.go, cmd/exec.go,
cmd/report.go, cmd/parallel.go           wire BundleLogReporter into each Reporter chain via MultiReporter
```

`BundleLogReporter{Dir string}` opens `<Dir>/<exec_id>/bundle.jsonl` on the
first `AgentExecStart` (when exec_id is first available), buffers events
written before that into memory, flushes on AgentExecStart, continues writing
synchronously per event after that. Closes on `BundleEnd`. Errors writing to
disk go to stderr; never block the run.

Why open on AgentExecStart rather than BundleStart: the `<exec_id>` dir doesn't
exist until the runner's `InsertCall` completes. AgentExecStart fires inside
PromptBundle.execute right before Executor.Execute is called, AFTER InsertCall
in the production path. The `bundle_start` event written before that goes into
the buffer.

cmd.md append: BundleLogReporter's AgentExecStart also appends the "## Bundle"
section to `<Dir>/<exec_id>/cmd.md`. Same file the runner writes; the reporter
appends after the runner closes its initial write.

Acceptance:
- Every PromptBundle execution writes a complete bundle.jsonl with at least
  bundle_start, agent_exec_start, agent_exec_end, bundle_end.
- A bundle with PreExec / PostExec actions produces pre_exec_start/end and
  post_exec_start/end pairs for each.
- `bundle_end` `duration_ms` is the wall-time from bundle_start to bundle_end.
- cmd.md contains the bundle metadata section.
- All existing cmd tests pass — the new file is a strict addition, not a
  visible change.

Tests:
- Unit: BundleLogReporter fires the full event sequence against a fake
  Executor (no DB needed if AgentExecStart can fire before file open).
- Integration: full PromptBundle flow with mock agent, assert bundle.jsonl
  contents + cmd.md additions.
- Race: the stress test (100 nested bundles) gains a BundleLogReporter in
  the MultiReporter; assert no file collisions, no races, all 100
  bundle.jsonl files complete.

Estimate: ~300 lines new + tests.

### H3 — exec --format jsonl --progress-fd + exec_id stderr line

Files:
```
internal/flow/reporters_json.go          NEW — JSONReporter
internal/runner/runner.go                emit "exec_id=<id>" to stderr after InsertCall (always)
cmd/exec.go                              add --format and --progress-fd flags; wire JSONReporter into MultiReporter when --format=jsonl
docs/COMMANDS.md                         document exec --format and --progress-fd
README.md                                if external-orchestrator section exists, add a snippet
```

JSONReporter emits the SAME content BundleLogReporter writes to bundle.jsonl
(every `source:"bundle"` event), PLUS each runner.RunProgress event wrapped as
`{ts, source:"agent", exec_id, ...progress fields}`. Single line stream
on the chosen fd.

The reporter writes synchronously to the fd — no buffering. Orchestrators
flush by reading.

Acceptance:
- `ateam exec --format jsonl --progress-fd 3 -- "echo hi"` against the mock
  agent emits a valid JSONL stream with bundle + agent events interleaved.
- The first stderr line after Resolve is `exec_id=<int>` (always, no flag).
- `--format` value other than `jsonl` errors with "unknown format".
- `--progress-fd` validation: must be a positive integer; the fd must be
  writable (caller's responsibility — surface OS error if not).

Tests:
- Unit: JSONReporter formats events correctly; bundle + agent interleaving;
  unknown phases pass through.
- Integration: subprocess test using `os.Pipe()` to read --progress-fd output;
  parse JSONL; assert presence of bundle_start, agent_exec_start, agent_exec_end,
  bundle_end.

Estimate: ~150 lines new + tests + ~30 docs.

### H4 — cmd/exec_bundle.go::buildBundle factoring

Files:
```
cmd/exec_bundle.go                       NEW
cmd/exec.go                              call buildBundle
cmd/parallel.go                          call buildBundle per task
cmd/report.go                            call buildBundle per role (with per-role profile injected via BundleSpec.Profile)
```

`buildBundle` consolidates the resolveRunner + applyRunnerOverrides + bundle
construction across the three cmds. ~150-line refactor; should be a strict
LoC reduction.

Acceptance:
- All cmd tests pass.
- `git diff --stat` shows net negative LoC for exec.go + parallel.go +
  report.go combined.
- `cmd/exec_bundle.go` is well-tested in isolation.

Tests:
- Unit: buildBundle correctness for each cmd's spec shape.
- Existing cmd tests (TestVerify, TestReport, TestParallel*) regress green.

Estimate: ~150 lines new, ~200 lines removed across the three cmds.

## Open items to lock in during implementation

1. **`--progress-fd` default value**: stderr (2) for simplicity, or require explicit fd to avoid mixing with the existing exec stderr stream? Lean toward requiring it (no default) so users opt in deliberately.

2. **JSONReporter format inversion**: should `--format jsonl` also imply
   `--no-stream --no-summary` so the orchestrator's stdout/stderr stay clean?
   Probably yes — passing `--format jsonl` says "I'm consuming structured
   events, don't print human-facing decoration." Document this.

3. **BundleLogReporter directory resolution**: `<Dir>` is `<projectDir>/.ateam/logs/`
   in normal mode. In scratch mode (no project) it's `<orgDir>/logs/`. Match
   the existing `runner.AgentExecutor.StateDir()` logic — the reporter can
   take the StateDir at construction.

4. **What happens when the same bundle runs twice with the same exec_id**:
   it can't — InsertCall guarantees a unique exec_id. BundleLogReporter
   doesn't need to handle collision.

5. **JSON event-time ordering between agent and bundle**: agent events
   typically come from a goroutine inside the runner; bundle events from the
   bundle-execute path. They CAN interleave out-of-order if the writer flushes
   late. Confirm: synchronous writes from BOTH sources, single mutex in
   JSONReporter to serialize line writes.

## Estimated total

| Phase | Net LoC | Commits |
|---|---|---|
| H1 | +60 | 1 |
| H2 | +320 | 1 |
| H3 | +180 | 1 |
| H4 | -50 | 1 |
| **Total** | **+510** | **4** |

Phase H is small relative to Phase G (+4356/-1432). The framework is in
place; H is mostly wiring.

## Sequencing

```
H1 (MultiReporter)
  └─ H2 (bundle.jsonl + Reporter action callbacks)
       ├─ H3 (--format jsonl + exec_id stderr)
       └─ H4 (buildBundle factoring)
```

H3 and H4 are independent after H2; either order is fine. H1 must precede H2
because H2 wires multiple reporters per cmd via MultiReporter.
