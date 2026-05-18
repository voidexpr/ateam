# Summary

The architecture is reasonable for a CLI of this size: a clear `main → cmd → internal/{root,runtime,runner,agent,calldb,container,prompts,web}` flow, an explicit Concurrency contract on the `Runner`, and a single Cobra root that fans out to ~26 subcommands. The main structural debts are (1) two oversized "god" files (`internal/runner/runner.go` 1281 LOC, `internal/web/handlers.go` 1433 LOC, `cmd/table.go` 1390 LOC) doing many things at once, (2) a mutation-based construction protocol for `Runner` and `Agent` that depends on call ordering, and (3) duplicated per-command setup boilerplate that pulls the same six pieces together in `exec/report/review/code/verify/parallel.go`. None of this blocks development today, but each finding makes the next change in that area harder than it needs to be.

# Findings

## 1. `Runner` is a god struct with a god `Run` method
- **Location**: `internal/runner/runner.go:85-110` (struct, ~30 fields), `internal/runner/runner.go:195-632` (Run, ~440 LOC)
- **Severity**: HIGH
- **Effort**: LARGE
- **Description**: `Runner` carries agent, container, project paths, sandbox config, args (inside/outside container), CallDB, profile metadata, container name + source, project ID, stall settings — three or four orthogonal concerns mixed into one record. `Run` then linearly: inserts the DB row, builds extra-args, computes per-exec dirs, writes settings, clones the container, resolves templates, builds the request, writes prompt+cmd files, spawns the agent, consumes events (with a stall watchdog), classifies failures, scans the stream for fallback usage data, writes fallback output, finalizes (promote files + rerender cmd.md + DB update). The function is hard to test in pieces (everything funnels through one call), hard to extend (new behaviors keep growing this method), and the concurrency contract documented at line 71-84 — "WRITTEN only during construction; READ-ONLY afterwards" — is enforced only by convention; multiple callers still mutate fields post-construction (`cr.CallDB = db` in `cmd/report.go:292`, `applyContainerNameOverride` in `cmd/table.go:181`, `setSourceWritable` in `cmd/table.go:873`).
- **Recommendation**: Split `Runner` into (a) `RunnerConfig` (immutable, built once) holding agent + container + sandbox + paths + DB, and (b) an `Execution` struct created per `Run` holding the live state (callID, dirs, files, cumulative counters, stall timer). Decompose `Run` into phases: `prepare()` (insert DB row, mkdirs, settings, container setup), `execute()` (stream loop + stall watchdog), `finalize()` (promote, rerender, DB update). Move the fallback "stream tail for usage" recovery into a helper. The exposed `Run` signature can stay the same.

## 2. `Agent` interface requires mutation with ordering rules
- **Location**: `internal/agent/agent.go:40-58`, callsites in `cmd/table.go:760-861`
- **Severity**: HIGH
- **Effort**: MEDIUM
- **Description**: `Agent` mandates `SetModel`, `SetEffort`, `SetMaxBudgetUSD` mutators, each documented as "MUTATES — call before the Agent is shared with a pool." `CloneWithResolvedTemplates` partly mitigates this by handing each pool worker its own clone, but the contract is fragile: any future caller that forgets the ordering creates a data race that won't show up under `go test -race` unless the exact path is exercised. The same mutation pattern bleeds into `applyRunnerOverrides` (`cmd/runner_overrides.go`) and the per-role override flow in `cmd/report.go:235-252` where a *second* runner is built and overrides reapplied.
- **Recommendation**: Move `Model`/`Effort`/`MaxBudgetUSD` into the `Request` (per-run, already passed by value through `runner.Runner.Run`). Each agent reads them off `Request`; setters and the "before sharing" contract disappear. Where it cannot move to `Request` (e.g. `Env`), wrap the agent in a small immutable `AgentSpec` and have `CloneWithResolvedTemplates` return a fresh agent built from spec+overrides.

## 3. `cmd/table.go` is a kitchen-sink helper file
- **Location**: `cmd/table.go` (1390 LOC, ~40 unrelated helpers)
- **Severity**: HIGH
- **Effort**: MEDIUM
- **Description**: One file mixes: project DB open/require helpers, runner construction (`newRunner`, `newRunnerFromAgent`, `runnerFromAgentConfig`, `minimalRunnerFromAgentConfig`, `newRunnerDefault`, `resolveRunner`, `resolveRunnerMinimal`), container construction (`buildContainer`, `findLinuxBinary`, `crossBuildIfPossible`, `dockerCp`, `dockerExecOutput`, `resolveContainerName`), agent construction (`buildAgent`, `buildPricingFromConfig`, `mergedPricingFromConfig`), pricing helpers, sandbox/git helpers (`gitWriteDirs`, `execGitCmd`, `resolveVolumePath`, `splitVolumeSpec`), flag-registration helpers (`addProfileFlags`, `addContainerNameFlag`, `addBudgetFlags`, `addCheaperModelFlag`, `addVerboseFlag`, `addForceFlag`, `addDockerAutoSetupFlag`), override appliers (`applyContainerName`, `applyEffort`, `applyModel`, `applyModelOverrides`, `applyMaxBudgetUSD`, `setSourceWritable`), the runner pool driver (`runPool` + `poolDisplayOpts`), and dry-run printers (`printDryRunInfo`, `printDryRunSecrets`, `logIsolationResults`). The name "table" no longer describes anything; the file is whatever didn't fit elsewhere.
- **Recommendation**: Split into 4–5 files in `cmd/`:
  - `cmd/runner_build.go` — `newRunner*`, `resolveRunner*`, `runnerFromAgentConfig`, `applyContainerName*`.
  - `cmd/container_build.go` — `buildContainer`, `findLinuxBinary`, `crossBuildIfPossible`, `dockerCp`, `resolveVolumePath`, `gitWriteDirs`.
  - `cmd/flags.go` — `add*Flag` helpers and `parseBudgetUSD`.
  - `cmd/dry_run.go` — `printDryRunInfo`, `printDryRunSecrets`, `logIsolationResults`.
  - Keep `cmd/table.go` for the tabwriter/`printDone`/ExitError code that actually justifies the name (~80 LOC), or fold into a new `cmd/output.go`.

## 4. `internal/web/handlers.go` mixes feature areas
- **Location**: `internal/web/handlers.go` (1433 LOC, 30+ handlers)
- **Severity**: MEDIUM
- **Effort**: MEDIUM
- **Description**: Handlers for overview, reports, runs, run-file viewing (markdown/stream rendering), supervisor outputs (review/verify), history (legacy + DB), cost, prompts, and code sessions live in one file along with shared helpers (`resolveRunFiles`, `enrichRuns`, `resolveHistoryFile`, `parseExecHistoryFilename`, `buildSessions`). Adding a new view means scrolling 1.4k LOC to find adjacent handlers; the file is also the natural touchpoint for path-traversal review (`isPathWithin`, `serveHistoryFile`) but security helpers are buried mid-file.
- **Recommendation**: Split by feature: `handlers_overview.go`, `handlers_runs.go` (run + run_file + enrich helpers), `handlers_history.go`, `handlers_supervisor.go` (review/verify shared template), `handlers_sessions.go`, `handlers_cost.go`, plus `handlers_safety.go` for path-traversal helpers. Same package; pure file split. No behavior change.

## 5. Per-command boilerplate is duplicated across 6+ files
- **Location**: `cmd/exec.go`, `cmd/report.go:132-205`, `cmd/review.go:152-228`, `cmd/verify.go:100-160`, `cmd/code.go`, `cmd/parallel.go`
- **Severity**: MEDIUM
- **Effort**: MEDIUM
- **Description**: Every primary action does the same opening sequence: `root.Resolve(orgFlag, projectFlag)` → `prompts.ResolveOptional(extraPrompt)` → `resolveRunner(...)` → `applyRunnerOverrides(...)` → `openProjectDB(env)` + `defer db.Close()` → `cr.CallDB = db` → optional `checkConcurrentRunsEnv` gated by `--force` → `cmdContext()` + `defer stop()`. That's ~30 lines repeated five or six times. Drift is already visible: `verify.go` adds `setSourceWritable(cr)` and `report.go` builds a per-role runner with its own override pass that is *almost* the same code (`cmd/report.go:235-252`).
- **Recommendation**: Introduce `cmd.prepareRun(action, opts CommonRunOpts) (*runContext, error)` returning `{env, runner, db, ctx, cancel}` with `runContext.Close()` handling `db.Close()` and `cancel()`. Each command then has 3-line setup instead of 30. The pool variant (`report`, `parallel`, `code`) can extend with `prepareRunBatch` returning the same plus a batch token + pre-dispatch budget closure.

## 6. `runner` re-exports `display` helpers as a back-compat facade
- **Location**: `internal/runner/runner.go:24-26,44-45,888,961-976`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: `runner.TimestampFormat`, `runner.ExpandHome`, `runner.Truncate`, `runner.FormatDuration`, `runner.ParseTimestampPrefix` are one-line forwarders to `internal/display` with comments like "kept as an alias for backward compatibility." Eleven cmd files and a couple of tests still import via the runner facade rather than `display` directly (see `cmd/resume.go:334`, `cmd/code.go:180`, `cmd/exec.go:258`, `cmd/cat.go:76`, `cmd/pool_status.go:94,152`). The indirection makes the `runner` package look larger than it is and obscures which functions are real runner concerns.
- **Recommendation**: Inline the facade — update the ~11 callsites to import `internal/display` and delete the forwarders. Mechanical refactor.

## 7. `internal/root` mixes resolution, project init, and DB migration
- **Location**: `internal/root/{resolve.go,init.go,migrate_logs.go}`
- **Severity**: MEDIUM
- **Effort**: MEDIUM
- **Description**: `root` is the natural place for "where am I?" discovery (`Resolve`, `Lookup`, `FindOrg`, `FindProject`) and for `ResolvedEnv` (the path bag every command consumes). It also owns `InstallOrg`, `InitProject`, `EnsureRoles` — project-scaffolding actions that happen once at install/init time — and `MigrateLogsLayout`, a sentinel-guarded migration that touches the filesystem AND the `calldb` schema (called from `cmd/table.go:88,112`). Three lifecycle stages (every command vs install-time vs migration) coexist with no separation, and `migrate_logs.go` is the only reason `root` imports `calldb`.
- **Recommendation**: Keep `root` as discovery/resolution only. Move `InstallOrg`/`InitProject`/`EnsureRoles` to a sibling `internal/install` (or `internal/project`). Move `MigrateLogsLayout` to either `internal/calldb` (DB schema migrations) or a dedicated `internal/migrate`. Eliminates the `root→calldb` import edge and makes "what runs at install vs every command" obvious from the package layout.

## 8. `internal/runner` mixes execution with stream formatting/parsing
- **Location**: `internal/runner/{format_stream.go,format_stream_html.go,format_helpers.go,parse_stream.go,tailer.go,events.go,format.go}` (~2000 LOC)
- **Severity**: MEDIUM
- **Effort**: MEDIUM
- **Description**: The runner package owns both "drive an agent and collect events" and "pretty-print or HTML-render a JSONL stream after the fact." The stream-formatting code is consumed by `cmd/cat.go`, `cmd/tail.go`, and `internal/web/handlers.go:593` (the HTML renderer used for the run log view), none of which need to *run* anything. Mixing them means: any package that wants to display a saved stream pulls in the entire runner (agent, container, calldb, fsclone, gitutil) transitively, and grepping `internal/runner` for a real execution change drowns in formatter code.
- **Recommendation**: Extract formatters into `internal/streamfmt` (or `internal/runner/format` as a subpackage). Move: `format_stream.go`, `format_stream_html.go`, `format_helpers.go`, `parse_stream.go`, `tailer.go`. Leave `events.go` (PhaseInit/Tool/Result constants used by the live progress UI) where it is or push to `runner/events`. After the split, `internal/web` and `cmd/{cat,tail}` import only the formatter package; runner becomes a pure execution engine.

## 9. `internal/agent` reaches into `internal/secret` for credential auditing
- **Location**: `internal/agent/claude_auth.go` (imports `internal/secret`)
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: Of the entire `agent` package, only `claude_auth.go` imports `secret` — to call `secret.MaskValue` and `secret.NewResolver` while auditing where Anthropic credentials are coming from. This is the *only* cross-cutting edge from the agent core out to a non-execution concern, and it exists because the `ateam claude` / `ateam agent-config` audit feature was implemented inside the agent package instead of next to those commands.
- **Recommendation**: Move `claude_auth.go` (and `claude_auth_test.go`) into a new `internal/agentaudit` package (or `cmd/auth_audit.go` if it has no internal callers). The agent core then depends on only `streamutil` + `container`, matching its narrow purpose.

## 10. Each `cmd/*.go` file declares a 15-28-variable module-level flag block
- **Location**: `cmd/exec.go:18-37`, `cmd/report.go:16-37`, `cmd/code.go`, `cmd/review.go`, `cmd/all.go:9-35`, `cmd/eval.go`, …
- **Severity**: LOW
- **Effort**: MEDIUM
- **Description**: Cobra's standard pattern is module-level `var foo string` plus `cmd.Flags().StringVar(&foo, ...)`. Each command repeats this for 15-28 flags, then immediately bundles them into a typed `Options` struct (`ReportOptions`, `ReviewOptions`, `VerifyOptions`, `CodeOptions`, `AllOptions`-style) and passes the struct into `runXxx`. The module-level vars are just a name-collision-prone way to feed cobra; they're also a soft barrier to parallel-running two commands in the same process (relevant for `ateam all`, which sequentially mutates the global state of `runReport` → `runReview` → `runCode` → `runVerify`).
- **Recommendation**: Use cobra's `RegisterFlagCompletionFunc` + a small helper that binds an `Options` struct field per flag, or just declare the flag vars inside `init()` and capture into a single struct that `RunE` reads via closure. Lower priority than the other splits.

## 11. `runtime` package mixes parsing with project-setup concerns
- **Location**: `internal/runtime/config.go:430-545` (`ResolveDockerfile`, `ResolvePrecheckCmd`, `WriteOrgDefaults`), `internal/runtime/dockergen.go`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: `internal/runtime` ought to be "parse runtime.hcl, hand back typed config." It also handles writing org defaults, locating per-role Dockerfiles on disk, and auto-generating Dockerfiles (`AutoSetupDockerfile`). The first half is a pure-data concern; the second is filesystem/I/O coupled to project layout and is only called from `cmd/install.go` and `cmd/table.go:454`.
- **Recommendation**: Lift Dockerfile generation/resolution into `internal/container` (which already owns Docker concerns) or a sibling `internal/dockersetup`. Keep `runtime` pure HCL parsing + inheritance resolution.

## 12. Auto-chain between `report → review` and `code → verify` is wired inside the command implementations
- **Location**: `cmd/report.go:352-355` (`opts.Review` calls `runReview` inline), `cmd/code.go` (verify chain), `cmd/all.go` (pipeline orchestrator that explicitly disables the chains via `NoVerify: true` to avoid double runs)
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: A higher-level command (`runReport`) directly invokes another command's entry point (`runReview`), and `runCode` does the same for `runVerify`. `runAll` then has to know to *suppress* those chains (`NoVerify: true` at `cmd/all.go:181`) so a verify doesn't run twice. The implicit cross-command calls turn each command into both a leaf and an orchestrator, and the suppression flag is a tell that the design is being fought rather than followed.
- **Recommendation**: Lift the chain decisions up into `runAll` (or a small `cmd/pipeline.go`). `runReport` and `runCode` should do one thing. `--review` on `ateam report` becomes a shorthand wired in `runAll`-style code, not an embedded call. Removes the need for `NoVerify` and similar suppression flags.

# Quick Wins

1. **Remove the `runner→display` facade** (Finding 6) — mechanical edit across ~11 cmd files; trims `runner` surface area.
2. **Split `cmd/table.go` into 4 focused files** (Finding 3) — pure file-level reorganization, no semantic change, makes the next refactor of runner construction navigable.
3. **Move `claude_auth.go` out of `internal/agent`** (Finding 9) — single file move, eliminates the only outbound coupling from the agent core to non-execution concerns.
4. **Extract `cmd.prepareRun` to dedupe per-command setup** (Finding 5) — 30→3 lines per command, reduces drift between `report`/`review`/`verify`/`code`/`exec`/`parallel`.

# Project Context

- **Language / build**: Go (single module `github.com/ateam`). `make build`, `make test`, `make test-docker`. Linter: `.golangci.yml`. Single binary entry at `main.go`.
- **CLI framework**: cobra. Root in `cmd/root.go` aggregates ~26 subcommands; each `cmd/<name>.go` registers flags as module-level vars and delegates to `run<Name>(opts)` in the same file.
- **Layout**:
  - `cmd/` — Cobra subcommands and the shared helpers in `cmd/table.go` (1390 LOC, multi-purpose).
  - `internal/root/` — Project + org discovery (`Resolve`, `ResolvedEnv`), project init, log-layout migration.
  - `internal/runtime/` — HCL parsing for `runtime.hcl` (agents, containers, profiles, inheritance) + Dockerfile resolution helpers.
  - `internal/runner/` — Execution engine. `runner.go` (1281 LOC) drives one agent run; `pool.go` parallelizes; `format_stream*.go`, `parse_stream.go`, `tailer.go`, `events.go` handle stream formatting (mixed concern — see Finding 8).
  - `internal/agent/` — `Agent` interface + Claude/Codex/Mock implementations; `claude_auth.go` is the only file pulling `internal/secret`.
  - `internal/container/` — Docker/docker-exec containers, prepare guard.
  - `internal/calldb/` — SQLite persistence (`agent_execs` table). `calldb.go` includes inline migrations.
  - `internal/prompts/` — 4-level prompt fallback (project → org → org-defaults → embedded), review selector, project info, trace helpers.
  - `internal/web/` — Embedded web UI; `handlers.go` (1433 LOC) covers every view.
  - `internal/{secret,display,gitutil,streamutil,fsclone,eval,config}/` — focused helpers, all under 500 LOC.
  - `defaults/` — embedded prompts/Dockerfile/runtime.hcl via `//go:embed`.
- **Key data flow**: `cmd/<x>.go` → `root.Resolve` → `runtime.Load` → `cmd.resolveRunner` → `runner.Runner` (single or via `runner.RunPool`) → `agent.Agent.Run` → JSONL events parsed by `agent` + persisted to `logs/<exec_id>/stream.jsonl` + `calldb`. Promoted artefacts land in `runtime/<exec_id>/` and clone to canonical paths (e.g. `roles/<id>/report.md`).
- **Concurrency**: documented contract in `internal/runner/runner.go:71-84` and `CONCURRENCY.md`; `Runner` fields are convention-only read-only after construction, `Agent`/`Container` are cloned per pool exec via `CloneWithResolvedTemplates`/`Clone`. SQLite serialized to one writer (`SetMaxOpenConns(1)`).
- **No prior architecture report on disk** at `.ateam/runtime/{1,2}/`; this report is the baseline.
